package proc

import (
	"context"
	"fmt"
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
	LoopBatchSize   = 25
)

func init() {
	sys.OnClientReady(func(client *bot.Client) {
		sys.RegisterDaemon(sys.LogLoopRotator, func() { InitLoopRotator(client) })
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
	DurationTimeout *time.Timer
}

// State maps
var (
	configuredChannels sync.Map // map[snowflake.ID]*ChannelData
	activeLoops        sync.Map // map[snowflake.ID]*LoopState
)

// InitLoopRotator initializes the loop rotator daemon
func InitLoopRotator(client *bot.Client) {
	go func() {
		configs, err := sys.GetAllLoopConfigs(context.Background())
		if err != nil {
			sys.LogLoopRotator(sys.MsgLoopFailedToLoadConfigs, err)
			return
		}

		sys.LogLoopRotator(sys.MsgLoopLoadingChannels, len(configs))

		for _, config := range configs {
			configuredChannels.Store(config.ChannelID, &ChannelData{
				Config:    config,
				ChannelID: config.ChannelID,
				Hooks:     nil, // Lazy load
			})
		}

		sys.LogLoopRotator(sys.MsgLoopLoadedChannels, len(configs))

		// Auto-resume running loops
		resumeCount := 0
		for _, config := range configs {
			if config.IsRunning {
				go func(cfg *sys.LoopConfig) {
					dataVal, ok := configuredChannels.Load(cfg.ChannelID)
					if !ok {
						return
					}
					channelData := dataVal.(*ChannelData)

					// Load webhooks
					if err := loadWebhooksForChannel(client, channelData); err != nil {
						sys.LogLoopRotator(sys.MsgLoopFailedToResume, cfg.ChannelName, err)
						return
					}

					startLoopInternal(cfg.ChannelID, channelData, client)
				}(config)
				resumeCount++
			}
		}

		if resumeCount > 0 {
			sys.LogLoopRotator(sys.MsgLoopResuming, resumeCount)
		}
	}()
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
func loadWebhooksForChannel(client *bot.Client, data *ChannelData) error {
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

	if data.Config.ChannelType == "category" {
		return prepareWebhooksForCategory(client, channel, data)
	}
	return prepareWebhooksForSingleChannel(client, channel, data)
}

// prepareWebhooksForSingleChannel prepares webhooks for a single channel
func prepareWebhooksForSingleChannel(client *bot.Client, channel discord.GuildChannel, data *ChannelData) error {
	channelID := channel.ID()

	webhooks, err := getWebhooksWithRetry(client, channelID)
	if err != nil {
		return fmt.Errorf("failed to fetch webhooks after retries: %w", err)
	}

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
			sys.LogLoopRotator(sys.MsgLoopWebhookLimitReached, channel.Name())
			return nil
		}
		newHook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{
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
		sys.LogLoopRotator(sys.MsgLoopPreparedWebhook, channel.Name())
	}
	return nil
}

// prepareWebhooksForCategory prepares webhooks for all text channels in a category
func prepareWebhooksForCategory(client *bot.Client, channel discord.GuildChannel, data *ChannelData) error {
	categoryID := channel.ID()
	guildID := channel.GuildID()

	var hooks []WebhookData
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Limit to 10 concurrent API requests

	// Iterate through cached channels
	for ch := range client.Caches.Channels() {
		if ch.GuildID() != guildID {
			continue
		}

		if textCh, ok := ch.(discord.GuildMessageChannel); ok {
			if textCh.ParentID() != nil && *textCh.ParentID() == categoryID {
				wg.Add(1)
				go func(tc discord.GuildMessageChannel) {
					defer wg.Done()
					sem <- struct{}{}        // Acquire
					defer func() { <-sem }() // Release

					webhooks, err := getWebhooksWithRetry(client, tc.ID())
					if err != nil {
						sys.LogLoopRotator(sys.MsgLoopFailedToFetchWebhooks, tc.Name(), err)
						return
					}

					self, _ := client.Caches.SelfUser()
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
							sys.LogLoopRotator(sys.MsgLoopWebhookLimitReached, tc.Name())
							return
						}
						newHook, err := client.Rest.CreateWebhook(tc.ID(), discord.WebhookCreate{
							Name: LoopWebhookName,
						})
						if err != nil {
							sys.LogLoopRotator(sys.MsgLoopFailedToCreateWebhook, tc.Name(), err)
							return
						}
						hook = newHook
					}

					if hook != nil {
						mu.Lock()
						hooks = append(hooks, WebhookData{
							WebhookID:    hook.ID(),
							WebhookToken: hook.Token,
							ChannelName:  tc.Name(),
						})
						mu.Unlock()
						sys.LogLoopRotator(sys.MsgLoopPreparedWebhook, tc.Name())
					}
				}(textCh)
			}
		}
	}

	wg.Wait()
	data.Hooks = hooks
	sys.LogLoopRotator(sys.MsgLoopPreparedCategoryHooks, len(hooks), channel.Name())
	return nil
}

