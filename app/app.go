package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"crypto/rand"
	"encoding/binary"
	"sync/atomic"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"github.com/sho0pi/naturaltime"
)

// start

const (
	StatsAnsiReset    = "\u001b[0m"
	StatsAnsiPink     = "\u001b[35m"
	StatsAnsiPinkBold = "\u001b[35;1m"
	StatsCacheTTL     = 5 * time.Second

	MsgBotRebootCommanded      = "Reboot commanded by user %s (%s)"
	MsgBotShutdownCommanded    = "Shutdown commanded by user %s (%s)"
	MsgBotLogReadFail          = "Failed to read log file: %v"
	MsgBotRebooting            = "**Rebooting...**"
	MsgBotShuttingDown         = "**Shutting down...**"
	MsgBotStatsLoading         = "Loading stats..."
	MsgBotStatusUpdated        = "Status visibility updated!"
	MsgBotStatusEnabled        = "Status rotation enabled!"
	MsgBotStatusDisabled       = "Status rotation disabled!"
	MsgBotConsoleDisabled      = "Logging to file is disabled."
	MsgBotConsoleEmpty         = "No logs available."
	MsgBotStatsSendFail        = "Failed to send initial stats: %v"
	MsgBotConsoleBtnOldest     = "[Oldest]"
	MsgBotConsoleBtnOlder      = "[Older]"
	MsgBotConsoleBtnRefresh    = "[Refresh]"
	MsgBotConsoleBtnNewer      = "[Newer]"
	MsgBotConsoleBtnLatest     = "[Latest]"
	MsgBotRebootBuilding       = "**Building...**"
	MsgBotRebootBuildFail      = "❌ **Build Failed**\n```\n%s\n```"
	MsgBotRebootBuildSuccess   = "✅ **Build Successful**"
	MsgBotUnknownSubcommand    = "Unknown subcommand: %s"
	MsgBotStatusPinned         = "Status has been pinned to **%s**."
	MsgBotStatusInvalid        = "Invalid status selection."
	MsgBotServerOnly           = "This command can only be used in a server."
	MsgBotClearCommandsFail    = "Failed to clear commands: %v"
	MsgBotClearCommandsSuccess = "Successfully cleared all guild commands from this server."
	MsgBotLogTruncated         = "Log file truncated by user %s"
	MsgConsoleNavLabel         = "Navigate Logs..."
	MsgStatusRotatorShutdown   = "Shutting down Status Rotator..."
	MsgStatusClearFail         = "Failed to clear status: %v"
	MsgStatusTime              = "Time: %s (Local)"
	MsgBotSendStickerFail      = "Failed to send sticker: %v\nNote: Bots can only send stickers from the same guild or official Discord stickers."
	MsgBotSendStickerSuccess   = "Sticker sent successfully!"
	MsgBotStickerIDInvalid     = "Invalid Sticker ID format."
	MsgStatusUpdateFail        = "Failed to update status: %v"
	MsgStatusRotated           = "Rotated status to %s (next in %s)"
	MsgStatusRotatedNoInterval = "Rotated status to %s"
	MsgDebugStatusCmdFail      = "Status update failed: %v"

	StatusDisableAll = "Disable All Status"
	StatusEnableAll  = "Enable All Status"

	MsgRoleColorFailedToFetchConfigs = "Failed to fetch configs: %v"
	MsgRoleColorNextUpdate           = "Guild %s next update in %d minutes"
	MsgRoleColorUpdateFail           = "Failed to update role %s in guild %s: %v"
	MsgRoleColorUpdated              = "Updated role %s in guild %s to %s"
	MsgRoleColorInvalidHex           = "Invalid hex color code: `%s`. Must be a valid 6-character hex code."
	MsgRoleColorRoleNotFound         = "Could not find your custom color role."
	MsgDebugRoleColorUpdateFail      = "Failed to update guild config: %v"
	MsgDebugRoleColorResetFail       = "Failed to reset guild config: %v"
	MsgDebugRoleColorRefreshFail     = "Failed to refresh role color: %v"
	MsgRoleColorErrGuildOnly         = "This command can only be used in a server."
	MsgRoleColorErrSetFail           = "Failed to set role color configuration."
	MsgRoleColorErrResetFail         = "Failed to reset role color configuration."
	MsgRoleColorErrNoRole            = "No role is configured for color rotation."
	MsgRoleColorErrRefreshFail       = "Failed to refresh role color."
	MsgRoleColorErrNoRoleStats       = "No random color role is currently configured for this server. Use `/rolecolor set` to start!"
	MsgRoleColorSetSuccess           = "Role <@&%s> will now have random colors!"
	MsgRoleColorResetSuccess         = "Role color rotation has been disabled."
	MsgRoleColorRefreshSuccess       = "Role color has been refreshed!"
	MsgRoleColorStatsHeader          = "**Random Role Color Status**"
	MsgRoleColorStatsContent         = "**Current Role:** <@&%s>\n" +
		"**Status:** `Active`\n\n" +
		"The app will periodically change the color of this role to a random vibrant hue."
	minMinutes = 1
	maxMinutes = 10

	MsgReminderFailedToQueryDue      = "Failed to query due reminders: %v"
	MsgReminderFailedToCreateDM      = "Failed to create DM channel for user %s: %v"
	MsgReminderFailedToSend          = "Failed to send reminder %d: %v"
	MsgReminderFailedToDelete        = "Failed to delete sent reminder %d: %v"
	MsgReminderFailedToDeleteGeneral = "Failed to delete reminder: %v"
	MsgReminderSentAndDeleted        = "Sent and deleted reminder %d for user %s"
	MsgReminderFailedToSave          = "Failed to save reminder: %v"
	MsgReminderFailedToDeleteAll     = "Failed to delete all reminders: %v"
	MsgReminderFailedToQuery         = "Failed to query reminders: %v"
	MsgReminderAutocompleteFailed    = "Failed to query reminders for autocomplete: %v"
	MsgReminderRespondError          = "Failed to respond to interaction: %v"
	MsgReminderNaturalTimeInitFail   = "Failed to initialize naturaltime parser: %v"
	ErrReminderParseFailed           = "Failed to parse the date/time. Try formats like 'tomorrow', 'in 2 hours', 'next friday at 3pm'."
	ErrReminderPastTime              = "The reminder time must be in the future!"
	ErrReminderSaveFailed            = "Failed to save reminder. Please try again."
	ErrReminderFetchFailed           = "Failed to retrieve your reminders."
	ErrReminderDismissFailed         = "Failed to dismiss reminder."
	ErrReminderDismissAllFail        = "Failed to dismiss all reminders."
	MsgReminderSetSuccess            = "Reminder set for %s\n\n %s"
	MsgReminderDismissedBatch        = "Dismissed **%d** reminder(s)!"
	MsgReminderNoActive              = "You have no active reminders. Set one with `/reminder set`!"
	MsgReminderDismissed             = "Reminder dismissed!"
	MsgReminderListHeader            = "**Your Reminders** (%d active)\n\n"
	MsgReminderListItem              = "%d. **%s** - %s\n"
	MsgReminderChoiceAll             = "Dismiss All (%d reminders)"
	MsgReminderStatsHeader           = "**Your Active Reminders (%d)**\n\n"
	MsgReminderStatsMore             = "> ...and %d more."
	MsgReminderStatsDue              = "> Due %s (`%s`)\n"
	MsgReminderStatsDM               = "> Delivery: Direct Message\n"
	MsgReminderRelLessMinute         = "in less than a minute"
	MsgReminderRelMinute             = "in 1 minute"
	MsgReminderRelMinutes            = "in %d minutes"
	MsgReminderRelHour               = "in 1 hour"
	MsgReminderRelHours              = "in %d hours"
	MsgReminderRelDay                = "in 1 day"
	MsgReminderRelDays               = "in %d days"
	MsgReminderRelWeek               = "in 1 week"
	MsgReminderRelWeeks              = "in %d weeks"
	MsgReminderRelMonth              = "in 1 month"
	MsgReminderRelMonths             = "in %d months"
	MsgReminderRelYear               = "in 1 year"
	MsgReminderRelYears              = "in %d years"
)

