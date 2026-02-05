package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
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

// ===========================
// Command Registration
// ===========================

func init() {
	adminPerm := discord.PermissionAdministrator

	OnClientReady(func(ctx context.Context, client *bot.Client) {
		RegisterDaemon(LogLoopManager, func(ctx context.Context) (bool, func(), func()) { return InitLoopManager(ctx, client) })
	})

	RegisterCommand(discord.SlashCommandCreate{
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

	RegisterAutocompleteHandler("loop", handleLoopAutocomplete)
}

// ===========================
// Loop System Types
// ===========================

// WebhookIdentity stores ID and Token for a webhook
type WebhookIdentity struct {
	ID    snowflake.ID
	Token string
}

// WebhookData stores information about a webhook pool for loop execution
type WebhookData struct {
	Webhooks     []WebhookIdentity
	ChannelName  string
	ThreadIDs    []snowflake.ID
	IsStructured bool
}

// ChannelData stores configuration and webhooks for a loop channel
type ChannelData struct {
	Config *LoopConfig
	Hooks  []WebhookData
}

// LoopState tracks the runtime state of an active loop
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

// webhookCacheEntry caches webhook data to reduce API calls
type webhookCacheEntry struct {
	Webhooks map[snowflake.ID][]discord.Webhook
	Fetched  time.Time
}

// ===========================
// Globals & Constants
// ===========================

const (
	LoopWebhookName = "LoopHook"
	WebhookCacheTTL = 5 * time.Minute
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

// InitLoopManager initializes the loop system, loading configurations and setting up handlers
func InitLoopManager(ctx context.Context, client *bot.Client) (bool, func(), func()) {
	RegisterComponentHandler("vote:", handleVoteButton)

	var rlMu sync.Mutex
	var rlLastTrigger time.Time
	var rlCount int
	OnRateLimitExceeded(func() {
		if atomic.LoadInt32(&isCleaningThreads) > 0 {
			return
		}

		rlMu.Lock()
		defer rlMu.Unlock()

		now := time.Now()
		if now.Sub(rlLastTrigger) > 10*time.Second {
			rlCount = 1
		} else {
			rlCount++
		}
		rlLastTrigger = now

		if rlCount >= 5 {
			LogLoopManager("🛑 Loop system fail-safe triggered (5 rate limits in 10s). Stopping all active operations.")
			StopAllLoops(ctx, client)
			rlCount = 0
		} else {
			LogLoopManager("⚠️ Rate limit detected (%d/5). Continuing...", rlCount)
		}
	})

	if err := ResetAllLoopStates(ctx); err != nil {
		LogLoopManager("⚠️ Failed to reset loop states: %v", err)
	}

	return true, func() {
		configs, err := GetAllLoopConfigs(ctx)
		if err != nil {
			LogLoopManager(MsgLoopFailedToLoadConfigs, err)
			return
		}

		for _, config := range configs {
			data := &ChannelData{
				Config: config,
				Hooks:  nil,
			}
			configuredChannels.Store(config.ChannelID, data)
		}

		if len(configs) > 0 {
			LogLoopManager(MsgLoopLoadedChannels, len(configs))
		}
	}, func() { ShutdownLoopManager(ctx, client) }
}

// ShutdownLoopManager gracefully stops all active loops
func ShutdownLoopManager(ctx context.Context, client *bot.Client) {
	LogLoopManager("Shutting down Loop Manager...")
	StopAllLoops(ctx, client)
}

// ===========================
// Command Handlers
// ===========================

// handleLoop routes loop subcommands to their respective handlers
func handleLoop(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "stats":
		handleLoopStats(event)
	case "erase":
		handleLoopErase(event)
	case "set":
		handleLoopSet(event, data)
	case "start":
		handleLoopStart(event, data)
	case "stop":
		handleLoopStop(event, data)
	case "close":
		handleLoopClose(event, data)
	case "clean":
		handleLoopClean(event, data)
	default:
		log.Printf("Unknown loop subcommand: %s", subCmd)
	}
}

func handleLoopErase(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	targetID, ok := data.OptString("target")
	if !ok {
		return
	}

	_ = event.DeferCreateMessage(true)

	go func() {
		ctx := AppContext
		client := event.Client()

		if targetID == "all" {
			configs, _ := GetAllLoopConfigs(ctx)
			if len(configs) == 0 {
				loopRespond(event, MsgLoopEraseNoConfigs, true)
				return
			}
			count := 0
			for _, cfg := range configs {
				if err := DeleteLoopConfig(ctx, cfg.ChannelID, client); err == nil {
					count++
				}
			}
			loopRespond(event, fmt.Sprintf(MsgLoopErasedBatch, count), true)
			return
		}

		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, MsgLoopErrInvalidSelection, true)
			return
		}

		cfg, err := GetLoopConfig(ctx, tID)
		if err != nil || cfg == nil {
			loopRespond(event, MsgLoopErrConfigNotFound, true)
			return
		}

		name := cfg.ChannelName
		if ch, ok := client.Caches.Channel(tID); ok {
			name = ch.Name()
		}

		if err := DeleteLoopConfig(ctx, tID, client); err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopDeleteFail, name, err), true)
		} else {
			loopRespond(event, fmt.Sprintf(MsgLoopDeleted, name), true)
		}
	}()
}

