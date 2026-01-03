package home

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/sys"
)

// Debug command shared utilities
const (
	DebugAnsiReset    = "\u001b[0m"
	DebugAnsiPink     = "\u001b[35m"
	DebugAnsiPinkBold = "\u001b[35;1m"
	DebugCacheTTL     = 5000 * time.Millisecond
)

var (
	debugStartTime = time.Now()

	// Cache
	debugCacheMu    sync.RWMutex
	debugStatsCache DebugStatsCache
)

type DebugStatsCache struct {
	System  DebugCachedData
	Metrics DebugCachedMetrics
}

type DebugCachedData struct {
	Data      string
	Timestamp time.Time
}

type DebugCachedMetrics struct {
	Data          DebugHealthMetrics
	Timestamp     time.Time
	InteractionID string
}

type DebugHealthMetrics struct {
	Ping        int64
	GatewayPing int64
	DBLatency   string
}

func debugTitle(text string) string {
	return fmt.Sprintf("%s%s%s", DebugAnsiPink, text, DebugAnsiReset)
}

func debugKey(text string) string {
	return fmt.Sprintf("%s> %s:%s", DebugAnsiPink, text, DebugAnsiReset)
}

func debugVal(text string) string {
	return fmt.Sprintf("%s%s%s", DebugAnsiPinkBold, text, DebugAnsiReset)
}

func debugTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func handleDebugAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data

	var choices []discord.AutocompleteChoice

	// Find focused option and detect subcommand context
	var focusedName string
	var focusedValue string
	var subCommand string

	if data.SubCommandName != nil {
		subCommand = *data.SubCommandName
	}

	for _, opt := range data.Options {
		if opt.Focused {
			focusedName = opt.Name
			if opt.Value != nil {
				focusedValue = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	switch focusedName {
	case "key":
		// Autocomplete for error constants
		errors := sys.GetUserErrors()
		keys := make([]string, 0, len(errors))
		for k := range errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if focusedValue == "" || strings.Contains(strings.ToLower(k), strings.ToLower(focusedValue)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  debugTruncate(k+": "+errors[k], 100),
					Value: k,
				})
				if len(choices) >= 25 {
					break
				}
			}
		}

	case "target":
		// Use the advanced loop autocomplete with status indicators
		debugWebhookLooperAutocomplete(event, subCommand, focusedValue)
		return
	}

	event.AutocompleteResult(choices)
}

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "debug",
		Description:              "Debug and Stress Testing Utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:        "stats",
				Description: "Debug statistics",
				Options: []discord.ApplicationCommandOptionSubCommand{
					{
						Name:        "summary",
						Description: "Display detailed system and application statistics",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionBool{
								Name:        "ephemeral",
								Description: "Whether the message should be ephemeral (default: true)",
								Required:    false,
							},
						},
					},
					{
						Name:        "ping",
						Description: "Check bot latency",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionBool{
								Name:        "ephemeral",
								Description: "Whether the message should be ephemeral (default: true)",
								Required:    false,
							},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "echo",
				Description: "Echo a message back to you",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "Message to echo",
						Required:    true,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:        "rolecolor",
				Description: "Random Role Color Utilities",
				Options: []discord.ApplicationCommandOptionSubCommand{
					{
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
					{
						Name:        "reset",
						Description: "Reset configuration",
					},
					{
						Name:        "refresh",
						Description: "Force an immediate color change",
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "status",
				Description: "Configure bot status visibility",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "visible",
						Description: "Enable or disable status rotation",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommandGroup{
				Name:        "loop",
				Description: "Webhook stress testing and looping utilities",
				Options: []discord.ApplicationCommandOptionSubCommand{
					{
						Name:        "list",
						Description: "List configured loop channels",
					},
					{
						Name:        "set",
						Description: "Configure a channel or category for looping",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionChannel{
								Name:        "channel",
								Description: "Text channel or category to configure",
								Required:    true,
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
						},
					},
					{
						Name:        "start",
						Description: "Start webhook loop(s)",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionString{
								Name:         "target",
								Description:  "Target to start (all or specific channel)",
								Required:     false,
								Autocomplete: true,
							},
							discord.ApplicationCommandOptionString{
								Name:        "interval",
								Description: "Duration to run (e.g., 30s, 5m, 1h). Leave empty for random mode.",
								Required:    false,
							},
						},
					},
					{
						Name:        "stop",
						Description: "Stop webhook loop(s)",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionString{
								Name:         "target",
								Description:  "Target to stop (all or specific channel)",
								Required:     false,
								Autocomplete: true,
							},
						},
					},
					{
						Name:        "purge",
						Description: "Purge all webhooks from a category",
						Options: []discord.ApplicationCommandOption{
							discord.ApplicationCommandOptionChannel{
								Name:         "category",
								Description:  "Category to purge webhooks from",
								Required:     true,
								ChannelTypes: []discord.ChannelType{discord.ChannelTypeGuildCategory},
							},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "test-error",
				Description: "Preview user-facing error constants",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "key",
						Description:  "The error constant to preview",
						Required:     true,
						Autocomplete: true,
					},
				},
			},
		},
	}, handleDebugCommand)

	sys.RegisterComponentHandler("ping_refresh", handleDebugPingRefresh)
	sys.RegisterComponentHandler("delete_loop_config", handleDebugLoopConfigDelete)
	sys.RegisterComponentHandler("start_loop_select", handleDebugStartLoopSelect)
	sys.RegisterComponentHandler("stop_loop_select", handleDebugStopLoopSelect)
	sys.RegisterAutocompleteHandler("debug", handleDebugAutocomplete)
}

func handleDebugCommand(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()

	// Check for subcommand group first
	if data.SubCommandGroupName != nil {
		group := *data.SubCommandGroupName
		switch group {
		case "stats":
			if data.SubCommandName != nil {
				switch *data.SubCommandName {
				case "summary":
					handleDebugStats(event, data)
				case "ping":
					handleDebugPing(event, data)
				}
			}
		case "rolecolor":
			if data.SubCommandName != nil {
				handleDebugRoleColor(event, data, *data.SubCommandName)
			}
		case "loop":
			if data.SubCommandName != nil {
				handleDebugWebhookLooper(event, data, *data.SubCommandName)
			}
		}
		return
	}

	// Handle regular subcommands
	if data.SubCommandName == nil {
		return
	}
	subCmd := *data.SubCommandName

	switch subCmd {
	case "echo":
		handleDebugEcho(event, data)
	case "status":
		handleDebugStatus(event, data)
	case "test-error":
		handleTestError(event, data)
	default:
		log.Printf("Unknown debug subcommand: %s", subCmd)
	}
}
