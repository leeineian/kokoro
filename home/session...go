package home

import (
	"log"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/leeineian/minder/sys"
)

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "session",
		Description:              "Session management utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "reboot",
				Description: "Restart the bot process",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "shutdown",
				Description: "Shut down the bot process",
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
				Description: "Configure bot status visibility",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "visible",
						Description: "Enable or disable status rotation",
						Required:    true,
					},
				},
			},
		},
	}, handleSession)
}

func handleSession(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "reboot":
		handleSessionReboot(event)
	case "shutdown":
		handleSessionShutdown(event)
	case "stats":
		handleSessionStats(event)
	case "status":
		handleSessionStatus(event)
	default:
		log.Printf("Unknown session subcommand: %s", subCmd)
	}
}