func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelIDStr, _ := data.OptString("category")
	channelID, err := snowflake.Parse(channelIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidChannel, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	go func() {
		channel, ok := event.Client().Caches.Channel(channelID)
		if !ok {
			loopRespond(event, MsgLoopErrChannelFetchFail, true)
			return
		}

		if channel.Type() != discord.ChannelTypeGuildCategory {
			loopRespond(event, MsgLoopErrOnlyCategories, true)
			return
		}

		existing, _ := GetLoopConfig(AppContext, channelID)

		message := "@everyone"
		if msg, ok := data.OptString("message"); ok {
			message = msg
		} else if existing != nil {
			message = existing.Message
		}

		webhookAuthor := "LoopHook"
		if author, ok := data.OptString("webhook_author"); ok {
			webhookAuthor = author
		} else if existing != nil {
			webhookAuthor = existing.WebhookAuthor
		}

		webhookAvatar := ""
		if avatar, ok := data.OptString("webhook_avatar"); ok {
			webhookAvatar = avatar
		} else if existing != nil {
			webhookAvatar = existing.WebhookAvatar
		}

		threadMessage := ""
		if tmsg, ok := data.OptString("thread_message"); ok {
			threadMessage = tmsg
		} else if existing != nil {
			threadMessage = existing.ThreadMessage
		}

		threadCount := 0
		if count, ok := data.OptInt("thread_count"); ok {
			threadCount = count
		} else if existing != nil {
			threadCount = existing.ThreadCount
		}

		voteChannelID := ""
		if vc, ok := data.OptChannel("vote_channel"); ok {
			voteChannelID = vc.ID.String()
		} else if existing != nil {
			voteChannelID = existing.VoteChannelID
		}

		voteRole := ""
		if vr, ok := data.OptRole("vote_role"); ok {
			voteRole = vr.ID.String()
		} else if existing != nil {
			voteRole = existing.VoteRole
		}

		voteMessage := ""
		if vm, ok := data.OptString("vote_message"); ok {
			voteMessage = strings.ReplaceAll(vm, "\\n", "\n")
		} else if existing != nil {
			voteMessage = existing.VoteMessage
		}

		voteThreshold := 0
		if vt, ok := data.OptInt("vote_threshold"); ok {
			if vt < 0 {
				vt = 0
			}
			if vt > 100 {
				vt = 100
			}
			voteThreshold = vt
		} else if existing != nil {
			voteThreshold = existing.VoteThreshold
		}

		isSerial := false
		if isq, ok := data.OptString("queue"); ok {
			isSerial = isq == "serial"
		} else if existing != nil {
			isSerial = existing.IsSerial
		}

		config := &LoopConfig{
			ChannelID:     channelID,
			ChannelName:   channel.Name(),
			ChannelType:   resolveChannelType(channel.Type()),
			Message:       message,
			WebhookAuthor: webhookAuthor,
			WebhookAvatar: webhookAvatar,
			UseThread:     threadCount > 0,
			ThreadMessage: threadMessage,
			ThreadCount:   threadCount,
			VoteChannelID: voteChannelID,
			VoteRole:      voteRole,
			VoteMessage:   voteMessage,
			VoteThreshold: voteThreshold,
			IsSerial:      isSerial,
		}

		if err := SetLoopConfig(AppContext, event.Client(), channelID, config); err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopSaveFail, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(MsgLoopConfiguredDisp, channel.Name()), true)
	}()
}

func handleLoopStart(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetID := ""
	if t, ok := data.OptString("target"); ok {
		targetID = t
	}
	rounds, _ := data.OptInt("rounds")

	if targetID == "all" {
		_ = event.DeferCreateMessage(true)
		go func() {
			configs, _ := GetAllLoopConfigs(AppContext)
			if len(configs) == 0 {
				_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
					SetIsComponentsV2(true).
					AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgLoopErrNoChannels))).
					Build())
				return
			}

			var ids []snowflake.ID
			for _, cfg := range configs {
				ids = append(ids, cfg.ChannelID)
			}

			_ = BatchStartLoops(AppContext, event.Client(), ids, rounds)

			activeNow := GetActiveLoops()
			var startedNames []string
			for _, cfg := range configs {
				if _, ok := activeNow[cfg.ChannelID]; ok {
					name := cfg.ChannelName
					if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
						name = ch.Name()
					}
					startedNames = append(startedNames, name)
				}
			}

			msg := MsgLoopErrNoneStarted
			if len(startedNames) > 0 {
				msg = fmt.Sprintf(MsgLoopStartedBatch, len(startedNames), strings.Join(startedNames, "**, **"))
			}
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay("> "+msg))).
				Build())
		}()
	} else {
		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, MsgLoopErrInvalidSelection, true)
			return
		}
		_ = event.DeferCreateMessage(true)
		go func() {
			err = StartLoop(AppContext, event.Client(), tID, rounds)
			name := targetID
			if ch, ok := event.Client().Caches.Channel(tID); ok {
				name = ch.Name()
			}
			msg := fmt.Sprintf(MsgLoopStarted, name)
			if err != nil {
				msg = fmt.Sprintf(MsgLoopStartFail, name, err)
			}
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay("> "+msg))).
				Build())
		}()
	}
}

func handleLoopStop(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID string
	if t, ok := data.OptString("target"); ok {
		targetID = t
	}

	_ = event.DeferCreateMessage(true)

	if targetID == "all" {
		go func() {
			activeLoops := GetActiveLoops()
			if len(activeLoops) == 0 {
				loopRespond(event, MsgLoopNoRunning, true)
				return
			}

			stopped := 0
			for channelID := range activeLoops {
				if StopLoopInternal(AppContext, channelID, event.Client()) {
					stopped++
				}
			}

			loopRespond(event, fmt.Sprintf(MsgLoopStoppedBatch, stopped), true)
		}()
	} else {
		tID, err := snowflake.Parse(targetID)
		go func() {
			if err == nil && StopLoopInternal(AppContext, tID, event.Client()) {
				loopRespond(event, MsgLoopStoppedDisp, true)
			} else {
				loopRespond(event, MsgLoopErrStopFail, true)
			}
		}()
	}
}

