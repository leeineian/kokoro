package proc

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/big"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

// Constants
const (
	LoopWebhookName = "LoopHook"
	LoopBatchSize   = 10 // Balanced for 200+ channel performance/stability
)

var (
	// Global semaphore to strictly limit webhook management operations (creation/deletion)
	// across all tasks to ensure we stay well within Discord's guild management limits.
	webhookOpSem = make(chan struct{}, 1)
)

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogLoopManager, func(ctx context.Context) { InitLoopManager(ctx, client) })
	})
}

// WebhookData holds a webhook and its channel info
type WebhookData struct {
	WebhookID    snowflake.ID
	WebhookToken string
	ChannelName  string
}

// ChannelData holds the configuration and webhooks for a loop
type ChannelData struct {
	Config    *sys.LoopConfig
	ChannelID snowflake.ID // Pre-parsed for performance
	Hooks     []WebhookData
}

// LoopState tracks the running state of a loop
type LoopState struct {
	StopChan        chan struct{}
	RoundsTotal     int
	CurrentRound    int
	NextRun         time.Time
	EndTime         time.Time // Estimated or fixed end time
	DurationTimeout *time.Timer
}

// State maps
var (
	configuredChannels sync.Map // map[snowflake.ID]*ChannelData
	activeLoops        sync.Map // map[snowflake.ID]*LoopState
)

// InitLoopManager initializes the loop manager daemon
func InitLoopManager(ctx context.Context, client *bot.Client) {
	go func() {
		// Wait longer for cache and stability (especially in large guilds)
		sys.LogLoopManager("⏳ Waiting for cache stabilization (20s)...")
		time.Sleep(20 * time.Second)

		configs, err := sys.GetAllLoopConfigs(ctx)
		if err != nil {
			sys.LogLoopManager(sys.MsgLoopFailedToLoadConfigs, err)
			return
		}

		for _, config := range configs {
			configuredChannels.Store(config.ChannelID, &ChannelData{
				Config:    config,
				ChannelID: config.ChannelID,
				Hooks:     nil,
			})
		}

		sys.LogLoopManager(sys.MsgLoopLoadedChannels, len(configs))

		// Pre-fetch all guild webhooks to a master map
		// Map: GuildID -> ChannelID -> []Webhooks
		masterWebhookMap := make(map[snowflake.ID]map[snowflake.ID][]discord.Webhook)

		uniqueGuildIDs := make(map[snowflake.ID]bool)
		for guild := range client.Caches.Guilds() {
			uniqueGuildIDs[guild.ID] = true
		}

		sys.LogLoopManager("🔍 Warming up webhook cache for %d guilds...", len(uniqueGuildIDs))

		for gID := range uniqueGuildIDs {
			select {
			case <-ctx.Done():
				return
			default:
			}

			sys.LogLoopManager("  └ Fetching webhooks for guild: %s", gID)

			// Sequential fetch to avoid hitting global limits
			webhookOpSem <- struct{}{}
			hooks, err := client.Rest.GetAllWebhooks(gID, rest.WithCtx(ctx))
			<-webhookOpSem

			if err != nil {
				sys.LogLoopManager("  ⚠️ Failed to fetch webhooks for guild %s: %v", gID, err)
				continue
			}

			gMap := make(map[snowflake.ID][]discord.Webhook)
			for _, wh := range hooks {
				var chID snowflake.ID
				switch w := wh.(type) {
				case discord.IncomingWebhook:
					chID = w.ChannelID
				case discord.ChannelFollowerWebhook:
					chID = w.ChannelID
				}
				if chID != 0 {
					gMap[chID] = append(gMap[chID], wh)
				}
			}
			masterWebhookMap[gID] = gMap

			// Small breath between guilds
			time.Sleep(2 * time.Second)
		}

		// Auto-resume running loops sequentially
		resumeCount := 0
		for _, config := range configs {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if config.IsRunning {
				dataVal, ok := configuredChannels.Load(config.ChannelID)
				if !ok {
					continue
				}
				channelData := dataVal.(*ChannelData)

				// Find GuildID from cache
				var guildID snowflake.ID
				if ch, ok := client.Caches.Channel(config.ChannelID); ok {
					guildID = ch.GuildID()
				} else {
					// Fallback: If not in cache, we can't safely resume without risking an API storm.
					// But we've waited 20s, so it SHOULD be there.
					sys.LogLoopManager("❌ Channel %s not in cache, skipping resume.", config.ChannelID)
					continue
				}

				// Load webhooks using our pre-fetched master map
				if err := loadWebhooksForChannel(ctx, client, channelData, masterWebhookMap[guildID]); err != nil {
					sys.LogLoopManager(sys.MsgLoopFailedToResume, config.ChannelName, err)
					continue
				}

				startLoopInternal(ctx, config.ChannelID, channelData, client)
				resumeCount++

				// Small delay between starting different categories
				time.Sleep(1 * time.Second)
			}
		}

		if resumeCount > 0 {
			sys.LogLoopManager(sys.MsgLoopResuming, resumeCount)
		}

		// Background cleanup (very late to avoid initialization phase)
		time.Sleep(5 * time.Minute)
		cleanupStaleWebhooks(ctx, client)
	}()
}