type Reminder struct {
	ID        int64
	UserID    snowflake.ID
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	Message   string
	RemindAt  time.Time
	SendTo    string
	CreatedAt time.Time
}

type Hooks struct {
	LogApp                        func(format string, v ...any)
	LogInfo                       func(format string, v ...any)
	LogError                      func(format string, v ...any)
	LogWarn                       func(format string, v ...any)
	LogDebug                      func(format string, v ...any)
	RegisterCommand               func(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate))
	RegisterAutocompleteHandler   func(commandName string, handler func(event *events.AutocompleteInteractionCreate))
	RegisterComponentHandler      func(customID string, handler func(event *events.ComponentInteractionCreate))
	RespondInteractionV2          func(client bot.Client, interaction discord.Interaction, content string, ephemeral bool) error
	EditInteractionV2             func(client bot.Client, interaction discord.Interaction, content string) error
	RegisterDaemon                func(name string, logFunc func(format string, v ...any), start func(ctx context.Context) (bool, func(), func()))
	OnClientReady                 func(handler func(ctx context.Context, client bot.Client))
	AdminPerm                     int64
	StartupTime                   time.Time
	LogFile                       string
	GetProjectName                func() string
	AppContext                    context.Context
	RequestRestart                func()
	GetAppConfig                  func(ctx context.Context, key string) (string, error)
	SetAppConfig                  func(ctx context.Context, key, value string) error
	GetRemindersCount             func(ctx context.Context) (int, error)
	SetGuildRandomColorRole       func(ctx context.Context, guildID snowflake.ID, roleID snowflake.ID) error
	GetGuildRandomColorRole       func(ctx context.Context, guildID snowflake.ID) (snowflake.ID, error)
	GetAllGuildRandomColorConfigs func(ctx context.Context) (map[snowflake.ID]snowflake.ID, error)
	RandomIntRange                func(min, max int) int
	ColorizeHex                   func(color int) string
	DeleteAllRemindersForUser     func(ctx context.Context, userID snowflake.ID) (int64, error)
	DeleteReminder                func(ctx context.Context, id int64, userID snowflake.ID) (bool, error)
	GetRemindersForUser           func(ctx context.Context, userID snowflake.ID) ([]*Reminder, error)
	AddReminder                   func(ctx context.Context, r *Reminder) error
	ClaimDueReminders             func(ctx context.Context) ([]*Reminder, error)
	SendComponentsV2              func(client bot.Client, channelID snowflake.ID, components []any, messageReference *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error)
	Truncate                      func(s string, length int) string
	NewV2Container                func(components ...any) any
	NewTextDisplay                func(text string) any
	StreamingURL                  string
}

