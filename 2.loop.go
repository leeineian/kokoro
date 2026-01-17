package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"regexp"
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
					discord.ApplicationCommandOptionString{
						Name:        "duration",
						Description: "Duration to run (e.g., 30s, 5m, 1h). Leave empty for random mode.",
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
				Name:        "stats",
				Description: "View all current loop configurations and their status",
			},
		},
	}, handleLoop)

	RegisterAutocompleteHandler("loop", handleLoopAutocomplete)
}

// ===========================
// Loop System Types
// ===========================

// WebhookData stores information about a webhook for loop execution
type WebhookData struct {
	WebhookID    snowflake.ID
	WebhookToken string
	ChannelName  string
	ThreadIDs    []snowflake.ID
}

// ChannelData stores configuration and webhooks for a loop channel
type ChannelData struct {
	Config *LoopConfig
	Hooks  []WebhookData
}

// LoopState tracks the runtime state of an active loop
type LoopState struct {
	StopChan        chan struct{}
	ResumeChan      chan struct{}
	IsPaused        bool
	VoteMessageID   snowflake.ID
	Votes           map[snowflake.ID]struct{}
	RoundsTotal     int
	CurrentRound    int
	NextRun         time.Time
	EndTime         time.Time
	DurationTimeout *time.Timer
	NeededVotes     int
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
	// Limit webhook creation/deletion operations
	webhookOpSem = make(chan struct{}, 1)
	// Limit concurrent message sends (prevents connection exhaustion/503s)
	messageSendSem = make(chan struct{}, 200)

	// Serial Loop Queue
	loopQueue     []snowflake.ID
	shuffledQueue []snowflake.ID
	loopQueueMu   sync.Mutex
)

// InitLoopManager initializes the loop system, loading configurations and setting up handlers
func InitLoopManager(ctx context.Context, client *bot.Client) (bool, func(), func()) {
	RegisterComponentHandler("vote:", handleVoteButton)

	// Register rate limit failsafe
	OnRateLimitExceeded(func() {
		LogLoopManager("🛑 Loop system fail-safe triggered. Stopping all active operations.")
		StopAllLoops(ctx, client)
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
	default:
		log.Printf("Unknown loop subcommand: %s", subCmd)
	}
}

func handleLoopErase(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if targetID, ok := data.OptString("target"); ok {
		handleLoopSelect(event, targetID)
		return
	}
}

func handleLoopSelect(event *events.ApplicationCommandInteractionCreate, targetID string) {
	_ = event.DeferCreateMessage(true)

	if targetID == "all" {
		go func() {
			configs, _ := GetAllLoopConfigs(AppContext)
			if len(configs) == 0 {
				loopRespond(event, MsgLoopEraseNoConfigs, true)
				return
			}

			count := 0
			for _, cfg := range configs {
				if err := DeleteLoopConfig(AppContext, cfg.ChannelID, event.Client()); err == nil {
					count++
				}
			}

			loopRespond(event, fmt.Sprintf(MsgLoopErasedBatch, count), true)
		}()
		return
	}

	tID, err := snowflake.Parse(targetID)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidSelection, true)
		return
	}

	go func() {
		cfg, err := GetLoopConfig(AppContext, tID)
		if err != nil || cfg == nil {
			loopRespond(event, MsgLoopErrConfigNotFound, true)
			return
		}

		currentName := cfg.ChannelName
		if channel, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
			currentName = channel.Name()
		}

		err = DeleteLoopConfig(AppContext, tID, event.Client())
		if err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopDeleteFail, currentName, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(MsgLoopDeleted, currentName), true)
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
		} else {
			if guild, ok := event.Client().Caches.Guild(*event.GuildID()); ok {
				if icon := guild.IconURL(); icon != nil {
					webhookAvatar = *icon
				}
			}
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

		config := &LoopConfig{
			ChannelID:     channelID,
			ChannelName:   channel.Name(),
			ChannelType:   "category",
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
		}

		if err := SetLoopConfig(AppContext, event.Client(), channelID, config); err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopSaveFail, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(MsgLoopConfiguredDisp, channel.Name()), true)
	}()
}