func cleanupStaleWebhooks(ctx context.Context, client *bot.Client) {
	configs, err := sys.GetAllLoopConfigs(ctx)
	if err != nil {
		return
	}

	validChannelIDSet := make(map[snowflake.ID]bool)
	validCategoryIDSet := make(map[snowflake.ID]bool)

	for _, cfg := range configs {
		if cfg.ChannelType == "category" {
			validCategoryIDSet[cfg.ChannelID] = true
		} else {
			validChannelIDSet[cfg.ChannelID] = true
		}
	}

	self, _ := client.Caches.SelfUser()

	// Iterate guilds sequentially
	for guild := range client.Caches.Guilds() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		webhooks, err := client.Rest.GetAllWebhooks(guild.ID, rest.WithCtx(ctx))
		if err != nil {
			continue
		}

		deletedCount := 0
		for _, wh := range webhooks {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if wh.Name() != LoopWebhookName {
				continue
			}

			var chID snowflake.ID
			var creatorID snowflake.ID

			switch w := wh.(type) {
			case discord.IncomingWebhook:
				chID = w.ChannelID
				creatorID = w.User.ID
			case discord.ChannelFollowerWebhook:
				chID = w.ChannelID
				creatorID = w.User.ID
			default:
				continue
			}

			if creatorID != self.ID {
				continue
			}

			isStale := true
			if validChannelIDSet[chID] {
				isStale = false
			} else {
				if ch, ok := client.Caches.Channel(chID); ok {
					if textCh, ok := ch.(discord.GuildMessageChannel); ok && textCh.ParentID() != nil {
						if validCategoryIDSet[*textCh.ParentID()] {
							isStale = false
						}
					}
				}
			}

			if isStale {
				if err := client.Rest.DeleteWebhook(wh.ID(), rest.WithCtx(ctx)); err == nil {
					deletedCount++
					// Slow down deletions to avoid hitting management rate limits
					time.Sleep(2 * time.Second)
				}
			}
		}

		if deletedCount > 0 {
			sys.LogLoopManager("🧹 [Automatic] Cleaned up %d stale LoopHook(s) in guild: %s", deletedCount, guild.Name)
		}
	}
}

// parseDuration parses a duration string to time.Duration
func parseDuration(duration string) (time.Duration, error) {
	if duration == "0" || duration == "" {
		return 0, nil
	}

	re := regexp.MustCompile(`^(\d+)(s|m|min|h|hr)?$`)
	match := re.FindStringSubmatch(strings.ToLower(duration))
	if match == nil {
		return 0, fmt.Errorf("invalid duration format: %s", duration)
	}

	value, _ := strconv.Atoi(match[1])
	unit := match[2]
	if unit == "" {
		unit = "s"
	}

	switch unit {
	case "s":
		return time.Duration(value) * time.Second, nil
	case "m", "min":
		return time.Duration(value) * time.Minute, nil
	case "h", "hr":
		return time.Duration(value) * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown interval unit: %s", unit)
	}
}

