package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

// Cat command shared utilities
func getCatColorChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "White", Value: "ffffff"},
		{Name: "Black", Value: "000000"},
		{Name: "Red", Value: "ff0000"},
		{Name: "Orange", Value: "ff8000"},
		{Name: "Yellow", Value: "ffff00"},
		{Name: "Green", Value: "00ff00"},
		{Name: "Cyan", Value: "00ffff"},
		{Name: "Blue", Value: "0000ff"},
		{Name: "Purple", Value: "8000ff"},
		{Name: "Pink", Value: "ff00ff"},
	}
}

func getCatExpressionChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Happy", Value: "happy"},
		{Name: "Sad", Value: "sad"},
		{Name: "Angry", Value: "angry"},
		{Name: "Surprised", Value: "surprised"},
		{Name: "Dead", Value: "dead"},
		{Name: "Winking", Value: "winking"},
	}
}

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
