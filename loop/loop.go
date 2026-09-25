package loop

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"golang.org/x/time/rate"
)

const (
	MsgLoopFailedToLoadConfigs   = "Failed to load configs: %v"
	MsgLoopLoadedChannels        = "Loaded configuration for %d categories."
	MsgLoopFailedToResume        = "Failed to resume %s: %v"
	MsgLoopResuming              = "Resuming %d active loops..."
	MsgLoopWebhookLimitReached   = "Channel %s has 10 webhooks, skipping"
	MsgLoopPreparedWebhook       = "Prepared webhook for channel: %s"
	MsgLoopFailedToFetchWebhooks = "Failed to fetch webhooks for %s: %v"
	MsgLoopFailedToCreateWebhook = "Failed to create webhook for %s: %v"
	MsgLoopPreparedCategoryHooks = "Prepared %d webhooks for category: %s"
	MsgLoopStartingTimed         = "Starting timed loop for %s"
	MsgLoopTimeLimitReached      = "Time limit reached for %s"
	MsgLoopStartingRandom        = "Starting infinite random mode for %s"
	MsgLoopRandomStatus          = "[%s] Random: %d rounds (%d pings), next delay: %s"
	MsgLoopRateLimited           = "[%s] Rate limited. Retrying in %v (Attempt %d/3)"
	MsgLoopSendFail              = "Failed to send to %s: %v"
	MsgLoopRenameFail            = "Failed to rename channel: %v"
	MsgLoopStopped               = "Stopped loop for: %s"
	MsgLoopConfigured            = "Configured channel: %s"
	MsgLoopEraseNoConfigs        = "No configurations were found to erase."
	MsgLoopErasedBatch           = "Erased **%d** configuration(s)."
	MsgLoopErrInvalidSelection   = "Invalid selection."
	MsgLoopErrConfigNotFound     = "Configuration not found."
	MsgLoopDeleteFail            = "Failed to delete configuration for **%s**: %v"
	MsgLoopDeleted               = "Deleted configuration for **%s**."
	MsgLoopErrInvalidChannel     = "Invalid channel selection."
	MsgLoopErrChannelFetchFail   = "Failed to fetch channel."
	MsgLoopErrOnlyCategories     = "Only **categories** are supported. Please select a category channel."
	MsgLoopSaveFail              = "Failed to save configuration: %v"
	MsgLoopConfiguredDisp        = "**Category Configured**\n> **%s**\n> Duration: ∞\n> Run `/loop start` to begin."
	MsgLoopErrInvalidDuration    = "Invalid duration: %v"
	MsgLoopErrNoChannels         = "No channels configured!"
	MsgLoopErrNoneStarted        = "No loops were started."
	MsgLoopStartedBatch          = "Started **%d** loop(s) for: **%s**"
	MsgLoopStarted               = "Started loop for: **%s**"
	MsgLoopStartFail             = "Failed to start **%s**: %v"
	MsgLoopNoRunning             = "No loops are currently running."
	MsgLoopStoppedBatch          = "Stopped **%d** loop(s)."
	MsgLoopStoppedDisp           = "Stopped the selected loop."
	MsgLoopErrStopFail           = "Could not find or stop the loop."
	MsgLoopErrGuildOnly          = "This command can only be used in a server."
	MsgLoopErrRetrieveFail       = "Failed to retrieve loop configurations."
	MsgLoopErrNoGuildConfigs     = "No loops are currently configured for this server."
	MsgLoopStatsHeader           = "**Current Loop Configurations**\n\n"
	MsgLoopStatsInterval         = "> • Interval: `%s`\n"
	MsgLoopStatsStatus           = "> • Status: %s\n"
	MsgLoopStatsThreads          = "> • Threads: `Enabled` (%d per channel)\n"
	MsgLoopStatsMessage          = "> • Message: `%s`\n"
	MsgLoopStatsAuthor           = "> • Author: `%s`\n"
	MsgLoopStatsAvatar           = "> • Avatar: [Link](<%s>)\n"
	MsgLoopStatsThreadMsg        = "> • Thread Message: `%s`\n"
	MsgLoopStatsVoteChan         = "> • Vote Channel: <#%s>\n"
	MsgLoopStatsVoteRole         = "> • Vote Role: <@&%s>\n"
	MsgLoopStatsVoteReaction     = "> • Vote Reaction: %s\n"
	MsgLoopStatsVoteThreshold    = "> • Vote Threshold: `%d%%`\n"
	MsgLoopStatsVoteMsg          = "> • Vote Message: `%s`\n"
	MsgLoopStatsQueue            = "> • Queue: `%s`\n"
	MsgLoopStatusStopped         = "🔴"
	MsgLoopStatusRunning         = "🟢"
	MsgLoopStatusRound           = " (Round %d)"
	MsgLoopStatusRoundBatch      = " (Round %d/%d)"
	MsgLoopStatusNextRun         = " (Next: %s)"
	MsgLoopStatusEnds            = " (Ends: %s)"
	MsgLoopStatusFinishing       = " (Finishing...)"
	MsgLoopChoiceStartAll        = "Start All Configured Loops"
	MsgLoopChoiceStart           = "Start Loop: %s %s%s (Duration: %s)"
	MsgLoopChoiceCategory        = "%s"
	MsgLoopChoiceEraseAll        = "Erase All Configured Loops"
	MsgLoopChoiceErase           = "Erase Loop: %s %s%s (Duration: %s)"
	MsgLoopChoiceStopAll         = "Stop All Running Loops"
	MsgLoopChoiceStop            = "Stop Loop: %s %s%s (Duration: %s)"
	MsgLoopSearchStartAll        = "start all configured loops"
	MsgLoopSearchEraseAll        = "erase all configured loops"
	MsgLoopSearchStopAll         = "stop all running loops"
	LoopWebhookName              = "LoopHook"
	WebhookCacheTTL              = 5 * time.Minute
)