type StatsHealthMetrics struct {
	Ping        int64
	GatewayPing int64
	DBLatency   string
}

type StatsCachedData struct {
	Data      string
	Timestamp time.Time
}

type StatsCachedMetrics struct {
	Data          StatsHealthMetrics
	Timestamp     time.Time
	InteractionID string
}

type StatsCache struct {
	System  StatsCachedData
	Metrics StatsCachedMetrics
}

type roleState struct {
	sync.RWMutex
	guildID   snowflake.ID
	roleID    snowflake.ID
	lastColor string // #HEX
	active    bool   // prevents ghost rotations
}

var (
	hooks           Hooks
	statusMap       map[string]func(context.Context, bot.Client) string
	statusKeys      []string
	lastStatusText  string
	statusMu        sync.RWMutex
	configKeyStatus = "status_visible"
	configKeyPin    = "status_pinned"
	statsStartTime  = time.Now().UTC()
	statsCacheMu    sync.RWMutex
	statsCache      StatsCache

	rotatorTimers sync.Map
	nextUpdateMap sync.Map
	roleStates    sync.Map

	reminderSchedulerRunning int32
	reminderParser           *naturaltime.Parser
)

func Init(h Hooks) {
	hooks = h

	adminPerm := discord.Permissions(hooks.AdminPerm)

	hooks.OnClientReady(func(ctx context.Context, client bot.Client) {
		hooks.RegisterDaemon("APP", hooks.LogApp, func(ctx context.Context) (bool, func(), func()) { return StartStatusRotator(ctx, client) })
	})

	hooks.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "app",
		Description:              "App management utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "reboot",
				Description: "Restart the app",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "build",
						Description: "Whether to rebuild the binary before restarting (default: false)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "shutdown",
				Description: "Shut down the app",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "Display system and application statistics",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "status",
				Description: "Configure app status visibility",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "select",
						Description:  "Select a specific status to pin or enable/disable rotation",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "console",
				Description: "View recent app logs",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "truncate",
						Description: "Whether to clear the log file before viewing (default: false)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "cleanup",
				Description: "Clear all guild commands from the current server",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "send",
				Description: "Send a Discord sticker (Admin Only)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "sticker_id",
						Description: "The ID of the sticker to send",
						Required:    true,
					},
				},
			},
		},
	}, handleBot)

	hooks.OnClientReady(func(ctx context.Context, client bot.Client) {
		hooks.RegisterDaemon("ROLE", hooks.LogInfo, func(ctx context.Context) (bool, func(), func()) { return StartRoleColorRotator(ctx, client) })
	})

	hooks.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "rolecolor",
		Description:              "Random Role Color Utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Set the role to randomly color",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionRole{
						Name:        "role",
						Description: "The role to color",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "reset",
				Description: "Reset configuration",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "refresh",
				Description: "Force an immediate color change",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View current random color role configuration",
			},
		},
	}, handleRoleColor)

	initReminderParser()

	hooks.OnClientReady(func(ctx context.Context, client bot.Client) {
		hooks.RegisterDaemon("REMINDER", hooks.LogInfo, func(ctx context.Context) (bool, func(), func()) { return StartReminderScheduler(ctx, client) })
	})

	// Register reminder command
	hooks.RegisterCommand(discord.SlashCommandCreate{
		Name:        "reminder",
		Description: "Manage reminders",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Set a new reminder",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "The reminder message",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "when",
						Description: "When to remind (e.g., 'tomorrow', 'in 1 week', 'next friday at 3pm')",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "sendto",
						Description: "Where to send the reminder",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "This Channel (Default)", Value: "channel"},
							{Name: "Direct Message", Value: "dm"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "list",
				Description: "List and dismiss reminders",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "dismiss",
						Description:  "Select a reminder to dismiss",
						Required:     false,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View a summary of your active reminders",
			},
		},
	}, handleReminder)

	// Register autocomplete handler
	hooks.RegisterAutocompleteHandler("reminder", handleReminderAutocomplete)

	hooks.RegisterAutocompleteHandler("app", handleBotAutocomplete)
	hooks.RegisterComponentHandler("console:", handleConsolePagination)
}