func handleLoopClose(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetIDStr, _ := data.OptString("target")
	shouldDelete, _ := data.OptBool("delete")

	targetID, err := snowflake.Parse(targetIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidSelection, true)
		return
	}

	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	action := "Closed (Archived)"
	if shouldDelete {
		action = "Deleted"
	}

	go func() {
		ctx := AppContext
		client := event.Client()
		scopeIDs, targetName := resolveScope(client, *guildID, targetID)
		LogLoopManager("[CLOSE] Final scope IDs count: %d for %s", len(scopeIDs), targetName)

		var threadsToProcess []discord.GuildThread

		var activeFeed struct {
			Threads []discord.GuildThread `json:"threads"`
		}
		activeEndpoint := rest.NewEndpoint(http.MethodGet, "/guilds/"+guildID.String()+"/threads/active")
		LogLoopManager("[CLOSE] Fetching all active threads for guild %s via /threads/active...", guildID.String())
		if err := client.Rest.Do(activeEndpoint.Compile(nil), nil, &activeFeed); err == nil {
			LogLoopManager("[CLOSE] Found %d total active threads in guild.", len(activeFeed.Threads))
			for _, t := range activeFeed.Threads {
				pid := t.ParentID()
				if pid != nil && scopeIDs[*pid] {
					threadsToProcess = append(threadsToProcess, t)
					LogLoopManager("[CLOSE] -> MATCH found: Thread '%s' (%s) in parent %s", t.Name(), t.ID(), *pid)
				}
			}
		} else {
			LogLoopManager("⚠️ [CLOSE] Failed to fetch active threads via REST: %v", err)
		}

		if shouldDelete {
			for sid := range scopeIDs {
				if sid == targetID {
					if ch, ok := client.Caches.Channel(sid); ok && ch.Type() == discord.ChannelTypeGuildCategory {
						continue
					}
				}

				archived, err := client.Rest.GetPublicArchivedThreads(sid, time.Time{}, 0)
				if err == nil {
					for _, t := range archived.Threads {
						threadsToProcess = append(threadsToProcess, t)
						LogLoopManager("[CLOSE] -> MATCH (Archived) found: Thread '%s' (%s)", t.Name(), t.ID())
					}
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
		}

		LogLoopManager("[CLOSE] Total threads marked for processing: %d", len(threadsToProcess))

		limiter := rate.NewLimiter(rate.Limit(4), 10)

		count := 0
		processedIDs := make(map[snowflake.ID]bool)
		for _, t := range threadsToProcess {
			if processedIDs[t.ID()] {
				continue
			}
			processedIDs[t.ID()] = true

			if err := limiter.Wait(ctx); err != nil {
				break
			}

			if shouldDelete {
				err := client.Rest.DeleteChannel(t.ID())
				if err == nil {
					count++
				}
			} else {
				if !t.ThreadMetadata.Archived {
					_, err := client.Rest.UpdateChannel(t.ID(), discord.GuildThreadUpdate{
						Archived: boolPtr(true),
					})
					if err == nil {
						count++
					}
				}
			}
		}

		msg := fmt.Sprintf("✅ **Loop Close**: Successfully %s **%d** matching threads.", action, count)
		_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(discord.NewContainer(discord.NewTextDisplay(msg))).
			Build())
	}()
}

func handleLoopStats(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	configs, err := GetAllLoopConfigs(AppContext)
	if err != nil {
		loopRespond(event, MsgLoopErrRetrieveFail, true)
		return
	}

	activeLoops := GetActiveLoops()
	var guildConfigs []*LoopConfig

	for _, cfg := range configs {
		if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
			if ch.GuildID() == *guildID {
				guildConfigs = append(guildConfigs, cfg)
			}
		}
	}

	if len(guildConfigs) == 0 {
		loopRespond(event, MsgLoopErrNoGuildConfigs, true)
		return
	}

	var sb strings.Builder
	sb.WriteString(MsgLoopStatsHeader)

	for _, cfg := range guildConfigs {
		state := activeLoops[cfg.ChannelID]
		emoji, details := getLoopStatusDetails(cfg, state)
		intervalStr := FormatDuration(IntervalMsToDuration(cfg.Interval))

		sb.WriteString(fmt.Sprintf("%s **#%s**\n", emoji, cfg.ChannelName))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsStatus, details))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsInterval, intervalStr))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsMessage, cfg.Message))
		if cfg.WebhookAuthor != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsAuthor, cfg.WebhookAuthor))
		}
		if cfg.WebhookAvatar != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsAvatar, cfg.WebhookAvatar))
		}
		if cfg.UseThread {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsThreads, cfg.ThreadCount))
			if cfg.ThreadMessage != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsThreadMsg, cfg.ThreadMessage))
			}
		}
		if cfg.VoteChannelID != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteChan, cfg.VoteChannelID))
			if cfg.VoteRole != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteRole, cfg.VoteRole))
			}
			if cfg.VoteMessage != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteMsg, cfg.VoteMessage))
			}
			sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteThreshold, cfg.VoteThreshold))
		}
		queueType := "Parallel"
		if cfg.IsSerial {
			queueType = "Serial"
		}
		sb.WriteString(fmt.Sprintf(MsgLoopStatsQueue, queueType))
		sb.WriteString("\n")
	}

	loopRespond(event, sb.String(), true)
}

func handleLoopClean(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetIDStr, _ := data.OptString("target")
	targetID, err := snowflake.Parse(targetIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidSelection, true)
		return
	}

	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	go func() {
		client := event.Client()
		ctx := AppContext

		// 1. Resolve Scope (Target + Children if Category)
		scopeIDs, targetName := resolveScope(client, *guildID, targetID)

		// 2. Identify all parents (channels where we might have threads)
		totalThreadsFound := 0
		var threadMap = make(map[snowflake.ID][]snowflake.ID) // parentID -> []threadIDs

		// A. Get Active Threads to narrow down which channels have actual bot threads
		var activeFeed struct {
			Threads []discord.GuildThread `json:"threads"`
		}
		activeEndpoint := rest.NewEndpoint(http.MethodGet, "/guilds/"+guildID.String()+"/threads/active")
		if err := client.Rest.Do(activeEndpoint.Compile(nil), nil, &activeFeed); err == nil {
			for _, t := range activeFeed.Threads {
				pid := t.ParentID()
				if pid != nil && scopeIDs[*pid] {
					// Check if it's a bot thread (generally same name as parent)
					if parent, ok := client.Caches.Channel(*pid); ok && t.Name() == parent.Name() {
						threadMap[*pid] = append(threadMap[*pid], t.ID())
						totalThreadsFound++
					}
				}
			}
		}

		if totalThreadsFound == 0 {
			msg := fmt.Sprintf("✅ **Loop Clean**: No active bot threads found in **%s**.", targetName)
			_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay(msg))).
				Build())
			return
		}

		msgStart := fmt.Sprintf("🧹 **Loop Clean**: Starting cleanup for **%d** threads in **%s**...\nThis may take a while.", totalThreadsFound, targetName)
		_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(discord.NewContainer(discord.NewTextDisplay(msgStart))).
			Build())

		// 3. Execution
		for parentID, tIDs := range threadMap {
			fastCleanThreadParticipants(ctx, client, parentID, tIDs)
		}

		msgEnd := fmt.Sprintf("✅ **Loop Clean**: Finished cleaning **%d** matching threads in **%s**.", totalThreadsFound, targetName)
		_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(discord.NewContainer(discord.NewTextDisplay(msgEnd))).
			Build())
	}()
}

func handleLoopAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	focusedOpt := ""
	for _, opt := range data.Options {
		if opt.Focused {
			if opt.Value != nil {
				focusedOpt = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	subCmd := ""
	if data.SubCommandName != nil {
		subCmd = *data.SubCommandName
	}

	var choices []discord.AutocompleteChoice

	switch subCmd {
	case "start":
		configs, _ := GetAllLoopConfigs(AppContext)
		activeLoops := GetActiveLoops()
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchStartAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceStartAll, Value: "all"})
			}
		}
		for _, data := range configs {
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				if ch.GuildID() != *event.GuildID() {
					continue
				}
				displayName := ch.Name()
				intervalStr := FormatDuration(IntervalMsToDuration(data.Interval))
				emoji, details := getLoopStatusDetails(data, activeLoops[data.ChannelID])
				if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceStart, displayName, emoji, details, intervalStr),
						Value: data.ChannelID.String(),
					})
				}
			}
		}

	case "set":
		guildID := *event.GuildID()
		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() == guildID && ch.Type() == discord.ChannelTypeGuildCategory {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{Name: fmt.Sprintf(MsgLoopChoiceCategory, ch.Name()), Value: ch.ID().String()})
				}
			}
		}

	case "erase":
		configs, _ := GetAllLoopConfigs(AppContext)
		guildID := *event.GuildID()
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchEraseAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceEraseAll, Value: "all"})
			}
		}
		activeLoops := GetActiveLoops()
		for _, cfg := range configs {
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				if ch.GuildID() != guildID {
					continue
				}
				displayName := ch.Name()
				intervalStr := FormatDuration(IntervalMsToDuration(cfg.Interval))
				emoji, details := getLoopStatusDetails(cfg, activeLoops[cfg.ChannelID])
				if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceErase, displayName, emoji, details, intervalStr),
						Value: cfg.ChannelID.String(),
					})
				}
			}
		}

	case "stop":
		activeLoops := GetActiveLoops()
		configs, _ := GetAllLoopConfigs(AppContext)
		guildID := *event.GuildID()
		if len(activeLoops) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchStopAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceStopAll, Value: "all"})
			}
		}
		for channelID, state := range activeLoops {
			if ch, ok := event.Client().Caches.Channel(channelID); ok {
				if ch.GuildID() != guildID {
					continue
				}
				name := ch.Name()
				var config *LoopConfig
				for _, cfg := range configs {
					if cfg.ChannelID == channelID {
						config = cfg
						break
					}
				}
				if config == nil {
					continue
				}
				intervalStr := FormatDuration(IntervalMsToDuration(config.Interval))
				emoji, details := getLoopStatusDetails(config, state)
				if focusedOpt == "" || strings.Contains(strings.ToLower(name), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceStop, name, emoji, details, intervalStr),
						Value: channelID.String(),
					})
				}
			}
		}

	case "close", "clean":
		guildID := *event.GuildID()
		configs, _ := GetAllLoopConfigs(AppContext)
		configuredMap := make(map[snowflake.ID]bool)
		for _, cfg := range configs {
			configuredMap[cfg.ChannelID] = true
		}

		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() != guildID {
				continue
			}

			isTarget := false
			prefix := ""
			switch ch.Type() {
			case discord.ChannelTypeGuildCategory:
				isTarget = true
				prefix = "📁"
			case discord.ChannelTypeGuildForum, discord.ChannelTypeGuildMedia:
				isTarget = true
				prefix = "🏷️"
			default:
				if configuredMap[ch.ID()] {
					isTarget = true
					prefix = "🔄"
				}
			}

			if isTarget {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf("%s %s", prefix, ch.Name()),
						Value: ch.ID().String(),
					})
				}
			}
			if len(choices) >= 25 {
				break
			}
		}

	default:
	}

	if len(choices) > 25 {
		choices = choices[:25]
	}
	event.AutocompleteResult(choices)
}

// ===========================
// Internal Logic
// ===========================

// loopRespond sends a formatted response message for loop commands
func loopRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	var displayContent string
	if !strings.HasPrefix(content, "#") && !strings.HasPrefix(content, ">") {
		displayContent = "> " + content
	} else {
		displayContent = content
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(discord.NewContainer(discord.NewTextDisplay(displayContent))).
		SetEphemeral(ephemeral)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		updateBuilder := discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(discord.NewContainer(discord.NewTextDisplay(displayContent)))
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), updateBuilder.Build())
	}
}

// getLoopStatusDetails returns an emoji and status description for a loop
func getLoopStatusDetails(cfg *LoopConfig, state *LoopState) (string, string) {
	if state == nil {
		return MsgLoopStatusStopped, ""
	}
	emoji := MsgLoopStatusRunning
	details := ""
	if cfg.Interval > 0 {
		details += fmt.Sprintf(MsgLoopStatusRound, state.CurrentRound)
	} else {
		details += fmt.Sprintf(MsgLoopStatusRoundBatch, state.CurrentRound, state.RoundsTotal)
	}
	if !state.NextRun.IsZero() {
		details += fmt.Sprintf(MsgLoopStatusNextRun, state.NextRun.Format(DefaultTimeFormat))
	}
	return emoji, details
}