var (
	// Configuration & State Maps
	configuredChannels sync.Map // map[snowflake.ID]*ChannelData
	activeLoops        sync.Map // map[snowflake.ID]*LoopState

	// Caching
	globalWebhookCache sync.Map // map[snowflake.ID]webhookCacheEntry
	globalWebhookMu    sync.Map // map[snowflake.ID]*sync.Mutex

	// System Flags
	isEmergencyStop int32

	// Semaphores
	webhookOpSem   = make(chan struct{}, 1)
	messageSendSem = make(chan struct{}, 2000)

	// Serial Loop Queue
	loopQueue         []snowflake.ID
	shuffledQueue     []snowflake.ID
	loopQueueMu       sync.Mutex
	serialActive      int32
	isCleaningThreads int32
)

type LoopConfig struct {
	ChannelID     snowflake.ID
	ChannelName   string
	ChannelType   string
	Rounds        int
	Interval      int
	Message       string
	WebhookAuthor string
	WebhookAvatar string
	UseThread     bool
	ThreadMessage string
	ThreadCount   int
	Threads       string
	IsRunning     bool
	VoteChannelID string
	VoteRole      string
	VoteMessage   string
	VoteThreshold int
	IsSerial      bool
}

func safeGo(f func()) {
	go func() {
		defer func() { recover() }()
		f()
	}()
}

type WebhookIdentity struct {
	ID    snowflake.ID
	Token string
}
type WebhookData struct {
	Webhooks     []WebhookIdentity
	ChannelName  string
	ThreadIDs    []snowflake.ID
	IsStructured bool
}
type ChannelData struct {
	Config *LoopConfig
	Hooks  []WebhookData
}
type LoopState struct {
	StopChan      chan struct{}
	ResumeChan    chan struct{}
	IsPaused      bool
	VoteMessageID snowflake.ID
	Votes         map[snowflake.ID]struct{}
	RoundsTotal   int
	CurrentRound  int
	NextRun       time.Time
	NeededVotes   int
}
type webhookCacheEntry struct {
	Webhooks map[snowflake.ID][]discord.Webhook
	Fetched  time.Time
}

