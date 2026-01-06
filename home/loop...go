package home

import (
	"log"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "loop",
		Description:              "Webhook stress testing and looping utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "list",
				Description: "List configured loop channels",
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
		},
	}, handleLoop)

	sys.RegisterComponentHandler("delete_loop_config", handleDeleteLoopConfig)
	sys.RegisterAutocompleteHandler("loop", handleLoopAutocomplete)
}

func handleLoop(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "list":
		handleLoopList(event)
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

func loopRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	// Add some spacing/formatting to make it look cleaner
	var displayContent string
	if !strings.HasPrefix(content, "#") && !strings.HasPrefix(content, ">") {
		displayContent = "> " + content
	} else {
		displayContent = content
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(displayContent),
			),
		).
		SetEphemeral(ephemeral).
		Build())
}

func loopTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
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

	handleWebhookLooperAutocomplete(event, subCmd, focusedOpt)
}
