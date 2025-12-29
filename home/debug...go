package home

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
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

func debugStrPtr(s string) *string {
	return &s
}

func init() {
	sys.RegisterCommand(&discordgo.ApplicationCommand{
		Name:                     "debug",
		Description:              "Debug and Stress Testing Utilities (Admin Only)",
		DMPermission:             new(bool), // false
		DefaultMemberPermissions: func() *int64 { i := int64(discordgo.PermissionAdministrator); return &i }(),
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Name:        "stats",
				Description: "Debug statistics",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "summary",
						Description: "Display detailed system and application statistics",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionBoolean,
								Name:        "ephemeral",
								Description: "Whether the message should be ephemeral (default: true)",
								Required:    false,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "ping",
						Description: "Check bot latency",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionBoolean,
								Name:        "ephemeral",
								Description: "Whether the message should be ephemeral (default: true)",
								Required:    false,
							},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "echo",
				Description: "Echo a message back to you",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message",
						Description: "Message to echo",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Name:        "rolecolor",
				Description: "Random Role Color Utilities",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "set",
						Description: "Set the role to randomly color",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionRole,
								Name:        "role",
								Description: "The role to color",
								Required:    true,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "reset",
						Description: "Reset configuration",
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "refresh",
						Description: "Force an immediate color change",
					},
				},
			},
			{
				Name:        "status",
				Description: "Configure bot status visibility",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Options: []*discordgo.ApplicationCommandOption{
					{
						Name:        "visible",
						Description: "Enable or disable status rotation",
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
				Name:        "loop",
				Description: "Webhook stress testing and looping utilities",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "list",
						Description: "List configured loop channels",
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "set",
						Description: "Configure a channel or category for looping",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionChannel,
								Name:        "channel",
								Description: "Text channel or category to configure",
								Required:    true,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "active_name",
								Description: "Name when loop is active",
								Required:    false,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "inactive_name",
								Description: "Name when loop is inactive",
								Required:    false,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "message",
								Description: "Message to send (default: @everyone)",
								Required:    false,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "webhook_author",
								Description: "Webhook display name (default: LoopHook)",
								Required:    false,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "webhook_avatar",
								Description: "Webhook avatar URL",
								Required:    false,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "start",
						Description: "Start webhook loop(s)",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:         discordgo.ApplicationCommandOptionString,
								Name:         "target",
								Description:  "Target to start (all or specific channel)",
								Required:     false,
								Autocomplete: true,
							},
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "interval",
								Description: "Duration to run (e.g., 30s, 5m, 1h). Leave empty for random mode.",
								Required:    false,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "stop",
						Description: "Stop webhook loop(s)",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:         discordgo.ApplicationCommandOptionString,
								Name:         "target",
								Description:  "Target to stop (all or specific channel)",
								Required:     false,
								Autocomplete: true,
							},
						},
					},
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "purge",
						Description: "Purge all webhooks from a category",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionChannel,
								Name:        "category",
								Description: "Category to purge webhooks from",
								Required:    true,
								ChannelTypes: []discordgo.ChannelType{
									discordgo.ChannelTypeGuildCategory,
								},
							},
						},
					},
				},
			},
		},
	}, handleDebugCommand)

	sys.RegisterComponentHandler("ping_refresh", handleDebugPingRefresh)
	sys.RegisterComponentHandler("delete_loop_config", handleDebugLoopConfigDelete)
	sys.RegisterComponentHandler("stop_loop_select", handleDebugStopLoopSelect)
	sys.RegisterAutocompleteHandler("debug", debugWebhookLooperAutocomplete)
}

func handleDebugCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		return
	}

	switch options[0].Name {
	case "stats":
		subCommandGroup := options[0]
		if len(subCommandGroup.Options) > 0 {
			switch subCommandGroup.Options[0].Name {
			case "summary":
				handleDebugStats(s, i, subCommandGroup.Options[0].Options)
			case "ping":
				handleDebugPing(s, i, subCommandGroup.Options[0].Options)
			}
		}
	case "echo":
		handleDebugEcho(s, i, options[0].Options)
	case "rolecolor":
		handleDebugRoleColor(s, i, options[0].Options)
	case "status":
		handleDebugStatus(s, i, options[0].Options)
	case "loop":
		handleDebugWebhookLooper(s, i, options[0].Options)
	default:
		log.Printf("Unknown debug subcommand: %s", options[0].Name)
	}
}