// end

func handleBot(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "reboot":
		handleBotReboot(event, data)
	case "shutdown":
		handleBotShutdown(event)
	case "stats":
		handleBotStats(event, data)
	case "status":
		handleBotStatus(event, data)
	case "console":
		handleBotConsole(event, data)
	case "cleanup":
		handleBotCleanup(event)
	case "send":
		handleBotSend(event, data)
	default:
		log.Printf(MsgBotUnknownSubcommand, subCmd)
	}
}

func handleBotReboot(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	build, _ := data.OptBool("build")
	hooks.LogWarn(MsgBotRebootCommanded, event.User().Username, event.User().ID)

	_ = hooks.RespondInteractionV2(*event.Client(), event, MsgBotRebooting, true)

	if build {
		_ = hooks.EditInteractionV2(*event.Client(), event, MsgBotRebootBuilding)

		exePath, err := os.Executable()
		if err != nil {
			exePath = hooks.GetProjectName()
		}

		cmd := exec.Command("go", "build", "-o", exePath, ".")
		output, err := cmd.CombinedOutput()
		if err != nil {
			_ = hooks.EditInteractionV2(*event.Client(), event, fmt.Sprintf(MsgBotRebootBuildFail, string(output)))
			return
		}

		_ = hooks.EditInteractionV2(*event.Client(), event, MsgBotRebootBuildSuccess+"\n"+MsgBotRebooting)
	}

	hooks.RequestRestart()
	time.AfterFunc(1500*time.Millisecond, func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	})
}