func handleLoopStart(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID, durationStr string
	if t, ok := data.OptString("target"); ok {
		targetID = t
	}
	if d, ok := data.OptString("duration"); ok {
		durationStr = d
	}

	duration := IntervalMsToDuration(0)
	if durationStr != "" {
		parsed, err := ParseDuration(durationStr)
		if err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopErrInvalidDuration, err), true)
			return
		}
		duration = parsed
	}

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

			_ = BatchStartLoops(AppContext, event.Client(), ids, duration)

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
			err = StartLoop(AppContext, event.Client(), tID, duration)
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
		sb.WriteString("\n")
	}

	loopRespond(event, sb.String(), true)
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
	} else if !state.EndTime.IsZero() {
		if state.EndTime.After(time.Now().UTC()) {
			details += fmt.Sprintf(MsgLoopStatusEnds, state.EndTime.Format(DefaultTimeFormat))
		} else {
			details += MsgLoopStatusFinishing
		}
	}
	return emoji, details
}

// ===========================
// Logic from proc.loop.go
// ===========================

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

func StartLoop(ctx context.Context, client *bot.Client, channelID snowflake.ID, interval time.Duration) error {
	return BatchStartLoops(ctx, client, []snowflake.ID{channelID}, interval)
}

func BatchStartLoops(ctx context.Context, client *bot.Client, channelIDs []snowflake.ID, interval time.Duration) error {
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
		if interval > 0 {
			data.Config.Interval = int(interval.Milliseconds())
		}
		toStart = append(toStart, data)
	}

	if len(toStart) == 0 {
		return fmt.Errorf("no loops were able to start")
	}

	loopQueueMu.Lock()
	loopQueue = make([]snowflake.ID, 0, len(toStart))
	sort.Slice(toStart, func(i, j int) bool { return toStart[i].Config.ChannelName < toStart[j].Config.ChannelName })
	for _, data := range toStart {
		loopQueue = append(loopQueue, data.Config.ChannelID)
	}
	shuffledQueue = nil
	loopQueueMu.Unlock()

	startNextInQueue(ctx, client)
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

	LogLoopManager("[%s] Starting serial loop...", data.Config.ChannelName)
	startLoopInternal(ctx, nextID, data, client)
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
	for i, id := range loopQueue {
		if id == channelID {
			loopQueue = append(loopQueue[:i], loopQueue[i+1:]...)
			break
		}
	}
	for i, id := range shuffledQueue {
		if id == channelID {
			shuffledQueue = append(shuffledQueue[:i], shuffledQueue[i+1:]...)
			break
		}
	}
	loopQueueMu.Unlock()

	if val, ok := activeLoops.LoadAndDelete(channelID); ok {
		state := val.(*LoopState)
		close(state.StopChan)
		if state.DurationTimeout != nil {
			state.DurationTimeout.Stop()
		}
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

func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client *bot.Client) {
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
		}()

		// Local RNG for this loop instance (avoids global lock convention)
		seed := time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		// Pre-allocated buffer for hooks to reduce GC pressure
		hookBuf := make([]WebhookData, len(data.Hooks))

		// Jitter: random 1 to 10-second delay before starting
		jitter := time.Duration(rng.Intn(10)+1) * time.Second
		LogLoopManager("[%s] Applying %s startup jitter...", data.Config.ChannelName, jitter)
		select {
		case <-time.After(jitter):
		case <-stopChan:
			return
		}

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimed := interval > 0

		if isTimed {
			LogLoopManager(MsgLoopStartingTimed, FormatDuration(interval))
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

		if isTimed {
			// --- TIMED MODE ---
			for {
				select {
				case <-stopChan:
					return
				default:
				}
				state.CurrentRound++
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			}
		} else {
			// Random Mode
			rounds := rng.Intn(100) + 1
			var delay time.Duration

			state.RoundsTotal = rounds
			state.CurrentRound = 0

			totalPings := rounds * len(data.Hooks) * (1 + data.Config.ThreadCount)
			if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
				LogLoopManager("[%s] Random: %d rounds (%d pings)", data.Config.ChannelName, rounds, totalPings)
				// Delay remains 0, won't be used
			} else {
				delay = time.Duration(rng.Intn(1000)+1) * time.Second
				LogLoopManager(MsgLoopRandomStatus, data.Config.ChannelName, rounds, totalPings, FormatDuration(delay))
			}

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
			if delay > 0 {
				state.NextRun = time.Now().UTC().Add(delay)
			} else {
				state.NextRun = time.Time{} // Paused or instant
			}

			// Interaction / Pause Logic
			if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
				LogLoopManager("[%s] Pausing for vote...", data.Config.ChannelName)
				state.IsPaused = true
				state.NextRun = time.Time{} // Clear next run as we are paused indefinitely

				// 1. Get or Create Webhook for the Vote Channel
				var voteChanID snowflake.ID

				// Handle potential legacy format (CHAN:MSG) or just CHAN
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
					// Prevent infinite pause without interaction possibility?
					// For now, we just fail to post the message, but loop remains paused.
				}

				var hookID snowflake.ID
				var hookToken string

				// Fetch existing webhooks
				if hooks, err := client.Rest.GetWebhooks(voteChanID); err == nil {
					for _, h := range hooks {
						// Look for a token-based incoming webhook, preferably ours
						if incoming, ok := h.(discord.IncomingWebhook); ok {
							hookID = incoming.ID()
							hookToken = incoming.Token
							if incoming.Name() == LoopWebhookName {
								break // Found our preferred one
							}
						}
					}
				}

				if hookID == 0 {
					wh, err := client.Rest.CreateWebhook(voteChanID, discord.WebhookCreate{
						Name: LoopWebhookName,
					})
					if err == nil {
						hookID = wh.ID()
						hookToken = wh.Token
					} else {
						LogLoopManager("⚠️ Failed to create webhook for vote channel %s: %v", voteChanID, err)
					}
				}

				// 2. Send "Panel Message" using the webhook
				if hookID != 0 {
					// Delete old notification if exists (cleanup)
					if state.VoteMessageID != 0 {
						_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
					}

					panelContent := data.Config.VoteMessage
					if panelContent == "" {
						panelContent = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", data.Config.ChannelName)
					}

					// Initialize votes
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
						SetUsername(data.Config.ChannelName).
						SetAvatarURL(data.Config.WebhookAvatar)

					msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, builder.Build(), rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

					if err == nil {
						state.VoteMessageID = msg.ID
					} else {
						LogLoopManager("⚠️ Failed to send vote panel message: %v", err)
					}
				}

				// Wait for resume signal
				select {
				case <-resumeChan:
					// Resumed!
					LogLoopManager("[%s] Loop resumed by vote!", data.Config.ChannelName)
					state.IsPaused = false

					// Give a small delay so users see the final vote count and green button
					time.Sleep(3 * time.Second)

					if state.VoteMessageID != 0 {
						// Clean up notification
						_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
						state.VoteMessageID = 0
					}
				case <-stopChan:
					return
				}
			} else {
				// Standard delay
				select {
				case <-time.After(delay):
					state.NextRun = time.Time{}
				case <-stopChan:
					return
				}
			}
		}

		// Natural finish of the batch
		LogLoopManager("[%s] Batch finished. Moving to next in queue...", data.Config.ChannelName)
		// Trigger next loop in the queue
		go startNextInQueue(ctx, client)
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
		go func(hd WebhookData, startJitter time.Duration, stepDelay time.Duration) {
			defer wg.Done()

			// Minor jitter to prevent simultaneous network spikes
			select {
			case <-time.After(startJitter):
			case <-stopChan:
				return
			}

			// Helper for sending with retries
			sendWithRetry := func(threadID snowflake.ID, msgContent string) {
				// Acquire semaphore to limit concurrent connections
				select {
				case messageSendSem <- struct{}{}:
					defer func() { <-messageSendSem }()
				case <-stopChan:
					return
				case <-ctx.Done():
					return
				}

				backoffs := []time.Duration{2 * time.Second, 4 * time.Second}
				for attempt := 0; attempt <= len(backoffs); attempt++ {
					// Check stop before each attempt
					select {
					case <-stopChan:
						return
					default:
					}

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

			// 1. Send to main channel (Synchronous - Priority)
			if content != "" {
				sendWithRetry(0, content)
			}

			// 2. Send to threads (Parallel but Paced)
			var shardWg sync.WaitGroup
			if threadContent != "" && len(hd.ThreadIDs) > 0 {
				for i, tid := range hd.ThreadIDs {
					// Apply rate limit delay before launching next thread
					if i > 0 || content != "" {
						select {
						case <-time.After(stepDelay):
						case <-stopChan:
							return
						}
					}

					shardWg.Add(1)
					go func(tID snowflake.ID) {
						defer shardWg.Done()
						sendWithRetry(tID, threadContent)
					}(tid)
				}
			}

			// Wait for threads to finish
			shardWg.Wait()
		}(h, workerJitter, delay)

		// Variable delay based on randomized rate
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

	// Wait up to 30 seconds for the channel to appear in cache (it might be missing immediately after READY)
	for i := 0; i < 60; i++ {
		if ch, found := client.Caches.Channel(data.Config.ChannelID); found {
			channel = ch
			ok = true
			break
		}
		if i == 10 { // Only log if still missing after 5 seconds
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
			LogLoopManager("Cached %d webhooks for guild %s", len(hooks), guildID)
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
				LogLoopManager(MsgLoopFailedToCreateWebhook, tc.Name(), err)
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

			// Check for archived threads
			if len(threadIDs) < data.Config.ThreadCount {
				archived, err := client.Rest.GetPublicArchivedThreads(tc.ID(), time.Time{}, 0, rest.WithCtx(ctx))
				if err == nil {
					for _, thread := range archived.Threads {
						if thread.Name() == expectedName && thread.OwnerID == self.ID {
							reopened, err := unarchiveThreadWithRetry(ctx, client, thread.ID())
							if err == nil {
								threadIDs = append(threadIDs, reopened.ID())
								if len(threadIDs) >= data.Config.ThreadCount {
									break
								}
							}
						}
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
					LogLoopManager("❌ Failed to create thread for %s: %v", tc.Name(), err)
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
	LogLoopManager("[%s] Preparing %d webhooks (%d channels + %d threads)...", category.Name(), len(hooks), len(hooks), data.Config.ThreadCount*len(hooks))
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

	LogLoopManager("🔨 [WEBHOOK CREATE] Attempting for channel %s", channelID)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: LoopWebhookName}, rest.WithCtx(ctx))
	if err != nil {
		LogLoopManager("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	LogLoopManager("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
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

	LogLoopManager("🔨 [THREAD CREATE] Attempting for channel %s", channelID)
	thread, err := client.Rest.CreateThread(channelID, discord.GuildPublicThreadCreate{
		Name: name,
	}, rest.WithCtx(ctx))
	if err != nil {
		LogLoopManager("❌ [THREAD CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	LogLoopManager("✅ [THREAD CREATE] Success for channel %s", channelID)
	return thread, nil
}

func unarchiveThreadWithRetry(ctx context.Context, client *bot.Client, threadID snowflake.ID) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	LogLoopManager("🔓 [THREAD UNARCHIVE] Attempting for thread %s", threadID)
	channel, err := client.Rest.UpdateChannel(threadID, discord.GuildThreadUpdate{
		Archived: boolPtr(false),
	}, rest.WithCtx(ctx))
	if err != nil {
		LogLoopManager("❌ [THREAD UNARCHIVE] Failed for thread %s: %v", threadID, err)
		return nil, err
	}

	thread, ok := channel.(discord.GuildThread)
	if !ok {
		return nil, fmt.Errorf("updated channel %s is not a thread", threadID)
	}

	LogLoopManager("✅ [THREAD UNARCHIVE] Success for thread %s", threadID)
	return &thread, nil
}

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞"
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
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

func intPtr(i int) *int    { return &i }
func boolPtr(b bool) *bool { return &b }