func SetLoopConfig(ctx context.Context, client *bot.Client, channelID snowflake.ID, config *LoopConfig) error {
	if err := AddLoopConfig(ctx, channelID, config); err != nil {
		return err
	}
	configuredChannels.Store(channelID, &ChannelData{Config: config})
	LogLoopManager(MsgLoopConfigured, config.ChannelName)
	return nil
}

func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID, client *bot.Client) error {
	StopLoopInternal(ctx, channelID, client)
	configuredChannels.Delete(channelID)
	return DeleteLoopConfigDB(ctx, channelID)
}

func GetActiveLoops() map[snowflake.ID]*LoopState {
	res := make(map[snowflake.ID]*LoopState)
	activeLoops.Range(func(k, v any) bool {
		res[k.(snowflake.ID)] = v.(*LoopState)
		return true
	})
	return res
}

func StartLoop(ctx context.Context, client *bot.Client, channelID snowflake.ID, rounds int) error {
	return BatchStartLoops(ctx, client, []snowflake.ID{channelID}, rounds)
}

func BatchStartLoops(ctx context.Context, client *bot.Client, channelIDs []snowflake.ID, rounds int) error {
	if atomic.LoadInt32(&isEmergencyStop) == 1 {
		return fmt.Errorf("cannot start loops: system is currently in emergency stop due to rate limits")
	}

	var toStart []*ChannelData
	for _, id := range channelIDs {
		dataVal, ok := configuredChannels.Load(id)
		if !ok {
			continue
		}
		data := dataVal.(*ChannelData)
		if _, running := activeLoops.Load(id); running {
			continue
		}
		if err := loadWebhooksForChannelWithCache(ctx, client, data); err != nil {
			LogLoopManager("❌ Failed to prepare webhooks for %s: %v", id, err)
			continue
		}
		toStart = append(toStart, data)
	}

	if len(toStart) == 0 {
		return fmt.Errorf("no loops were able to start")
	}

	var serialToStart []*ChannelData
	parallelCount := 0

	for _, data := range toStart {
		if data.Config.IsSerial {
			serialToStart = append(serialToStart, data)
		} else {
			parallelCount++
			go startLoopInternal(ctx, data.Config.ChannelID, data, client, rounds)
		}
	}

	if len(serialToStart) > 0 {
		loopQueueMu.Lock()
		loopQueue = make([]snowflake.ID, 0, len(serialToStart))
		sort.Slice(serialToStart, func(i, j int) bool { return serialToStart[i].Config.ChannelName < serialToStart[j].Config.ChannelName })
		for _, data := range serialToStart {
			loopQueue = append(loopQueue, data.Config.ChannelID)
		}
		shuffledQueue = nil
		loopQueueMu.Unlock()

		if atomic.LoadInt32(&serialActive) == 0 {
			go startNextInQueue(ctx, client)
		}
	}

	return nil
}

func startNextInQueue(ctx context.Context, client *bot.Client) {
	loopQueueMu.Lock()
	if len(loopQueue) == 0 {
		shuffledQueue = nil
		loopQueueMu.Unlock()
		return
	}
	if len(shuffledQueue) == 0 {
		shuffledQueue = make([]snowflake.ID, len(loopQueue))
		copy(shuffledQueue, loopQueue)
		rand.Shuffle(len(shuffledQueue), func(i, j int) { shuffledQueue[i], shuffledQueue[j] = shuffledQueue[j], shuffledQueue[i] })
	}
	nextID := shuffledQueue[0]
	shuffledQueue = shuffledQueue[1:]
	loopQueueMu.Unlock()

	dataVal, ok := configuredChannels.Load(nextID)
	if !ok {
		startNextInQueue(ctx, client)
		return
	}
	data := dataVal.(*ChannelData)

	atomic.StoreInt32(&serialActive, 1)
	LogLoopManager("[%s] Starting serial loop...", data.Config.ChannelName)
	startLoopInternal(ctx, nextID, data, client, 0)
}

func StopAllLoops(ctx context.Context, client *bot.Client) {
	if !atomic.CompareAndSwapInt32(&isEmergencyStop, 0, 1) {
		return
	}
	activeLoops.Range(func(key, value any) bool {
		StopLoopInternal(ctx, key.(snowflake.ID), client)
		return true
	})
	loopQueueMu.Lock()
	loopQueue = nil
	shuffledQueue = nil
	loopQueueMu.Unlock()
	atomic.StoreInt32(&isEmergencyStop, 0)
}

func StopLoopInternal(ctx context.Context, channelID snowflake.ID, client *bot.Client) bool {
	loopQueueMu.Lock()
	loopQueue = removeFromSlice(loopQueue, channelID)
	shuffledQueue = removeFromSlice(shuffledQueue, channelID)
	loopQueueMu.Unlock()

	if val, ok := activeLoops.LoadAndDelete(channelID); ok {
		state := val.(*LoopState)
		close(state.StopChan)
		if dataVal, ok := configuredChannels.Load(channelID); ok {
			LogLoopManager(MsgLoopStopped, dataVal.(*ChannelData).Config.ChannelName)
		}
		return true
	}
	return false
}

