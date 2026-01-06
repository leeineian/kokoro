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
)

var (
	// Global semaphore to limit webhook creation/deletion operations
	webhookOpSem = make(chan struct{}, 1)

	// Global semaphore to limit concurrent message sends (prevents connection exhaustion/503s)
	messageSendSem = make(chan struct{}, 200)
)

// Utilities

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞ (Random)"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func ParseDuration(duration string) (time.Duration, error) {
	if duration == "" || duration == "0" {
		return 0, nil
	}
	re := regexp.MustCompile(`^(\d+)(s|m|h)?$`)
	m := re.FindStringSubmatch(strings.ToLower(duration))
	if m == nil {
		return 0, fmt.Errorf("invalid format")
	}
	v, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "m":
		return time.Duration(v) * time.Minute, nil
	case "h":
		return time.Duration(v) * time.Hour, nil
	default:
		return time.Duration(v) * time.Second, nil
	}
}

func IntervalMsToDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

// LoopManager

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogLoopManager, func(ctx context.Context) { InitLoopManager(ctx, client) })
	})
}

type WebhookData struct {
	WebhookID    snowflake.ID
	WebhookToken string
	ChannelName  string
	ThreadIDs    []snowflake.ID
}

type ChannelData struct {
	Config *sys.LoopConfig
	Hooks  []WebhookData
}

type LoopState struct {
	StopChan        chan struct{}
	RoundsTotal     int
	CurrentRound    int
	NextRun         time.Time
	EndTime         time.Time
	DurationTimeout *time.Timer
}

type webhookCacheEntry struct {
	Webhooks map[snowflake.ID][]discord.Webhook
	Fetched  time.Time
}

var (
	configuredChannels sync.Map // map[snowflake.ID]*ChannelData
	activeLoops        sync.Map // map[snowflake.ID]*LoopState

	globalWebhookCache sync.Map // map[snowflake.ID]webhookCacheEntry
	globalWebhookMu    sync.Map // map[snowflake.ID]*sync.Mutex
)

const WebhookCacheTTL = 5 * time.Minute

func InitLoopManager(ctx context.Context, client *bot.Client) {
	configs, err := sys.GetAllLoopConfigs(ctx)
	if err != nil {
		sys.LogLoopManager(sys.MsgLoopFailedToLoadConfigs, err)
		return
	}

	var toResume []*ChannelData
	for _, config := range configs {
		data := &ChannelData{
			Config: config,
			Hooks:  nil,
		}
		configuredChannels.Store(config.ChannelID, data)
		if config.IsRunning {
			toResume = append(toResume, data)
		}
	}

	sys.LogLoopManager(sys.MsgLoopLoadedChannels, len(configs))

	if len(toResume) > 0 {
		sys.LogLoopManager("Resuming %d active loops...", len(toResume))
		for _, data := range toResume {
			// Check if we need to wait for cache before going async to keep logs ordered
			if _, found := client.Caches.Channel(data.Config.ChannelID); !found {
				sys.LogLoopManager("Waiting for category %s to appear in cache...", data.Config.ChannelID)
			}
			go func(d *ChannelData) {
				if err := loadWebhooksForChannelWithCache(ctx, client, d); err != nil {
					sys.LogLoopManager("❌ Failed to resume loop for %s: %v", d.Config.ChannelName, err)
					return
				}
				startLoopInternal(ctx, d.Config.ChannelID, d, client)
			}(data)
		}
	}
}

func StartLoop(ctx context.Context, client *bot.Client, channelID snowflake.ID, interval time.Duration) error {
	return BatchStartLoops(ctx, client, []snowflake.ID{channelID}, interval)
}
func BatchStartLoops(ctx context.Context, client *bot.Client, channelIDs []snowflake.ID, interval time.Duration) error {
	var toStart []*ChannelData

	// 1. Preparation Phase (Webhooks)
	for _, id := range channelIDs {
		dataVal, ok := configuredChannels.Load(id)
		if !ok {
			continue
		}
		data := dataVal.(*ChannelData)

		if _, running := activeLoops.Load(id); running {
			continue
		}

		// Prepare webhooks (detect existing or create new)
		if err := loadWebhooksForChannelWithCache(ctx, client, data); err != nil {
			sys.LogLoopManager("❌ Failed to prepare webhooks for %s: %v", id, err)
			continue
		}

		// Apply runtime interval
		data.Config.Interval = int(interval.Milliseconds())
		toStart = append(toStart, data)
	}

	if len(toStart) == 0 {
		return fmt.Errorf("no loops were able to start")
	}

	// 2. Execution Phase (Simultaneous Start)
	for _, data := range toStart {
		startLoopInternal(ctx, data.Config.ChannelID, data, client)
	}

	return nil
}

