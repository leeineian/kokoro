package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "play",
		Description: "Good games.",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "connect4",
				Description: "Play Connect Four against another player or AI",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionUser{
						Name:        "opponent",
						Description: "Challenge another user (leave empty to play against AI)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "difficulty",
						Description: "AI difficulty level (only for AI games)",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Easy", Value: "easy"},
							{Name: "Medium", Value: "medium"},
							{Name: "Hard", Value: "hard"},
							{Name: "Impossible", Value: "impossible"},
						},
					},
					discord.ApplicationCommandOptionInt{
						Name:        "timer",
						Description: "Turn timer in seconds (leave empty or 0 to disable)",
						Required:    false,
					},
					discord.ApplicationCommandOptionString{
						Name:        "size",
						Description: "Board size",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Small (5x4)", Value: "small"},
							{Name: "Classic (7x6)", Value: "classic"},
							{Name: "Large (9x8)", Value: "large"},
							{Name: "Master (10x10)", Value: "master"},
						},
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
		case "connect4":
			handlePlayConnect4(event, data)
		}
	})
}