// startLoopInternal starts a loop for a channel
func startLoopInternal(channelID snowflake.ID, data *ChannelData, client *bot.Client) {
	stopChan := make(chan struct{})
	state := &LoopState{
		StopChan:     stopChan,
		RoundsTotal:  0,
		CurrentRound: 0,
	}
	activeLoops.Store(channelID, state)
	sys.SetLoopState(context.Background(), channelID, true)

	isAlive := func() bool {
		_, ok := activeLoops.Load(channelID)
		return ok
	}

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			sys.SetLoopState(context.Background(), channelID, false)
		}()

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimedMode := interval > 0
		isRandomMode := interval == 0

		if isTimedMode {
			sys.LogLoopRotator(sys.MsgLoopStartingTimed, FormatDuration(interval))
			state.DurationTimeout = time.AfterFunc(interval, func() {
				sys.LogLoopRotator(sys.MsgLoopTimeLimitReached, data.Config.ChannelName)
				StopLoopInternal(channelID, client)
			})
		} else if isRandomMode {
			sys.LogLoopRotator(sys.MsgLoopStartingRandom, data.Config.ChannelName)
		}

		for isAlive() {
			if isRandomMode {
				// Random mode: 1-100 rounds, 1-10 min delays
				randomRounds := rand.Intn(100) + 1
				randomDelay := time.Duration(rand.Intn(10)+1) * time.Minute

				state.RoundsTotal = randomRounds
				state.CurrentRound = 0

				sys.LogLoopRotator(sys.MsgLoopRandomStatus,
					data.Config.ChannelName, randomRounds, FormatDuration(randomDelay))

				for i := 0; i < randomRounds && isAlive(); i++ {
					state.CurrentRound = i + 1
					executeRound(data, isAlive, client)
				}

				if !isAlive() {
					break
				}

				// Wait before next iteration
				select {
				case <-time.After(randomDelay):
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
				time.Sleep(time.Duration(rand.Intn(250)) * time.Millisecond)

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

					// Handle Rate Limits
					time.Sleep(time.Duration(attempt+1) * time.Second)
					sys.LogLoopRotator(sys.MsgLoopSendFail, hd.ChannelName, err)
				}
			}(hookData)
		}
		wg.Wait()
	}
}

// StopLoopInternal stops a loop and performs cleanup
func StopLoopInternal(channelID snowflake.ID, client *bot.Client) bool {
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
	sys.SetLoopState(context.Background(), channelID, false)

	dataVal, ok := configuredChannels.Load(channelID)
	if ok {
		data := dataVal.(*ChannelData)
		sys.LogLoopRotator(sys.MsgLoopStopped, data.Config.ChannelName)
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
func SetLoopConfig(client *bot.Client, channelID snowflake.ID, config *sys.LoopConfig) error {
	sys.LogLoopRotator(sys.MsgLoopConfigured, config.ChannelName)
	if err := sys.AddLoopConfig(context.Background(), channelID, config); err != nil {
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
func StartLoop(client *bot.Client, channelID snowflake.ID, interval time.Duration) error {
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
	if err := loadWebhooksForChannel(client, data); err != nil {
		return err
	}

	// Apply runtime interval
	data.Config.Interval = int(interval.Milliseconds())

	startLoopInternal(channelID, data, client)
	return nil
}

// DeleteLoopConfig removes a loop configuration
func DeleteLoopConfig(channelID snowflake.ID, client *bot.Client) error {
	StopLoopInternal(channelID, client)
	configuredChannels.Delete(channelID)
	return sys.DeleteLoopConfig(context.Background(), channelID)
}

// getWebhooksWithRetry fetches webhooks with exponential backoff and jitter
func getWebhooksWithRetry(client *bot.Client, channelID snowflake.ID) ([]discord.Webhook, error) {
	var webhooks []discord.Webhook
	var err error

	for i := 0; i < 5; i++ {
		webhooks, err = client.Rest.GetWebhooks(channelID)
		if err == nil {
			return webhooks, nil
		}

		// Calculate wait time with exponential backoff and jitter
		// Wait: 2s, 4s, 8s, 16s... + random jitter
		jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
		wait := (time.Duration(1<<uint(i+1)) * time.Second) + jitter

		sys.LogLoopRotator("⚠️ Retrying webhook fetch for %s in %v (Attempt %d/5): %v",
			channelID, wait.Truncate(100*time.Millisecond), i+1, err)

		time.Sleep(wait)
	}

	return nil, err
}