func handleBotShutdown(event *events.ApplicationCommandInteractionCreate) {
	hooks.LogWarn(MsgBotShutdownCommanded, event.User().Username, event.User().ID)
	_ = hooks.RespondInteractionV2(*event.Client(), event, MsgBotShuttingDown, true)
	time.AfterFunc(1*time.Second, func() {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	})
}

func handleBotAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	input := data.String("select")

	var (
		choices []discord.AutocompleteChoice
	)
	for _, key := range statusKeys {
		name := key
		if gen, ok := statusMap[key]; ok {
			dynamicVal := gen(hooks.AppContext, *event.Client())
			if dynamicVal != "" {
				name = dynamicVal
			}
		}

		if input == "" || strings.Contains(strings.ToLower(name), strings.ToLower(input)) || strings.Contains(strings.ToLower(key), strings.ToLower(input)) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  name,
				Value: key,
			})
		}
		if len(choices) >= 25 {
			break
		}
	}

	_ = event.AutocompleteResult(choices)
}

func handleBotCleanup(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		guildID := event.GuildID()
		if guildID == nil {
			_ = hooks.RespondInteractionV2(*event.Client(), event, MsgBotServerOnly, true)
			return
		}
		return
	}

	_, err := event.Client().Rest.SetGuildCommands(event.ApplicationID(), *guildID, []discord.ApplicationCommandCreate{})
	if err != nil {
		_ = hooks.RespondInteractionV2(*event.Client(), event, fmt.Sprintf(MsgBotClearCommandsFail, err), true)
		return
	}

	_ = hooks.RespondInteractionV2(*event.Client(), event, MsgBotClearCommandsSuccess, true)
}

func handleBotSend(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	stickerIDStr := data.String("sticker_id")
	stickerID, err := snowflake.Parse(stickerIDStr)
	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreate().
			WithContent(MsgBotStickerIDInvalid).
			WithEphemeral(true))
		return
	}

	err = event.DeferCreateMessage(true)
	if err != nil {
		return
	}

	client := event.Client()
	channelID := event.Channel().ID()

	_, err = client.Rest.CreateMessage(channelID, discord.MessageCreate{
		StickerIDs: []snowflake.ID{stickerID},
	})

	if err != nil {
		_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.MessageUpdate{
			Content: strPtr(fmt.Sprintf(MsgBotSendStickerFail, err)),
		})
		return
	}

	_, _ = client.Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.MessageUpdate{
		Content: strPtr(MsgBotSendStickerSuccess),
	})
}

// utils start

func Atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func strPtr(s string) *string {
	return &s
}

func safeGo(f func()) {
	go func() {
		defer func() { recover() }()
		f()
	}()
}

