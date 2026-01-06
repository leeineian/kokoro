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
)

var (
	// Global semaphore to limit webhook creation/deletion operations
	webhookOpSem = make(chan struct{}, 1)
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

func secureIntn(max int) int {
	if max <= 0 {
		return 0
	}
	nBig, err := crand.Int(crand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return rand.Intn(max)
	}
	return int(nBig.Int64())
}

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

	for _, config := range configs {
		configuredChannels.Store(config.ChannelID, &ChannelData{
			Config: config,
			Hooks:  nil,
		})
	}

	sys.LogLoopManager(sys.MsgLoopLoadedChannels, len(configs))
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
	channel, ok := client.Caches.Channel(data.Config.ChannelID)
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
			sys.LogLoopManager("🔍 [WEBHOOK FETCH] Fetching all webhooks for guild %s", guildID)
			hooks, err := client.Rest.GetAllWebhooks(guildID, rest.WithCtx(ctx))
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
			sys.LogLoopManager("✅ [WEBHOOK FETCH] Cached %d webhooks for guild %s", len(hooks), guildID)
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

		hooks = append(hooks, WebhookData{
			WebhookID:    hook.ID(),
			WebhookToken: hook.Token,
			ChannelName:  tc.Name(),
		})
	}

	data.Hooks = hooks
	sys.LogLoopManager(sys.MsgLoopPreparedCategoryHooks, len(hooks), category.Name())
	return nil
}

func createWebhookWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID) (*discord.IncomingWebhook, error) {
	webhookOpSem <- struct{}{}
	defer func() { <-webhookOpSem }()

	sys.LogLoopManager("🔨 [WEBHOOK CREATE] Attempting for channel %s", channelID)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: LoopWebhookName}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	sys.LogLoopManager("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
	return hook, nil
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

		for {
			select {
			case <-stopChan:
				return
			default:
			}

			if isTimed {
				state.CurrentRound++
				executeRound(data, client, stopChan, content, author, avatar)
			} else {
				// Random Mode
				rounds := secureIntn(1000) + 1
				delay := time.Duration(secureIntn(1000)+1) * time.Second

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
					executeRound(data, client, stopChan, content, author, avatar)
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

func executeRound(data *ChannelData, client *bot.Client, stopChan chan struct{}, content, author, avatar string) {
	// Create a local copy of hooks to shuffle
	hooks := make([]WebhookData, len(data.Hooks))
	copy(hooks, data.Hooks)

	// Shuffle the hooks for true randomness
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(hooks), func(i, j int) {
		hooks[i], hooks[j] = hooks[j], hooks[i]
	})

	var wg sync.WaitGroup
	for _, h := range hooks {
		select {
		case <-stopChan:
			goto Wait
		default:
		}

		wg.Add(1)
		go func(hd WebhookData) {
			defer wg.Done()

			// Minor jitter to prevent simultaneous network spikes
			time.Sleep(time.Duration(secureIntn(50)) * time.Millisecond)

			for attempt := 0; attempt < 2; attempt++ {
				_, err := client.Rest.CreateWebhookMessage(hd.WebhookID, hd.WebhookToken, discord.WebhookMessageCreate{
					Content:   content,
					Username:  author,
					AvatarURL: avatar,
				}, rest.CreateWebhookMessageParams{Wait: false})
				if err == nil {
					return
				}
				if attempt == 0 {
					sys.LogLoopManager("⚠️ [MESSAGE SEND] Failed for %s (webhook %s): %v", hd.ChannelName, hd.WebhookID, err)
				}
				time.Sleep(2 * time.Second) // Simple rate limit backoff
			}
		}(h)

		// Delay between message starts to ensure a smooth flow
		// 20ms = 50 messages per second.
		time.Sleep(20 * time.Millisecond)
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