// FormatDuration formats a duration for display
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞ (Random)"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// IntervalMsToDuration converts milliseconds to duration
func IntervalMsToDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// ParseDurationString parses a duration string and returns a duration
func ParseDurationString(duration string) (time.Duration, error) {
	return parseDuration(duration)
}

// loadWebhooksForChannel loads webhooks for a channel or category with retry logic for cache readiness
func loadWebhooksForChannel(ctx context.Context, client *bot.Client, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	if data.Hooks != nil {
		return nil // Already loaded
	}

	// Retry logic: Bot might have just started and cache might not be ready
	var channel discord.GuildChannel
	var ok bool
	for i := 0; i < 5; i++ {
		channel, ok = client.Caches.Channel(data.ChannelID)
		if ok {
			break
		}
		if i < 4 {
			time.Sleep(1 * time.Second)
		}
	}

	if !ok {
		return fmt.Errorf("failed to fetch channel %s from cache after retries", data.ChannelID)
	}

	// If webhookMap is nil, we are loading this on-demand (e.g. from a command)
	// instead of during startup. In that case, fetch it now.
	if webhookMap == nil {
		sys.LogLoopManager("🔍 Fetching guild webhooks for %s", channel.Name())
		hooks, err := client.Rest.GetAllWebhooks(channel.GuildID(), rest.WithCtx(ctx))
		if err != nil {
			return err
		}
		webhookMap = make(map[snowflake.ID][]discord.Webhook)
		for _, wh := range hooks {
			var chID snowflake.ID
			switch w := wh.(type) {
			case discord.IncomingWebhook:
				chID = w.ChannelID
			case discord.ChannelFollowerWebhook:
				chID = w.ChannelID
			}
			if chID != 0 {
				webhookMap[chID] = append(webhookMap[chID], wh)
			}
		}
	}

	if data.Config.ChannelType == "category" {
		return prepareWebhooksForCategory(ctx, client, channel, data, webhookMap)
	}
	return prepareWebhooksForSingleChannel(ctx, client, channel, data, webhookMap)
}

// prepareWebhooksForSingleChannel prepares webhooks for a single channel
func prepareWebhooksForSingleChannel(ctx context.Context, client *bot.Client, channel discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	channelID := channel.ID()
	webhooks := webhookMap[channelID]

	self, _ := client.Caches.SelfUser()

	// Find existing webhook owned by bot
	var hook *discord.IncomingWebhook
	for _, wh := range webhooks {
		if incomingHook, ok := wh.(discord.IncomingWebhook); ok {
			if incomingHook.User.ID == self.ID && incomingHook.Name() == LoopWebhookName {
				hook = &incomingHook
				break
			}
		}
	}

	// Create if not exists
	if hook == nil {
		if len(webhooks) >= 10 {
			sys.LogLoopManager(sys.MsgLoopWebhookLimitReached, channel.Name())
			return nil
		}
		newHook, err := createWebhookWithRetry(ctx, client, channelID, discord.WebhookCreate{
			Name: LoopWebhookName,
		})
		if err != nil {
			return fmt.Errorf("failed to create webhook: %w", err)
		}
		hook = newHook
	}

	if hook != nil {
		data.Hooks = []WebhookData{{
			WebhookID:    hook.ID(),
			WebhookToken: hook.Token,
			ChannelName:  channel.Name(),
		}}
		sys.LogLoopManager(sys.MsgLoopPreparedWebhook, channel.Name())
	}
	return nil
}