type Hooks struct {
	LogInfo                     func(format string, v ...any)
	LogWarn                     func(format string, v ...any)
	LogError                    func(format string, v ...any)
	LogDebug                    func(format string, v ...any)
	RegisterCommand             func(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate))
	RegisterAutocompleteHandler func(commandName string, handler func(event *events.AutocompleteInteractionCreate))
	RegisterComponentHandler    func(customID string, handler func(event *events.ComponentInteractionCreate))
	RegisterDaemon              func(name string, logFunc func(format string, v ...any), start func(ctx context.Context) (bool, func(), func()))
	RespondInteractionV2        func(client bot.Client, interaction discord.Interaction, content string, ephemeral bool) error
	AppContext                  context.Context
	SetLoopConfigDB             func(ctx context.Context, channelID snowflake.ID, config *LoopConfig) error
	DeleteLoopConfigDB          func(ctx context.Context, channelID snowflake.ID) error
	GetLoopConfigDB             func(ctx context.Context, channelID snowflake.ID) (*LoopConfig, error)
	GetAllLoopConfigsDB         func(ctx context.Context) (map[snowflake.ID]*LoopConfig, error)
	AdminPerm                   int64
	GetAppConfig                func(ctx context.Context, key string) (string, error)
	SetLoopState                func(ctx context.Context, channelID snowflake.ID, state bool) error
	FormatDuration              func(d time.Duration) string
	DefaultTimeFormat           string
	ResetAllLoopStates          func(ctx context.Context) error
	EditInteractionV2           func(client bot.Client, interaction discord.Interaction, content string) error
	SendComponentsV2            func(client bot.Client, channelID snowflake.ID, components []any, messageReference *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error)
	NewV2Container              func(components ...any) any
	NewTextDisplay              func(text string) any
	IntervalMsToDuration        func(ms int) time.Duration
	OnClientReady               func(handler func(ctx context.Context, client bot.Client))
	OnRateLimitExceeded         func(handler func())
}

var loopSys Hooks

func Init(h Hooks) {
	loopSys = h

	adminPerm := discord.Permissions(loopSys.AdminPerm)

	loopSys.OnClientReady(func(ctx context.Context, client bot.Client) {
		loopSys.RegisterDaemon("LOOP", loopSys.LogInfo, func(ctx context.Context) (bool, func(), func()) { return InitLoopManager(ctx, client) })
	})

	loopSys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "loop",
		Description:              "Webhook stress testing and looping utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "erase",
				Description: "Erase a configured loop category",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target configuration to erase",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Configure a category for looping",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "category",
						Description:  "Category to configure",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "Message to send (default: @everyone)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "webhook_author",
						Description: "Webhook display name (default: LoopHook)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "webhook_avatar",
						Description: "Webhook avatar URL",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "thread_message",
						Description: "Message for threads (default: disabled)",
						Required:    false,
					},
					discord.ApplicationCommandOptionInt{
						Name:        "thread_count",
						Description: "Amount of threads per channel (default: disabled)",
						Required:    false,
					},
					discord.ApplicationCommandOptionChannel{
						Name:        "vote_channel",
						Description: "Channel where the vote panel will be posted",
						Required:    false,
					},
					discord.ApplicationCommandOptionRole{
						Name:        "vote_role",
						Description: "Role required to vote (and for % calculation)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "vote_message",
						Description: "Custom message to display on the vote panel",
						Required:    false,
					},
					discord.ApplicationCommandOptionInt{
						Name:        "vote_threshold",
						Description: "Percentage of role members required to resume (1-100)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "queue",
						Description: "Execution mode for this loop (default: Parallel)",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Serial", Value: "serial"},
							{Name: "Parallel", Value: "parallel"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "start",
				Description: "Start webhook loop(s)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target to start (all or specific channel)",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionInt{
						Name:        "rounds",
						Description: "Total rounds to run. Leave empty for random mode.",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stop",
				Description: "Stop webhook loop(s)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Target to stop (all or specific channel)",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "close",
				Description: "Close (archive) or delete all bot threads in a target",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Channel or Category to clean up",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "delete",
						Description: "Permanently delete threads instead of archiving (Default: False)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View all current loop configurations and their status",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "clean",
				Description: "Remove unauthorized participants from all bot threads in a target",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "target",
						Description:  "Channel or Category to clean",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
		},
	}, handleLoop)

	loopSys.RegisterAutocompleteHandler("loop", handleLoopAutocomplete)
}

