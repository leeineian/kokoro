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
}

func handleRoleColor(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "stats":
		handleRoleColorStats(event)
	case "set":
		handleRoleColorSet(event, data)
	case "reset":
		handleRoleColorReset(event)
	case "refresh":
		handleRoleColorRefresh(event)
	default:
		log.Printf("Unknown rolecolor subcommand: %s", subCmd)
	}
}