// prepareWebhooksForCategory prepares webhooks for all text channels in a category
func prepareWebhooksForCategory(ctx context.Context, client *bot.Client, channel discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	categoryID := channel.ID()
	guildID := channel.GuildID()

	var hooks []WebhookData
	self, _ := client.Caches.SelfUser()

	// Get a filtered list of channels for this guild
	allChannels := client.Caches.Channels()
	var targetChannels []discord.GuildMessageChannel
	for ch := range allChannels {
		if ch.GuildID() == guildID {
			if textCh, ok := ch.(discord.GuildMessageChannel); ok {
				if textCh.ParentID() != nil && *textCh.ParentID() == categoryID {
					targetChannels = append(targetChannels, textCh)
				}
			}
		}
	}

	sys.LogLoopManager("🔨 Preparing %d channels in category: %s", len(targetChannels), channel.Name())

	// Process sequentially to safely manage rate limits
	for _, tc := range targetChannels {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Look up webhooks from our bulk-fetched map
		webhooks := webhookMap[tc.ID()]

		var hook *discord.IncomingWebhook
		for _, wh := range webhooks {
			if incomingHook, ok := wh.(discord.IncomingWebhook); ok {
				if incomingHook.User.ID == self.ID && incomingHook.Name() == LoopWebhookName {
					hook = &incomingHook
					break
				}
			}
		}

		if hook == nil {
			if len(webhooks) >= 10 {
				sys.LogLoopManager(sys.MsgLoopWebhookLimitReached, tc.Name())
				continue
			}
			// Use retry logic for webhook creation
			newHook, err := createWebhookWithRetry(ctx, client, tc.ID(), discord.WebhookCreate{
				Name: LoopWebhookName,
			})
			if err != nil {
				sys.LogLoopManager(sys.MsgLoopFailedToCreateWebhook, tc.Name(), err)
				continue
			}
			hook = newHook

			// Discord has a strict guild-wide creation limit (nominal 5/5s).
			// We wait 6 seconds to be absolutely certain we never trigger a burst penalty.
			time.Sleep(6 * time.Second)
		}

		if hook != nil {
			hooks = append(hooks, WebhookData{
				WebhookID:    hook.ID(),
				WebhookToken: hook.Token,
				ChannelName:  tc.Name(),
			})
			sys.LogLoopManager(sys.MsgLoopPreparedWebhook, tc.Name())
		}
	}

	data.Hooks = hooks
	sys.LogLoopManager(sys.MsgLoopPreparedCategoryHooks, len(hooks), channel.Name())
	return nil
}

// startLoopInternal starts a loop for a channel
func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client *bot.Client) {
	stopChan := make(chan struct{})
	state := &LoopState{
		StopChan:     stopChan,
		RoundsTotal:  0,
		CurrentRound: 0,
	}
	activeLoops.Store(channelID, state)
	sys.SetLoopState(ctx, channelID, true)

	isAlive := func() bool {
		_, ok := activeLoops.Load(channelID)
		return ok
	}

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			sys.SetLoopState(ctx, channelID, false)
		}()

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimedMode := interval > 0
		isRandomMode := interval == 0

		if isTimedMode {
			sys.LogLoopManager(sys.MsgLoopStartingTimed, FormatDuration(interval))
			state.EndTime = time.Now().UTC().Add(interval)
			state.DurationTimeout = time.AfterFunc(interval, func() {
				sys.LogLoopManager(sys.MsgLoopTimeLimitReached, data.Config.ChannelName)
				StopLoopInternal(ctx, channelID, client)
			})
		} else if isRandomMode {
			sys.LogLoopManager(sys.MsgLoopStartingRandom, data.Config.ChannelName)
		}

		for isAlive() {
			if isRandomMode {
				// Random mode: 1-1000 rounds, 1-1000 second delays
				randomRounds := secureIntn(1000) + 1
				randomDelay := time.Duration(secureIntn(1000)+1) * time.Second

				state.RoundsTotal = randomRounds
				state.CurrentRound = 0

				batches := (len(data.Hooks) + LoopBatchSize - 1) / LoopBatchSize
				if batches == 0 {
					batches = 1
				}

				// Initial generous estimation: 500ms per batch per round
				estDuration := time.Duration(randomRounds) * time.Duration(batches) * 500 * time.Millisecond
				state.EndTime = time.Now().UTC().Add(estDuration)

				sys.LogLoopManager(sys.MsgLoopRandomStatus,
					data.Config.ChannelName, randomRounds, FormatDuration(randomDelay))

				batchStart := time.Now()
				for i := 0; i < randomRounds && isAlive(); i++ {
					state.CurrentRound = i + 1
					executeRound(data, isAlive, client)

					// Dynamic EndTime update based on actual speed
					elapsed := time.Since(batchStart)
					avgPerRound := elapsed / time.Duration(i+1)
					remainingRounds := time.Duration(randomRounds - (i + 1))
					state.EndTime = time.Now().UTC().Add(avgPerRound * remainingRounds)
				}

				if !isAlive() {
					break
				}

				// Wait before next iteration
				state.EndTime = time.Time{} // Clear end time during wait
				state.NextRun = time.Now().UTC().Add(randomDelay)
				select {
				case <-time.After(randomDelay):
					state.NextRun = time.Time{} // Clear after delay
				case <-stopChan:
					return
				}
			} else if isTimedMode {
				state.CurrentRound++
				executeRound(data, isAlive, client)
			}
		}

		if state.DurationTimeout != nil {
			state.DurationTimeout.Stop()
		}
	}()
}