// --- UTILS ---

func getRoleMemberCount(client bot.Client, guildID, roleID snowflake.ID) int {
	members, err := client.Rest.GetMembers(guildID, 1000, 0)
	if err != nil {
		return 1
	}
	count := 0
	for _, m := range members {
		if m.User.Bot {
			continue
		}
		for _, rid := range m.RoleIDs {
			if rid == roleID {
				count++
				break
			}
		}
	}
	return int(math.Max(1, float64(count)))
}

func formatVoteLabel(current, total int) string { return fmt.Sprintf("%d/%d Votes", current, total) }

func loadWebhooksForChannelWithCache(ctx context.Context, client bot.Client, data *ChannelData) error {
	var channel discord.GuildChannel
	var ok bool

	for i := 0; i < 60; i++ {
		if ch, found := client.Caches.Channel(data.Config.ChannelID); found {
			channel = ch
			ok = true
			break
		}
		if i == 10 {
			loopSys.LogInfo("Still waiting for category %s to appear in cache...", data.Config.ChannelID)
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

	var webhookMap map[snowflake.ID][]discord.Webhook
	if val, ok := globalWebhookCache.Load(guildID); ok {
		entry := val.(webhookCacheEntry)
		if time.Since(entry.Fetched) < WebhookCacheTTL {
			webhookMap = entry.Webhooks
		}
	}

	if webhookMap == nil {
		muVal, _ := globalWebhookMu.LoadOrStore(guildID, &sync.Mutex{})
		mu := muVal.(*sync.Mutex)

		mu.Lock()
		if val, ok := globalWebhookCache.Load(guildID); ok {
			entry := val.(webhookCacheEntry)
			if time.Since(entry.Fetched) < WebhookCacheTTL {
				webhookMap = entry.Webhooks
				mu.Unlock()
			}
		}

		if webhookMap == nil {
			defer mu.Unlock()
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
			loopSys.LogInfo("Cached %d webhooks for guild %s", len(hooks), guildID)
		}
	}

	if data.Config.ChannelType == "" {
		data.Config.ChannelType = "category"
	}

	if data.Config.ChannelType == "forum" ||
		data.Config.ChannelType == "media" {
		return prepareWebhooksForChannel(ctx, client, channel, data, webhookMap)
	}

	return prepareWebhooksForCategory(ctx, client, channel, data, webhookMap)
}

func prepareWebhooksForChannel(ctx context.Context, client bot.Client, channel discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	guildID := channel.GuildID()
	var activeThreads []discord.GuildThread
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if thread, ok := ch.(discord.GuildThread); ok {
				activeThreads = append(activeThreads, thread)
			}
		}
	}

	workload, err := prepareWorkload(ctx, client, channel, data.Config, webhookMap[channel.ID()], activeThreads)
	if err != nil {
		loopSys.LogInfo("❌ Failed to prepare %s: %v", channel.Name(), err)
		return err
	}

	data.Hooks = []WebhookData{workload}
	loopSys.LogInfo("[%s] Prepared %d webhooks for channel with %d threads...", channel.Name(), len(workload.Webhooks), len(workload.ThreadIDs))
	return nil
}

