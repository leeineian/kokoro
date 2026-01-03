package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

// Eightball command shared utilities
func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "8ball",
		Description: "Eightball related commands",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "fortune",
				Description: "Get a random fortune",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "question",
						Description: "The question you want to ask the eightball",
						Required:    false,
					},
				},
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		subCmd := data.SubCommandName
		if subCmd == nil {
			return
		}

		switch *subCmd {
		case "fortune":
			handle8BallFortune(event)
		}
	})
}