func ColorizeHex(colorInt int) string {
	hex := fmt.Sprintf("#%06X", colorInt)
	r := (colorInt >> 16) & 0xFF
	g := (colorInt >> 8) & 0xFF
	b := colorInt & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm⬤ %s\x1b[0m", r, g, b, hex)
}

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// utils end

// --- LIVEROLE UTILS ---

func StartRoleColorRotator(ctx context.Context, client bot.Client) (bool, func(), func()) {
	// Load all configured guilds
	configs, err := hooks.GetAllGuildRandomColorConfigs(ctx)
	if err != nil {
		hooks.LogInfo(MsgRoleColorFailedToFetchConfigs, err)
		return false, nil, nil
	}

	if len(configs) == 0 {
		return false, nil, nil
	}

	return true, func() {
		for gID, rID := range configs {
			state := &roleState{
				guildID: gID,
				roleID:  rID,
				active:  true,
			}
			roleStates.Store(gID, state)

			// Start rotation for this guild
			ScheduleNextUpdate(ctx, client, gID, rID)
		}
	}, func() { ShutdownRoleColorRotator(ctx) }
}

func ShutdownRoleColorRotator(ctx context.Context) {
	hooks.LogInfo("Shutting down Role Color Rotator...")
	rotatorTimers.Range(func(key, value any) bool {
		if timer, ok := value.(*time.Timer); ok {
			timer.Stop()
		}
		rotatorTimers.Delete(key)
		return true
	})
}

func StartRotationForGuild(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) {
	// Stop existing if any
	StopRotationForGuild(guildID)

	state := &roleState{
		guildID: guildID,
		roleID:  roleID,
		active:  true,
	}
	roleStates.Store(guildID, state)

	ScheduleNextUpdate(ctx, client, guildID, roleID)
}

func StopRotationForGuild(guildID snowflake.ID) {
	if val, ok := roleStates.Load(guildID); ok {
		state := val.(*roleState)
		state.Lock()
		state.active = false
		state.Unlock()
	}

	if val, ok := rotatorTimers.Load(guildID); ok {
		if timer, ok := val.(*time.Timer); ok {
			timer.Stop()
		}
		rotatorTimers.Delete(guildID)
	}
	nextUpdateMap.Delete(guildID)
	roleStates.Delete(guildID)
}

func ScheduleNextUpdate(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) {
	if ctx.Err() != nil {
		return
	}

	// Verify we are still active
	val, ok := roleStates.Load(guildID)
	if !ok {
		return
	}
	state := val.(*roleState)
	state.RLock()
	if !state.active {
		state.RUnlock()
		return
	}
	state.RUnlock()

	minutes := hooks.RandomIntRange(minMinutes, maxMinutes)
	duration := time.Duration(minutes) * time.Minute

	nextUpdate := time.Now().UTC().Add(duration)
	nextUpdateMap.Store(guildID, nextUpdate)

	state.Lock()
	if state.lastColor == "" {
		if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
			state.lastColor = fmt.Sprintf("#%06X", role.Color)
		}
	}
	state.Unlock()

	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	}
	hooks.LogInfo(MsgRoleColorNextUpdate, guildLabel, minutes)

	timer := time.AfterFunc(duration, func() {
		state.RLock()
		active := state.active
		state.RUnlock()
		if !active {
			return
		}

		_ = UpdateRoleColor(ctx, client, guildID, roleID)
		ScheduleNextUpdate(ctx, client, guildID, roleID)
	})

	rotatorTimers.Store(guildID, timer)
}

func UpdateRoleColor(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) error {
	var newColor int
	var lastHex string

	val, ok := roleStates.Load(guildID)
	if !ok {
		return fmt.Errorf("rotation not active")
	}
	state := val.(*roleState)

	state.RLock()
	lastHex = state.lastColor
	active := state.active
	state.RUnlock()

	if !active {
		return fmt.Errorf("rotation not active")
	}

	for range 10 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			newColor = hooks.RandomIntRange(1, 16777214)
		} else {
			newColor = int(binary.BigEndian.Uint32(b[:]) & 0xFFFFFF)
		}

		if newColor == 0 {
			continue
		}

		hexColor := fmt.Sprintf("#%06X", newColor)
		if lastHex == "" || hexColor != lastHex {
			break
		}
	}

	if newColor == 0 {
		newColor = 0xE91E63
	}

	_, err := client.Rest.UpdateRole(guildID, roleID, discord.RoleUpdate{
		Color: &newColor,
	})

	roleLabel := roleID.String()
	if role, ok := client.Caches.Role(guildID, roleID); ok {
		roleLabel = fmt.Sprintf("%s (%s)", role.Name, roleID)
	}

	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	}

	if err != nil {
		hooks.LogInfo(MsgRoleColorUpdateFail, roleLabel, guildLabel, err)
		return err
	}

	hexColor := hooks.ColorizeHex(newColor)
	hooks.LogInfo(MsgRoleColorUpdated, roleLabel, guildLabel, hexColor)

	state.Lock()
	state.lastColor = fmt.Sprintf("#%06X", newColor)
	state.Unlock()
	return nil
}

func GetNextUpdate(ctx context.Context) (time.Time, snowflake.ID, bool) {
	var nearest time.Time
	var nearestGuild snowflake.ID
	found := false

	nextUpdateMap.Range(func(key, value any) bool {
		t := value.(time.Time)
		guildID := key.(snowflake.ID)
		if !found || t.Before(nearest) {
			nearest = t
			nearestGuild = guildID
			found = true
		}
		return true
	})

	return nearest, nearestGuild, found
}

func GetCurrentColor(ctx context.Context, client bot.Client, guildID snowflake.ID) string {
	val, ok, _ := GetCurrentColorInt(ctx, client, guildID)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%06X", val)
}