func handleVoteButton(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	parts := strings.Split(customID, ":")
	if len(parts) < 2 {
		return
	}
	channelID, _ := snowflake.Parse(parts[1])
	stateVal, ok := activeLoops.Load(channelID)
	if !ok {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ Loop is no longer active.").SetEphemeral(true).Build())
		return
	}
	state := stateVal.(*LoopState)
	dataVal, ok := configuredChannels.Load(channelID)
	if !ok {
		return
	}
	cfg := dataVal.(*ChannelData).Config
	if !state.IsPaused || state.VoteMessageID != event.Message.ID {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ This vote panel is no longer valid.").SetEphemeral(true).Build())
		return
	}

	hasRole := false
	reqRoleID, _ := snowflake.Parse(cfg.VoteRole)
	for _, rid := range event.Member().RoleIDs {
		if rid == reqRoleID {
			hasRole = true
			break
		}
	}
	if !hasRole {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ You do not have the required role to vote.").SetEphemeral(true).Build())
		return
	}

	if state.Votes == nil {
		state.Votes = make(map[snowflake.ID]struct{})
	}
	if _, voted := state.Votes[event.User().ID]; voted {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ You have already voted!").SetEphemeral(true).Build())
		return
	}
	state.Votes[event.User().ID] = struct{}{}

	valid := len(state.Votes)
	needed := state.NeededVotes
	if needed == 0 {
		needed = 1
	}

	style := discord.ButtonStyleDanger
	if valid >= needed {
		style = discord.ButtonStyleSuccess
	}

	panel := cfg.VoteMessage
	if panel == "" {
		panel = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", cfg.ChannelName)
	}

	update := discord.NewMessageUpdateBuilder().SetIsComponentsV2(true).SetComponents(
		discord.NewContainer(discord.NewSection(discord.NewTextDisplay(panel)).WithAccessory(discord.NewButton(style, formatVoteLabel(valid, needed), customID, "", 0))),
	)
	_ = event.UpdateMessage(update.Build())

	if valid >= needed {
		select {
		case state.ResumeChan <- struct{}{}:
		default:
		}
	}
}

func getRoleMemberCount(client *bot.Client, guildID, roleID snowflake.ID) int {
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

func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client *bot.Client, rounds int) {
	stopChan := make(chan struct{})
	resumeChan := make(chan struct{})
	state := &LoopState{
		StopChan:   stopChan,
		ResumeChan: resumeChan,
	}
	activeLoops.Store(channelID, state)
	SetLoopState(ctx, channelID, true)

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			SetLoopState(ctx, channelID, false)
			if data.Config.IsSerial {
				atomic.StoreInt32(&serialActive, 0)
				go startNextInQueue(ctx, client)
			}
		}()

		seed := time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		hookBuf := make([]WebhookData, len(data.Hooks))

		isFixedRounds := rounds > 0

		content := data.Config.Message
		if content == "" {
			content = "@everyone"
		}
		author, avatar := resolveWebhookIdentity(client, data.Config)
		threadContent := data.Config.ThreadMessage

		if isFixedRounds {
			// --- FIXED ROUNDS MODE ---
			LogLoopManager("[%s] Starting loop for %d rounds", data.Config.ChannelName, rounds)
			state.RoundsTotal = rounds
			for i := range rounds {
				select {
				case <-stopChan:
					return
				default:
				}
				state.CurrentRound = i + 1
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			}
		} else {
			// --- RANDOM/INFINITE MODE ---
			for {
				select {
				case <-stopChan:
					return
				default:
				}

				cycleRounds := rng.Intn(100) + 1
				var delay time.Duration

				state.RoundsTotal = cycleRounds
				state.CurrentRound = 0

				for i := range cycleRounds {
					select {
					case <-stopChan:
						return
					default:
					}
					state.CurrentRound = i + 1
					executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
				}

				if data.Config.IsSerial {
					return
				}

				if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
					LogLoopManager("[%s] Cycle finished (%d rounds). Pausing for vote...", data.Config.ChannelName, cycleRounds)
					state.IsPaused = true
					state.NextRun = time.Time{}

					var voteChanID snowflake.ID
					rawID := data.Config.VoteChannelID
					if strings.Contains(rawID, ":") {
						parts := strings.Split(rawID, ":")
						if len(parts) > 0 {
							rawID = parts[0]
						}
					}

					if pid, err := snowflake.Parse(rawID); err == nil {
						voteChanID = pid
					} else {
						LogLoopManager("⚠️ Invalid VoteChannelID '%s', cannot pause for vote.", data.Config.VoteChannelID)
					}

					var hookID snowflake.ID
					var hookToken string

					select {
					case webhookOpSem <- struct{}{}:
						hooks, err := client.Rest.GetWebhooks(voteChanID)
						if err == nil {
							for _, h := range hooks {
								if incoming, ok := h.(discord.IncomingWebhook); ok && incoming.Token != "" {
									if incoming.Name() == LoopWebhookName {
										hookID = incoming.ID()
										hookToken = incoming.Token
										break
									}
									if hookID == 0 {
										hookID = incoming.ID()
										hookToken = incoming.Token
									}
								}
							}
						}
						select {
						case <-time.After(1 * time.Second):
						case <-ctx.Done():
							return
						}
						<-webhookOpSem
					case <-ctx.Done():
						return
					}

					if hookID == 0 {
						select {
						case webhookOpSem <- struct{}{}:
							wh, err := client.Rest.CreateWebhook(voteChanID, discord.WebhookCreate{Name: LoopWebhookName})
							if err == nil {
								hookID = wh.ID()
								hookToken = wh.Token
								if hookToken == "" {
									LogLoopManager("⚠️ Created webhook for vote channel %s but could not retrieve token.", voteChanID)
								}
							} else {
								LogLoopManager("⚠️ Failed to create webhook for vote channel %s: %v", voteChanID, err)
							}
							select {
							case <-time.After(2 * time.Second):
							case <-ctx.Done():
								return
							}
							<-webhookOpSem
						case <-ctx.Done():
							return
						}
					}

					if hookID != 0 {
						if state.VoteMessageID != 0 {
							_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
						}

						panelContent := data.Config.VoteMessage
						if panelContent == "" {
							panelContent = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", data.Config.ChannelName)
						}

						state.Votes = make(map[snowflake.ID]struct{})
						requiredRoleID, _ := snowflake.Parse(data.Config.VoteRole)
						guildID := snowflake.ID(0)
						if ch, ok := client.Caches.Channel(channelID); ok {
							guildID = ch.GuildID()
						}

						totalRoleMembers := getRoleMemberCount(client, guildID, requiredRoleID)
						state.NeededVotes = int(math.Ceil(float64(totalRoleMembers) * float64(data.Config.VoteThreshold) / 100.0))
						if state.NeededVotes == 0 {
							state.NeededVotes = 1
						}

						label := formatVoteLabel(0, state.NeededVotes)
						voteCustomID := fmt.Sprintf("vote:%s", channelID)
						_, voteAvatar := resolveWebhookIdentity(client, data.Config)

						builder := discord.NewWebhookMessageCreateBuilder().
							SetIsComponentsV2(true).
							AddComponents(
								discord.NewContainer(
									discord.NewSection(
										discord.NewTextDisplay(panelContent),
									).WithAccessory(
										discord.NewButton(discord.ButtonStyleDanger, label, voteCustomID, "", 0),
									),
								),
							).
							SetAllowedMentions(&discord.AllowedMentions{
								Parse: []discord.AllowedMentionType{
									discord.AllowedMentionTypeEveryone,
									discord.AllowedMentionTypeRoles,
									discord.AllowedMentionTypeUsers,
								},
							}).
							SetUsername(fmt.Sprintf("Loop Finished: %s (%d rounds)", data.Config.ChannelName, cycleRounds)).
							SetAvatarURL(voteAvatar)

						if voteCh, ok := client.Caches.Channel(voteChanID); ok {
							if voteCh.Type() == discord.ChannelTypeGuildForum || voteCh.Type() == discord.ChannelTypeGuildMedia {
								builder.SetThreadName(fmt.Sprintf("Loop Vote: %s", data.Config.ChannelName))
							}
						}

						msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, builder.Build(), rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

						if err == nil {
							state.VoteMessageID = msg.ID
						} else {
							LogLoopManager("⚠️ Failed to send vote panel message to channel %s (Hook: %s): %v", voteChanID, hookID, err)
						}
					}

					select {
					case <-resumeChan:
						LogLoopManager("[%s] Loop resumed by vote!", data.Config.ChannelName)
						state.IsPaused = false
						select {
						case <-time.After(3 * time.Second):
						case <-ctx.Done():
							return
						}
						if state.VoteMessageID != 0 {
							_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
							state.VoteMessageID = 0
						}
					case <-stopChan:
						return
					}
				} else {
					delay = time.Duration(rng.Intn(300)+1) * time.Second
					LogLoopManager("[%s] Cycle finished (%d rounds). Next cycle in %s", data.Config.ChannelName, cycleRounds, FormatDuration(delay))
					state.NextRun = time.Now().UTC().Add(delay)
					select {
					case <-time.After(delay):
						state.NextRun = time.Time{}
					case <-stopChan:
						return
					}
				}
			}
		}

		if data.Config.IsSerial {
			LogLoopManager("[%s] Serial batch finished. The queue will continue...", data.Config.ChannelName)
		} else {
			LogLoopManager("[%s] Parallel batch finished.", data.Config.ChannelName)
		}
	}()
}