func loadWebhooksForChannelWithCache(ctx context.Context, client *bot.Client, data *ChannelData) error {
	var channel discord.GuildChannel
	var ok bool

	// Wait up to 30 seconds for the channel to appear in cache (it might be missing immediately after READY)
	for i := 0; i < 60; i++ {
		if ch, found := client.Caches.Channel(data.Config.ChannelID); found {
			channel = ch
			ok = true
			break
		}
		if i == 10 { // Only log if still missing after 5 seconds
			sys.LogLoopManager("Still waiting for category %s to appear in cache...", data.Config.ChannelID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if !ok {
		return fmt.Errorf("channel %s not in cache", data.Config.ChannelID)
	}

	guildID := channel.GuildID()

	// Atomic fetch-and-fill to avoid parallel API calls for the same guild
	var webhookMap map[snowflake.ID][]discord.Webhook
	if val, ok := globalWebhookCache.Load(guildID); ok {
		entry := val.(webhookCacheEntry)
		if time.Since(entry.Fetched) < WebhookCacheTTL {
			webhookMap = entry.Webhooks
		}
	}

	if webhookMap == nil {
		// Get or create a mutex for this guild
		muVal, _ := globalWebhookMu.LoadOrStore(guildID, &sync.Mutex{})
		mu := muVal.(*sync.Mutex)

		mu.Lock()
		// Double check after lock
		if val, ok := globalWebhookCache.Load(guildID); ok {
			entry := val.(webhookCacheEntry)
			if time.Since(entry.Fetched) < WebhookCacheTTL {
				webhookMap = entry.Webhooks
				mu.Unlock()
			}
		}

		if webhookMap == nil {
			defer mu.Unlock()
			// 350+ webhooks can be slow to fetch; use a dedicated longer timeout for this heavy operation.
			fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			hooks, err := client.Rest.GetAllWebhooks(guildID, rest.WithCtx(fetchCtx))
			cancel()

			if err != nil {
				return fmt.Errorf("failed to fetch webhooks: %w", err)
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
			globalWebhookCache.Store(guildID, webhookCacheEntry{
				Webhooks: webhookMap,
				Fetched:  time.Now(),
			})
			sys.LogLoopManager("Cached %d webhooks for guild %s", len(hooks), guildID)
		}
	}

	// Always prepare as category (loop system is category-only)
	return prepareWebhooksForCategory(ctx, client, channel, data, webhookMap)
}

func prepareWebhooksForCategory(ctx context.Context, client *bot.Client, category discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	var targetChannels []discord.GuildMessageChannel
	guildID := category.GuildID()
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if textCh, ok := ch.(discord.GuildMessageChannel); ok {
				if textCh.ParentID() != nil && *textCh.ParentID() == category.ID() {
					targetChannels = append(targetChannels, textCh)
				}
			}
		}
	}

	self, _ := client.Caches.SelfUser()
	var hooks []WebhookData

	// Find threads in cache for this guild
	var activeThreads []discord.GuildThread
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if thread, ok := ch.(discord.GuildThread); ok {
				activeThreads = append(activeThreads, thread)
			}
		}
	}

	for _, tc := range targetChannels {
		webhooks := webhookMap[tc.ID()]
		var hook *discord.IncomingWebhook
		for _, wh := range webhooks {
			if incoming, ok := wh.(discord.IncomingWebhook); ok {
				if incoming.User.ID == self.ID && incoming.Name() == LoopWebhookName {
					hook = &incoming
					break
				}
			}
		}

		if hook == nil {
			newHook, err := createWebhookWithRetry(ctx, client, tc.ID())
			if err != nil {
				sys.LogLoopManager(sys.MsgLoopFailedToCreateWebhook, tc.Name(), err)
				continue
			}
			hook = newHook
		}

		var threadIDs []snowflake.ID
		if data.Config.UseThread && data.Config.ThreadCount > 0 {
			// Find existing bot threads
			expectedName := "🧵" + tc.Name()
			for _, thread := range activeThreads {
				if thread.ParentID() != nil && *thread.ParentID() == tc.ID() &&
					thread.Name() == expectedName && thread.OwnerID == self.ID {
					threadIDs = append(threadIDs, thread.ID())
					if len(threadIDs) >= data.Config.ThreadCount {
						break
					}
				}
			}

			// Create missing threads
			for len(threadIDs) < data.Config.ThreadCount {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				newThread, err := createThreadWithRetry(ctx, client, tc.ID(), expectedName)
				if err != nil {
					sys.LogLoopManager("❌ Failed to create thread for %s: %v", tc.Name(), err)
					break
				}
				threadIDs = append(threadIDs, newThread.ID())
			}
		}

		hooks = append(hooks, WebhookData{
			WebhookID:    hook.ID(),
			WebhookToken: hook.Token,
			ChannelName:  tc.Name(),
			ThreadIDs:    threadIDs,
		})
	}

	data.Hooks = hooks
	sys.LogLoopManager("[%s] Preparing %d webhooks (with %d threads total)...", category.Name(), len(hooks), data.Config.ThreadCount*len(hooks))
	return nil
}

func createWebhookWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID) (*discord.IncomingWebhook, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	sys.LogLoopManager("🔨 [WEBHOOK CREATE] Attempting for channel %s", channelID)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: LoopWebhookName}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	sys.LogLoopManager("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
	return hook, nil
}

func createThreadWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID, name string) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	sys.LogLoopManager("🔨 [THREAD CREATE] Attempting for channel %s", channelID)
	thread, err := client.Rest.CreateThread(channelID, discord.GuildPublicThreadCreate{
		Name: name,
	}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [THREAD CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	sys.LogLoopManager("✅ [THREAD CREATE] Success for channel %s", channelID)
	return thread, nil
}

func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client *bot.Client) {
	stopChan := make(chan struct{})
	state := &LoopState{StopChan: stopChan}
	activeLoops.Store(channelID, state)
	sys.SetLoopState(ctx, channelID, true)

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			sys.SetLoopState(ctx, channelID, false)
		}()

		// Local RNG for this loop instance (avoids global lock convention)
		seed := time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		// Pre-allocated buffer for hooks to reduce GC pressure
		hookBuf := make([]WebhookData, len(data.Hooks))

		// Jitter: random 0 to 5-second delay before starting
		jitter := time.Duration(rng.Intn(5000)) * time.Millisecond
		sys.LogLoopManager("[%s] Applying %s startup jitter...", data.Config.ChannelName, jitter)
		select {
		case <-time.After(jitter):
		case <-stopChan:
			return
		}

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimed := interval > 0

		if isTimed {
			sys.LogLoopManager(sys.MsgLoopStartingTimed, FormatDuration(interval))
			state.EndTime = time.Now().UTC().Add(interval)
			state.DurationTimeout = time.AfterFunc(interval, func() {
				StopLoopInternal(ctx, channelID, client)
			})
		}

		content := data.Config.Message
		if content == "" {
			content = "@everyone"
		}
		author := data.Config.WebhookAuthor
		if author == "" {
			author = LoopWebhookName
		}
		avatar := data.Config.WebhookAvatar
		if avatar == "" {
			if ch, ok := client.Caches.Channel(channelID); ok {
				if guild, ok := client.Caches.Guild(ch.GuildID()); ok {
					if iconURL := guild.IconURL(); iconURL != nil {
						avatar = *iconURL
					}
				}
			}
		}

		threadContent := data.Config.ThreadMessage

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			if isTimed {
				state.CurrentRound++
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			} else {
				// Random Mode
				rounds := rng.Intn(1000) + 1
				delay := time.Duration(rng.Intn(1000)+1) * time.Second

				state.RoundsTotal = rounds
				state.CurrentRound = 0

				sys.LogLoopManager(sys.MsgLoopRandomStatus, data.Config.ChannelName, rounds, FormatDuration(delay))

				for i := 0; i < rounds; i++ {
					select {
					case <-stopChan:
						return
					default:
					}
					state.CurrentRound = i + 1
					executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
				}

				state.EndTime = time.Time{}
				state.NextRun = time.Now().UTC().Add(delay)
				select {
				case <-time.After(delay):
					state.NextRun = time.Time{}
				case <-stopChan:
					return
				}
			}
		}
	}()
}