// executeRound sends messages to all webhooks
func executeRound(data *ChannelData, isAlive func() bool, client *bot.Client) {
	content := data.Config.Message
	if content == "" {
		content = "@everyone"
	}

	webhookAuthor := data.Config.WebhookAuthor
	webhookAvatar := data.Config.WebhookAvatar

	// If author or avatar is missing, try to use server (guild) details
	if webhookAuthor == "" || webhookAvatar == "" {
		if channel, ok := client.Caches.Channel(data.ChannelID); ok {
			if guild, ok := client.Caches.Guild(channel.GuildID()); ok {
				if webhookAuthor == "" {
					webhookAuthor = guild.Name
				}
				if webhookAvatar == "" && guild.Icon != nil {
					if url := guild.IconURL(); url != nil {
						webhookAvatar = *url
					}
				}
			}
		}
	}

	// Final fallbacks
	if webhookAuthor == "" {
		webhookAuthor = LoopWebhookName
	}
	if webhookAvatar == "" {
		if self, ok := client.Caches.SelfUser(); ok {
			webhookAvatar = self.EffectiveAvatarURL()
		}
	}

	for i := 0; i < len(data.Hooks); i += LoopBatchSize {
		if !isAlive() {
			break
		}

		end := i + LoopBatchSize
		if end > len(data.Hooks) {
			end = len(data.Hooks)
		}
		batch := data.Hooks[i:end]

		var wg sync.WaitGroup
		for _, hookData := range batch {
			if !isAlive() {
				break
			}

			wg.Add(1)
			go func(hd WebhookData) {
				defer wg.Done()

				// Add jitter
				time.Sleep(time.Duration(secureIntn(500)) * time.Millisecond)

				for attempt := 0; attempt < 3; attempt++ {
					if !isAlive() {
						return
					}

					_, err := client.Rest.CreateWebhookMessage(hd.WebhookID, hd.WebhookToken, discord.WebhookMessageCreate{
						Content:   content,
						Username:  webhookAuthor,
						AvatarURL: webhookAvatar,
					}, rest.CreateWebhookMessageParams{Wait: false})

					if err == nil {
						return // Success
					}

					// Handle Rate Limits with backoff
					time.Sleep(time.Duration(attempt+2) * time.Second)
					sys.LogLoopManager(sys.MsgLoopSendFail, hd.ChannelName, err)
				}
			}(hookData)
		}
		wg.Wait()

		// Pause between batches to respect global rate limits
		// For 200+ channels, we need to be very rhythmic
		time.Sleep(2 * time.Second)
	}
}