func prepareWebhooksForCategory(ctx context.Context, client bot.Client, category discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	var targetChannels []discord.GuildChannel
	guildID := category.GuildID()
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID && ch.ParentID() != nil && *ch.ParentID() == category.ID() {
			switch ch.Type() {
			case discord.ChannelTypeGuildText, discord.ChannelTypeGuildNews, discord.ChannelTypeGuildForum, discord.ChannelTypeGuildMedia:
				targetChannels = append(targetChannels, ch)
			}
		}
	}

	var activeThreads []discord.GuildThread
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if thread, ok := ch.(discord.GuildThread); ok {
				activeThreads = append(activeThreads, thread)
			}
		}
	}

	var hooks []WebhookData
	for _, tc := range targetChannels {
		workload, err := prepareWorkload(ctx, client, tc, data.Config, webhookMap[tc.ID()], activeThreads)
		if err != nil {
			loopSys.LogInfo("❌ Failed to prepare %s (skipping): %v", tc.Name(), err)
			continue
		}
		hooks = append(hooks, workload)
	}

	data.Hooks = hooks
	totalWebhooks := 0
	for _, h := range hooks {
		totalWebhooks += len(h.Webhooks)
	}
	loopSys.LogInfo("[%s] Preparing %d webhooks across %d channels (+ %d threads)...", category.Name(), totalWebhooks, len(hooks), data.Config.ThreadCount*len(hooks))
	return nil
}

func resolveWebhookIdentity(client bot.Client, config *LoopConfig) (string, string) {
	author := config.WebhookAuthor
	if author == "" {
		author = LoopWebhookName
	}
	avatar := config.WebhookAvatar
	if avatar == "" {
		if self, ok := client.Caches.SelfUser(); ok {
			avatar = self.EffectiveAvatarURL()
		}
	}
	return author, avatar
}

func isStructured(t discord.ChannelType) bool {
	return t == discord.ChannelTypeGuildForum || t == discord.ChannelTypeGuildMedia
}