func executeRound(ctx context.Context, data *ChannelData, client *bot.Client, stopChan chan struct{}, content, threadContent, author, avatar string, rng *rand.Rand, hookBuf []WebhookData) {
	// Copy hooks to reusable buffer
	if len(hookBuf) != len(data.Hooks) {
		// Should not happen given logic, but safety check
		hookBuf = make([]WebhookData, len(data.Hooks))
	}
	copy(hookBuf, data.Hooks)

	// Shuffle the hooks for true randomness
	rng.Shuffle(len(hookBuf), func(i, j int) {
		hookBuf[i], hookBuf[j] = hookBuf[j], hookBuf[i]
	})

	// Randomize rate for this round: 1-50 messages per second
	rate := rng.Intn(50) + 1
	delay := time.Second / time.Duration(rate)

	var wg sync.WaitGroup
	for _, h := range hookBuf {
		select {
		case <-stopChan:
			goto Wait
		default:
		}

		// Calculate jitter for this worker using the single-threaded RNG
		workerJitter := time.Duration(rng.Intn(50)) * time.Millisecond

		wg.Add(1)
		go func(hd WebhookData, startJitter time.Duration) {
			defer wg.Done()

			// Minor jitter to prevent simultaneous network spikes
			time.Sleep(startJitter)

			// Helper for sending with retries
			sendWithRetry := func(threadID snowflake.ID, msgContent string) {
				// Acquire semaphore to limit concurrent connections
				messageSendSem <- struct{}{}
				defer func() { <-messageSendSem }()

				backoffs := []time.Duration{2 * time.Second, 4 * time.Second}
				for attempt := 0; attempt <= len(backoffs); attempt++ {
					params := rest.CreateWebhookMessageParams{Wait: false}
					if threadID != 0 {
						params.ThreadID = threadID
					}

					_, err := client.Rest.CreateWebhookMessage(hd.WebhookID, hd.WebhookToken, discord.WebhookMessageCreate{
						Content:   msgContent,
						Username:  author,
						AvatarURL: avatar,
						AllowedMentions: &discord.AllowedMentions{
							Parse: []discord.AllowedMentionType{
								discord.AllowedMentionTypeEveryone,
								discord.AllowedMentionTypeRoles,
								discord.AllowedMentionTypeUsers,
							},
						},
					}, params, rest.WithCtx(ctx))
					if err == nil {
						return
					}

					if attempt < len(backoffs) {
						select {
						case <-time.After(backoffs[attempt]):
						case <-stopChan:
							return
						case <-ctx.Done():
							return
						}
					}
				}
			}

			// Send to main channel
			if content != "" {
				sendWithRetry(0, content)
			}

			// Send to threads
			if threadContent != "" && len(hd.ThreadIDs) > 0 {
				for _, tid := range hd.ThreadIDs {
					select {
					case <-stopChan:
						return
					default:
						sendWithRetry(tid, threadContent)
					}
				}
			}
		}(h, workerJitter)

		// Variable delay based on randomized rate
		time.Sleep(delay)
	}

Wait:
	wg.Wait()
}

func StopLoopInternal(ctx context.Context, channelID snowflake.ID, client *bot.Client) bool {
	if val, ok := activeLoops.LoadAndDelete(channelID); ok {
		state := val.(*LoopState)
		close(state.StopChan)
		if state.DurationTimeout != nil {
			state.DurationTimeout.Stop()
		}
		if dataVal, ok := configuredChannels.Load(channelID); ok {
			sys.LogLoopManager(sys.MsgLoopStopped, dataVal.(*ChannelData).Config.ChannelName)
		}
		return true
	}
	return false
}

func SetLoopConfig(ctx context.Context, client *bot.Client, channelID snowflake.ID, config *sys.LoopConfig) error {
	if err := sys.AddLoopConfig(ctx, channelID, config); err != nil {
		return err
	}
	configuredChannels.Store(channelID, &ChannelData{Config: config})
	sys.LogLoopManager(sys.MsgLoopConfigured, config.ChannelName)
	return nil
}

func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID, client *bot.Client) error {
	StopLoopInternal(ctx, channelID, client)
	configuredChannels.Delete(channelID)
	return sys.DeleteLoopConfig(ctx, channelID)
}

func GetActiveLoops() map[snowflake.ID]*LoopState {
	res := make(map[snowflake.ID]*LoopState)
	activeLoops.Range(func(k, v interface{}) bool {
		res[k.(snowflake.ID)] = v.(*LoopState)
		return true
	})
	return res
}

func GetConfiguredChannels() map[snowflake.ID]*ChannelData {
	res := make(map[snowflake.ID]*ChannelData)
	configuredChannels.Range(func(k, v interface{}) bool {
		res[k.(snowflake.ID)] = v.(*ChannelData)
		return true
	})
	return res
}
