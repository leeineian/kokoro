package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "cat",
		Description: "Cat related commands",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "fact",
				Description: "Get a random cat fact",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "image",
				Description: "Get a random cat image",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "say",
				Description: "Cowsay but cat",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "The message for the cat to say",
						Required:    true,
						MaxLength:   intPtr(2000),
						MinLength:   intPtr(1),
					},
					discord.ApplicationCommandOptionString{
						Name:        "msgcolor",
						Description: "Color of the message text",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "bubcolor",
						Description: "Color of the speech bubble",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "catcolor",
						Description: "Color of the cat",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					discord.ApplicationCommandOptionString{
						Name:        "expression",
						Description: "The cat's facial expression",
						Required:    false,
						Choices:     getCatExpressionChoices(),
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
		case "fact":
			handleCatFact(event)
		case "image":
			handleCatImage(event)
		case "say":
			handleCatSay(event, data)
		}
	})
}

func intPtr(i int) *int {
	return &i
}