func removeFromSlice(slice []snowflake.ID, id snowflake.ID) []snowflake.ID {
	for i, v := range slice {
		if v == id {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

func resolveScope(client bot.Client, guildID, targetID snowflake.ID) (map[snowflake.ID]bool, string) {
	scopeIDs := make(map[snowflake.ID]bool)
	scopeIDs[targetID] = true
	targetName := "Unknown"

	ch, ok := client.Caches.Channel(targetID)
	if !ok {
		return scopeIDs, targetName
	}

	targetName = ch.Name()
	if ch.Type() != discord.ChannelTypeGuildCategory {
		return scopeIDs, targetName
	}

	var channels []struct {
		ID       snowflake.ID  `json:"id"`
		ParentID *snowflake.ID `json:"parent_id"`
		Name     string        `json:"name"`
	}
	channelsEndpoint := rest.NewEndpoint(http.MethodGet, "/guilds/"+guildID.String()+"/channels")
	if err := client.Rest.Do(channelsEndpoint.Compile(nil), nil, &channels); err == nil {
		for _, c := range channels {
			if c.ParentID != nil && *c.ParentID == targetID {
				scopeIDs[c.ID] = true
			}
		}
	}
	return scopeIDs, targetName
}

func prepareWorkload(ctx context.Context, client bot.Client, tc discord.GuildChannel, config *LoopConfig, webhooks []discord.Webhook, activeThreads []discord.GuildThread) (WebhookData, error) {
	self, _ := client.Caches.SelfUser()
	targetID := tc.ID()

	// 0. Permission Check
	if err := checkBotLoopPermissions(client, tc, config.UseThread && config.ThreadCount > 0); err != nil {
		return WebhookData{}, err
	}

	// 1. Webhooks
	var pool []WebhookIdentity
	for _, wh := range webhooks {
		if incoming, ok := wh.(discord.IncomingWebhook); ok {
			if incoming.User.ID == self.ID && strings.HasPrefix(incoming.Name(), LoopWebhookName) {
				pool = append(pool, WebhookIdentity{ID: incoming.ID(), Token: incoming.Token})
			}
		}
	}

	maxHooks := 1
	if config.ThreadCount >= 50 {
		maxHooks = 5
	}
	if config.ThreadCount >= 500 {
		maxHooks = 10
	}

	for len(pool) < maxHooks {
		newHook, err := createWebhookWithRetry(ctx, client, targetID, strconv.Itoa(len(pool)))
		if err != nil {
			break
		}
		pool = append(pool, WebhookIdentity{ID: newHook.ID(), Token: newHook.Token})
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return WebhookData{}, ctx.Err()
		}
	}

	if len(pool) == 0 {
		return WebhookData{}, fmt.Errorf("no webhooks available")
	}

	// 2. Threads
	var threadIDs []snowflake.ID
	if config.UseThread && config.ThreadCount > 0 {
		expectedName := tc.Name()
		for _, thread := range activeThreads {
			if thread.ParentID() != nil && *thread.ParentID() == targetID && thread.Name() == expectedName {
				threadIDs = append(threadIDs, thread.ID())
				if len(threadIDs) >= config.ThreadCount {
					break
				}
			}
		}

		if len(threadIDs) < config.ThreadCount {
			select {
			case webhookOpSem <- struct{}{}:
				archived, err := client.Rest.GetPublicArchivedThreads(targetID, time.Time{}, 0, rest.WithCtx(ctx))
				<-webhookOpSem
				if err == nil {
					for _, thread := range archived.Threads {
						if thread.Name() == expectedName {
							threadIDs = append(threadIDs, thread.ID())
							if len(threadIDs) >= config.ThreadCount {
								break
							}
						}
					}
				}
			case <-ctx.Done():
				return WebhookData{}, ctx.Err()
			}
		}

		starterAuthor, starterAvatar := resolveWebhookIdentity(client, config)
		starterMessage := config.ThreadMessage
		if starterMessage == "" {
			starterMessage = "Loop Thread"
		}

		for len(threadIDs) < config.ThreadCount {
			select {
			case <-ctx.Done():
				return WebhookData{}, ctx.Err()
			default:
			}

			var hID snowflake.ID
			var hToken string
			if len(pool) > 0 {
				hID, hToken = pool[0].ID, pool[0].Token
			}

			newThread, err := createThreadWithRetry(ctx, client, targetID, hID, hToken, expectedName, starterMessage, starterAuthor, starterAvatar, isStructured(tc.Type()))
			if err != nil {
				break
			}
			threadIDs = append(threadIDs, newThread.ID())
		}
	}

	return WebhookData{
		Webhooks:     pool,
		ChannelName:  tc.Name(),
		ThreadIDs:    threadIDs,
		IsStructured: isStructured(tc.Type()),
	}, nil
}

func resolveChannelType(t discord.ChannelType) string {
	switch t {
	case discord.ChannelTypeGuildCategory:
		return "category"
	case discord.ChannelTypeGuildForum:
		return "forum"
	case discord.ChannelTypeGuildMedia:
		return "media"
	default:
		return strconv.Itoa(int(t))
	}
}

func checkBotLoopPermissions(client bot.Client, channel discord.GuildChannel, useThreads bool) error {
	required := discord.PermissionViewChannel | discord.PermissionSendMessages | discord.PermissionManageWebhooks
	if useThreads {
		required |= discord.PermissionManageThreads | discord.PermissionSendMessagesInThreads
	}

	if channel.Type() == discord.ChannelTypeGuildForum || channel.Type() == discord.ChannelTypeGuildMedia {
		required |= discord.PermissionManageThreads
	}

	selfMember, ok := client.Caches.Member(channel.GuildID(), client.ApplicationID)
	if !ok {
		return fmt.Errorf("bot member not found in cache for guild %s", channel.GuildID())
	}

	perms := getMemberPermissionsInChannel(client, channel, selfMember)
	if !perms.Has(required) {
		missing := required &^ perms
		return fmt.Errorf("bot lacks required permissions in #%s: %v", channel.Name(), missing)
	}
	return nil
}

func fastCleanThreadParticipants(ctx context.Context, client bot.Client, parentChannelID snowflake.ID, threadIDs []snowflake.ID) {
	atomic.AddInt32(&isCleaningThreads, 1)
	defer atomic.AddInt32(&isCleaningThreads, -1)

	parentChannel, ok := client.Caches.Channel(parentChannelID)
	if !ok {
		return
	}

	loopSys.LogInfo("🧹 [CLEANUP] Starting participant check for %d threads in #%s (Token Bucket Throttled)", len(threadIDs), parentChannel.Name())

	limiter := rate.NewLimiter(rate.Limit(10), 20)

	count := 0
	for i, tid := range threadIDs {
		if i > 0 && i%50 == 0 {
			loopSys.LogInfo("🧹 [CLEANUP] Progress in #%s: %d/%d threads checked... (%d removed)", parentChannel.Name(), i, len(threadIDs), count)
		}

		if err := limiter.Wait(ctx); err != nil {
			return
		}

		if ch, ok := client.Caches.Channel(tid); ok {
			if thread, ok := ch.(discord.GuildThread); ok {
				if thread.MemberCount <= 1 {
					continue
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case webhookOpSem <- struct{}{}:
		}

		members, err := client.Rest.GetThreadMembers(tid)
		if err != nil {
			<-webhookOpSem
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		for _, member := range members {
			// Skip self
			if member.UserID == client.ApplicationID {
				continue
			}

			// Check if member has access to parent channel
			guildMember, ok := client.Caches.Member(parentChannel.GuildID(), member.UserID)
			if !ok {
				if err := limiter.Wait(ctx); err != nil {
					<-webhookOpSem
					return
				}
				_ = client.Rest.RemoveThreadMember(tid, member.UserID)
				count++
				continue
			}

			perms := getMemberPermissionsInChannel(client, parentChannel, guildMember)
			if !perms.Has(discord.PermissionViewChannel) {
				if err := limiter.Wait(ctx); err != nil {
					<-webhookOpSem
					return
				}
				// Remove unauthorized member from thread
				err := client.Rest.RemoveThreadMember(tid, member.UserID)
				if err == nil {
					count++
				}
			}
		}

		<-webhookOpSem
	}

	if count > 0 {
		loopSys.LogInfo("🧹 [CLEANUP] Finished! Removed %d unauthorized participants from threads in #%s", count, parentChannel.Name())
	} else {
		loopSys.LogInfo("🧹 [CLEANUP] Finished! No unauthorized participants found in #%s", parentChannel.Name())
	}
}

func getMemberPermissionsInChannel(client bot.Client, channel discord.GuildChannel, member discord.Member) discord.Permissions {
	guild, ok := client.Caches.Guild(channel.GuildID())
	if !ok {
		return 0
	}

	// Owner bypass
	if guild.OwnerID == member.User.ID {
		return discord.PermissionsAll
	}

	// 1. Base permissions (guild-wide roles)
	var perms discord.Permissions
	if everyoneRole, ok := client.Caches.Role(guild.ID, snowflake.ID(guild.ID)); ok {
		perms |= everyoneRole.Permissions
	}
	for _, roleID := range member.RoleIDs {
		if role, ok := client.Caches.Role(guild.ID, roleID); ok {
			perms |= role.Permissions
		}
	}

	// Administrator bypass
	if perms.Has(discord.PermissionAdministrator) {
		return discord.PermissionsAll
	}

	// 2. Overwrites
	overwrites := channel.PermissionOverwrites()

	// 2.1 @everyone Overwrites
	for _, o := range overwrites {
		if o.ID() == snowflake.ID(guild.ID) {
			if ro, ok := o.(discord.RolePermissionOverwrite); ok {
				perms &^= ro.Deny
				perms |= ro.Allow
			}
			break
		}
	}

	// 2.2 Role Overwrites
	var roleAllow, roleDeny discord.Permissions
	for _, o := range overwrites {
		for _, rID := range member.RoleIDs {
			if o.ID() == rID {
				if ro, ok := o.(discord.RolePermissionOverwrite); ok {
					roleDeny |= ro.Deny
					roleAllow |= ro.Allow
				}
				break
			}
		}
	}
	perms &^= roleDeny
	perms |= roleAllow

	// 2.3 Member Overwrites
	for _, o := range overwrites {
		if o.ID() == member.User.ID {
			if mo, ok := o.(discord.MemberPermissionOverwrite); ok {
				perms &^= mo.Deny
				perms |= mo.Allow
			}
			break
		}
	}

	return perms
}

func createWebhookWithRetry(ctx context.Context, client bot.Client, channelID snowflake.ID, suffix string) (*discord.IncomingWebhook, error) {
	name := LoopWebhookName
	if suffix != "" {
		name += "-" + suffix
	}

	webhookOpSem <- struct{}{}
	defer func() {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	loopSys.LogInfo("🔨 [WEBHOOK CREATE] Attempting for channel %s (%s)", channelID, name)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: name}, rest.WithCtx(ctx))
	if err != nil {
		loopSys.LogInfo("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	loopSys.LogInfo("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
	return hook, nil
}

func createThreadWithRetry(ctx context.Context, client bot.Client, channelID snowflake.ID, hookID snowflake.ID, hookToken string, name string, starterContent string, author string, avatar string, isForum bool) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	loopSys.LogInfo("🔨 [THREAD/POST CREATE] Attempting for channel %s (Forum: %v)", channelID, isForum)

	if isForum && hookID != 0 && hookToken != "" {
		// Use Webhook to create the post (ONLY works in Forum/Media channels)
		msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, discord.WebhookMessageCreate{
			Content:    starterContent,
			Username:   author,
			AvatarURL:  avatar,
			ThreadName: name,
		}, rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

		if err != nil {
			loopSys.LogInfo("❌ [THREAD CREATE - WEBHOOK] Failed: %v", err)
			return nil, err
		}

		if msg.Thread != nil {
			loopSys.LogInfo("✅ [THREAD CREATE - WEBHOOK] Success (Thread ID: %s)", msg.Thread.ID())
			return &msg.Thread.GuildThread, nil
		}

		loopSys.LogInfo("   (Thread object missing in response, attempting fetch for message ID %s)", msg.ID)
		select {
		case <-time.After(1 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		fetched, err := client.Rest.GetChannel(msg.ID)
		if err == nil {
			switch ch := fetched.(type) {
			case discord.GuildThread:
				return &ch, nil
			case *discord.GuildThread:
				return ch, nil
			}
		}
		return nil, fmt.Errorf("WEBHOOK_THREAD_NIL_FALLBACK_FETCH_ERROR: %v", err)
	}

	thread, err := client.Rest.CreateThread(channelID, discord.GuildPublicThreadCreate{
		Name: name,
	}, rest.WithCtx(ctx))
	if err != nil {
		loopSys.LogInfo("❌ [THREAD CREATE] Failed: %v", err)
		return nil, err
	}

	if hookID != 0 && hookToken != "" && starterContent != "" {
		_, err = client.Rest.CreateWebhookMessage(hookID, hookToken, discord.WebhookMessageCreate{
			Content:   starterContent,
			Username:  author,
			AvatarURL: avatar,
		}, rest.CreateWebhookMessageParams{ThreadID: thread.ID(), Wait: false}, rest.WithCtx(ctx))
		if err != nil {
			loopSys.LogInfo("⚠️ [THREAD IDENTITY] Failed to send starter message: %v", err)
		}
	}

	loopSys.LogInfo("✅ [THREAD CREATE] Success")
	return thread, nil
}

func boolPtr(b bool) *bool {
	return &b
}

func IsCleaningThreads() bool {
	return atomic.LoadInt32(&isCleaningThreads) > 0
}