func executeRound(ctx context.Context, data *ChannelData, client *bot.Client, stopChan chan struct{}, content, threadContent, author, avatar string, rng *rand.Rand, hookBuf []WebhookData) {
	if len(hookBuf) != len(data.Hooks) {
		hookBuf = make([]WebhookData, len(data.Hooks))
	}
	copy(hookBuf, data.Hooks)

	rng.Shuffle(len(hookBuf), func(i, j int) {
		hookBuf[i], hookBuf[j] = hookBuf[j], hookBuf[i]
	})

	rate := rng.Intn(50) + 1
	delay := max(time.Second/time.Duration(rate), 20*time.Millisecond)

	var wg sync.WaitGroup
	for _, h := range hookBuf {
		select {
		case <-stopChan:
			goto Wait
		default:
		}

		workerJitter := time.Duration(rng.Intn(50)) * time.Millisecond

		wg.Add(1)
		go func(hd WebhookData, startJitter time.Duration, stepDelay time.Duration) {
			defer wg.Done()

			select {
			case <-time.After(startJitter):
			case <-stopChan:
				return
			}

			// 1. Helper for sending with retries
			sendWithRetry := func(threadID snowflake.ID, msgContent string, wh WebhookIdentity) {
				if wh.ID == 0 {
					return
				}
				select {
				case messageSendSem <- struct{}{}:
					defer func() { <-messageSendSem }()
				case <-stopChan:
					return
				case <-ctx.Done():
					return
				}

				backoffs := []time.Duration{1 * time.Second, 2 * time.Second}
				for attempt := 0; attempt <= len(backoffs); attempt++ {
					select {
					case <-stopChan:
						return
					default:
					}

					params := rest.CreateWebhookMessageParams{Wait: false}
					if threadID != 0 {
						params.ThreadID = threadID
					}

					_, err := client.Rest.CreateWebhookMessage(wh.ID, wh.Token, discord.WebhookMessageCreate{
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
						Flags: discord.MessageFlagSuppressNotifications,
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

			// 2. Helper to get next webhook from pool
			getWebhook := func(idx int) WebhookIdentity {
				if len(hd.Webhooks) == 0 {
					return WebhookIdentity{}
				}
				return hd.Webhooks[idx%len(hd.Webhooks)]
			}

			// 3. Send to main channel (Synchronous - Priority)
			isStructured := hd.IsStructured
			if content != "" && !isStructured {
				sendWithRetry(0, content, getWebhook(0))
			}

			// 4. Send to threads (Parallel)
			var shardWg sync.WaitGroup
			if threadContent != "" && len(hd.ThreadIDs) > 0 {
				for i, tid := range hd.ThreadIDs {
					select {
					case <-stopChan:
						return
					default:
					}

					shardWg.Add(1)
					go func(tID snowflake.ID, index int) {
						defer shardWg.Done()
						sendWithRetry(tID, threadContent, getWebhook(index))
					}(tid, i)

					if i > 0 && i%50 == 0 {
						select {
						case <-time.After(5 * time.Millisecond):
						case <-stopChan:
							return
						case <-ctx.Done():
							return
						}
					}
				}
			}

			shardWg.Wait()
		}(h, workerJitter, delay)
		select {
		case <-time.After(delay):
		case <-stopChan:
			goto Wait
		}
	}

Wait:
	wg.Wait()
}

func loadWebhooksForChannelWithCache(ctx context.Context, client *bot.Client, data *ChannelData) error {
	var channel discord.GuildChannel
	var ok bool

	for i := 0; i < 60; i++ {
		if ch, found := client.Caches.Channel(data.Config.ChannelID); found {
			channel = ch
			ok = true
			break
		}
		if i == 10 {
			LogLoopManager("Still waiting for category %s to appear in cache...", data.Config.ChannelID)
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
			LogLoopManager("Cached %d webhooks for guild %s", len(hooks), guildID)
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

func prepareWebhooksForChannel(ctx context.Context, client *bot.Client, channel discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
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
		LogLoopManager("❌ Failed to prepare %s: %v", channel.Name(), err)
		return err
	}

	data.Hooks = []WebhookData{workload}
	LogLoopManager("[%s] Prepared %d webhooks for channel with %d threads...", channel.Name(), len(workload.Webhooks), len(workload.ThreadIDs))
	return nil
}

func prepareWebhooksForCategory(ctx context.Context, client *bot.Client, category discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
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
			LogLoopManager("❌ Failed to prepare %s (skipping): %v", tc.Name(), err)
			continue
		}
		hooks = append(hooks, workload)
	}

	data.Hooks = hooks
	totalWebhooks := 0
	for _, h := range hooks {
		totalWebhooks += len(h.Webhooks)
	}
	LogLoopManager("[%s] Preparing %d webhooks across %d channels (+ %d threads)...", category.Name(), totalWebhooks, len(hooks), data.Config.ThreadCount*len(hooks))
	return nil
}

func resolveWebhookIdentity(client *bot.Client, config *LoopConfig) (string, string) {
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

func resolveScope(client *bot.Client, guildID, targetID snowflake.ID) (map[snowflake.ID]bool, string) {
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

func prepareWorkload(ctx context.Context, client *bot.Client, tc discord.GuildChannel, config *LoopConfig, webhooks []discord.Webhook, activeThreads []discord.GuildThread) (WebhookData, error) {
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

// ===========================
// Permission & Access Logic
// ===========================

func checkBotLoopPermissions(client *bot.Client, channel discord.GuildChannel, useThreads bool) error {
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

func fastCleanThreadParticipants(ctx context.Context, client *bot.Client, parentChannelID snowflake.ID, threadIDs []snowflake.ID) {
	atomic.AddInt32(&isCleaningThreads, 1)
	defer atomic.AddInt32(&isCleaningThreads, -1)

	parentChannel, ok := client.Caches.Channel(parentChannelID)
	if !ok {
		return
	}

	LogLoopManager("🧹 [CLEANUP] Starting participant check for %d threads in #%s (Token Bucket Throttled)", len(threadIDs), parentChannel.Name())

	limiter := rate.NewLimiter(rate.Limit(10), 20)

	count := 0
	for i, tid := range threadIDs {
		if i > 0 && i%50 == 0 {
			LogLoopManager("🧹 [CLEANUP] Progress in #%s: %d/%d threads checked... (%d removed)", parentChannel.Name(), i, len(threadIDs), count)
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
		LogLoopManager("🧹 [CLEANUP] Finished! Removed %d unauthorized participants from threads in #%s", count, parentChannel.Name())
	} else {
		LogLoopManager("🧹 [CLEANUP] Finished! No unauthorized participants found in #%s", parentChannel.Name())
	}
}

func getMemberPermissionsInChannel(client *bot.Client, channel discord.GuildChannel, member discord.Member) discord.Permissions {
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

func createWebhookWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID, suffix string) (*discord.IncomingWebhook, error) {
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

	LogLoopManager("🔨 [WEBHOOK CREATE] Attempting for channel %s (%s)", channelID, name)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: name}, rest.WithCtx(ctx))
	if err != nil {
		LogLoopManager("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	LogLoopManager("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
	return hook, nil
}

func createThreadWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID, hookID snowflake.ID, hookToken string, name string, starterContent string, author string, avatar string, isForum bool) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	LogLoopManager("🔨 [THREAD/POST CREATE] Attempting for channel %s (Forum: %v)", channelID, isForum)

	if isForum && hookID != 0 && hookToken != "" {
		// Use Webhook to create the post (ONLY works in Forum/Media channels)
		msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, discord.WebhookMessageCreate{
			Content:    starterContent,
			Username:   author,
			AvatarURL:  avatar,
			ThreadName: name,
		}, rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

		if err != nil {
			LogLoopManager("❌ [THREAD CREATE - WEBHOOK] Failed: %v", err)
			return nil, err
		}

		if msg.Thread != nil {
			LogLoopManager("✅ [THREAD CREATE - WEBHOOK] Success (Thread ID: %s)", msg.Thread.ID())
			return &msg.Thread.GuildThread, nil
		}

		LogLoopManager("   (Thread object missing in response, attempting fetch for message ID %s)", msg.ID)
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
		LogLoopManager("❌ [THREAD CREATE] Failed: %v", err)
		return nil, err
	}

	if hookID != 0 && hookToken != "" && starterContent != "" {
		_, err = client.Rest.CreateWebhookMessage(hookID, hookToken, discord.WebhookMessageCreate{
			Content:   starterContent,
			Username:  author,
			AvatarURL: avatar,
		}, rest.CreateWebhookMessageParams{ThreadID: thread.ID(), Wait: false}, rest.WithCtx(ctx))
		if err != nil {
			LogLoopManager("⚠️ [THREAD IDENTITY] Failed to send starter message: %v", err)
		}
	}

	LogLoopManager("✅ [THREAD CREATE] Success")
	return thread, nil
}