// StopLoopInternal stops a loop and performs cleanup
func StopLoopInternal(ctx context.Context, channelID snowflake.ID, client *bot.Client) bool {
	stateVal, loaded := activeLoops.LoadAndDelete(channelID)
	if !loaded {
		return false
	}
	state := stateVal.(*LoopState)

	close(state.StopChan)
	if state.DurationTimeout != nil {
		state.DurationTimeout.Stop()
	}

	activeLoops.Delete(channelID)
	sys.SetLoopState(ctx, channelID, false)

	dataVal, ok := configuredChannels.Load(channelID)
	if ok {
		data := dataVal.(*ChannelData)
		sys.LogLoopManager(sys.MsgLoopStopped, data.Config.ChannelName)
	}

	return true
}

// GetActiveLoops returns a copy of active loop states
func GetActiveLoops() map[snowflake.ID]*LoopState {
	result := make(map[snowflake.ID]*LoopState)
	activeLoops.Range(func(key, value interface{}) bool {
		result[key.(snowflake.ID)] = value.(*LoopState)
		return true
	})
	return result
}

// GetConfiguredChannels returns a copy of configured channels
func GetConfiguredChannels() map[snowflake.ID]*ChannelData {
	result := make(map[snowflake.ID]*ChannelData)
	configuredChannels.Range(func(key, value interface{}) bool {
		result[key.(snowflake.ID)] = value.(*ChannelData)
		return true
	})
	return result
}

// SetLoopConfig configures a channel for looping
func SetLoopConfig(ctx context.Context, client *bot.Client, channelID snowflake.ID, config *sys.LoopConfig) error {
	sys.LogLoopManager(sys.MsgLoopConfigured, config.ChannelName)
	if err := sys.AddLoopConfig(ctx, channelID, config); err != nil {
		return err
	}

	data := &ChannelData{
		Config:    config,
		ChannelID: channelID,
		Hooks:     nil,
	}
	configuredChannels.Store(channelID, data)
	return nil
}

// StartLoop starts a loop for a channel ID
func StartLoop(ctx context.Context, client *bot.Client, channelID snowflake.ID, interval time.Duration) error {
	dataVal, ok := configuredChannels.Load(channelID)
	if !ok {
		return fmt.Errorf("channel not configured")
	}
	data := dataVal.(*ChannelData)

	// Check if already running
	if _, running := activeLoops.Load(channelID); running {
		return fmt.Errorf("loop already running")
	}

	// Load webhooks if needed
	if err := loadWebhooksForChannel(ctx, client, data, nil); err != nil {
		return err
	}

	// Apply runtime interval
	data.Config.Interval = int(interval.Milliseconds())

	startLoopInternal(ctx, channelID, data, client)
	return nil
}

// DeleteLoopConfig removes a loop configuration
func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID, client *bot.Client) error {
	StopLoopInternal(ctx, channelID, client)
	configuredChannels.Delete(channelID)
	return sys.DeleteLoopConfig(ctx, channelID)
}

// secureIntn returns a cryptographically secure random number in [0, max)
func secureIntn(max int) int {
	if max <= 0 {
		return 0
	}
	nBig, err := crand.Int(crand.Reader, big.NewInt(int64(max)))
	if err != nil {
		// Fallback to pseudo-random if crypto fails (virtually impossible)
		return rand.Intn(max)
	}
	return int(nBig.Int64())
}

// createWebhookWithRetry creates a webhook with exponential backoff and jitter
func createWebhookWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID, create discord.WebhookCreate) (*discord.IncomingWebhook, error) {
	// Acquire global management semaphore
	webhookOpSem <- struct{}{}
	defer func() { <-webhookOpSem }()

	var err error
	for i := 0; i < 5; i++ {
		var hook *discord.IncomingWebhook
		hook, err = client.Rest.CreateWebhook(channelID, create, rest.WithCtx(ctx))
		if err == nil {
			return hook, nil
		}

		// Calculate wait time with exponential backoff and jitter
		jitter := time.Duration(secureIntn(1000)) * time.Millisecond
		wait := (time.Duration(1<<uint(i+1)) * time.Second) + jitter

		sys.LogLoopManager("⚠️ Retrying webhook creation for %s in %v (Attempt %d/5): %v",
			channelID, wait.Truncate(100*time.Millisecond), i+1, err)

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return nil, err
}