func GetCurrentColorInt(ctx context.Context, client bot.Client, guildID snowflake.ID) (int, bool, bool) {
	val, ok := roleStates.Load(guildID)
	if !ok {
		return 0, false, false
	}
	state := val.(*roleState)

	if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
		return role.Color, true, true
	}

	state.RLock()
	defer state.RUnlock()
	if state.lastColor != "" {
		var colorInt int
		fmt.Sscanf(state.lastColor, "#%X", &colorInt)
		return colorInt, true, false
	}

	return 0, false, false
}

// --- REMINDER UTILS ---

func initReminderParser() {
	var err error
	reminderParser, err = naturaltime.New()
	if err != nil {
		hooks.LogError(MsgReminderNaturalTimeInitFail, err)
	}
}

func parseNaturalTime(input string) (time.Time, error) {
	now := time.Now().UTC()

	result, err := reminderParser.ParseDate(input, now)
	if err == nil && result != nil {
		return *result, nil
	}

	if d, err := time.ParseDuration(input); err == nil {
		return now.Add(d), nil
	}

	return time.Time{}, fmt.Errorf("could not parse time: %s", input)
}

func formatReminderRelativeTime(from, to time.Time) string {
	duration := to.Sub(from)

	if duration < time.Minute {
		return MsgReminderRelLessMinute
	}

	if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return MsgReminderRelMinute
		}
		return fmt.Sprintf(MsgReminderRelMinutes, minutes)
	}

	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return MsgReminderRelHour
		}
		return fmt.Sprintf(MsgReminderRelHours, hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return MsgReminderRelDay
	}
	if days < 7 {
		return fmt.Sprintf(MsgReminderRelDays, days)
	}

	weeks := days / 7
	if weeks == 1 {
		return MsgReminderRelWeek
	}
	if weeks < 4 {
		return fmt.Sprintf(MsgReminderRelWeeks, weeks)
	}

	months := days / 30
	if months == 1 {
		return MsgReminderRelMonth
	}
	if months < 12 {
		return fmt.Sprintf(MsgReminderRelMonths, months)
	}

	years := days / 365
	if years == 1 {
		return MsgReminderRelYear
	}
	return fmt.Sprintf(MsgReminderRelYears, years)
}

func StartReminderScheduler(ctx context.Context, client bot.Client) (bool, func(), func()) {
	if !atomic.CompareAndSwapInt32(&reminderSchedulerRunning, 0, 1) {
		return false, nil, nil
	}

	checkAndSendReminders(ctx, client)

	return true, func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					checkAndSendReminders(ctx, client)
				case <-ctx.Done():
					return
				}
			}
		}, func() {
			hooks.LogInfo("Shutting down Reminder System...")
		}
}

func checkAndSendReminders(parentCtx context.Context, client bot.Client) {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	// Atomically fetch and delete due reminders to prevent race conditions
	reminders, err := hooks.ClaimDueReminders(ctx)
	if err != nil {
		hooks.LogInfo(MsgReminderFailedToQueryDue, err)
		return
	}

	for _, r := range reminders {
		// Send reminder
		safeGo(func() { sendReminder(parentCtx, client, r) })
	}
}

func sendReminder(parentCtx context.Context, client bot.Client, r *Reminder) {
	channelID := r.ChannelID
	userID := r.UserID

	if channelID == 0 || userID == 0 {
		hooks.LogInfo("Invalid IDs for reminder %d. Skipping.", r.ID)
		return
	}

	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", userID, r.Message)
	targetChannelID := channelID

	if r.SendTo == "dm" {
		dmChannel, dmErr := client.Rest.CreateDMChannel(userID, rest.WithCtx(parentCtx))
		if dmErr != nil {
			hooks.LogInfo(MsgReminderFailedToCreateDM, userID, dmErr)
		} else {
			targetChannelID = dmChannel.ID()
		}
	}

	_, err := hooks.SendComponentsV2(client, targetChannelID, []any{hooks.NewV2Container(hooks.NewTextDisplay(reminderText))}, nil, nil, nil)

	if err != nil {
		hooks.LogInfo(MsgReminderFailedToSend, r.ID, err)
		return
	}

	hooks.LogInfo(MsgReminderSentAndDeleted, r.ID, userID)
}
