package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

// Cat API Types
type CatFact struct {
	Fact   string `json:"fact"`
	Length int    `json:"length"`
}

type CatImage struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Cat ANSI Art Constants
const (
	catAsciiWidth = 13
	catAnsiReset  = "\u001b[0m"
)

var catAnsiColors = map[string]string{
	"gray":   "30",
	"red":    "31",
	"green":  "32",
	"yellow": "33",
	"blue":   "34",
	"pink":   "35",
	"cyan":   "36",
	"white":  "37",
}

func getCatAnsiCode(color string) string {
	if code, ok := catAnsiColors[color]; ok {
		return "\u001b[0;" + code + "m"
	}
	return ""
}

func getCatColorChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Gray", Value: "gray"},
		{Name: "Red", Value: "red"},
		{Name: "Green", Value: "green"},
		{Name: "Yellow", Value: "yellow"},
		{Name: "Blue", Value: "blue"},
		{Name: "Pink", Value: "pink"},
		{Name: "Cyan", Value: "cyan"},
		{Name: "White", Value: "white"},
	}
}

func getCatExpressionChoices() []discord.ApplicationCommandOptionChoiceString {
	return []discord.ApplicationCommandOptionChoiceString{
		{Name: "Neutral", Value: "o.o"},
		{Name: "Shocked", Value: "O.O"},
		{Name: "Happy", Value: "^.^"},
		{Name: "Sleeping", Value: "-.-"},
		{Name: "Confused", Value: "o.O"},
		{Name: "Silly", Value: ">.<"},
		{Name: "Wink", Value: "o.~"},
		{Name: "Dizzy", Value: "@.@"},
		{Name: "Crying", Value: "T.T"},
		{Name: "Angry", Value: "ò.ó"},
		{Name: "Star Eyes", Value: "*.*"},
		{Name: "Money", Value: "$.$"},
		{Name: "None", Value: "   "},
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
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View cat system status and details",
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		subCmd := data.SubCommandName
		if subCmd == nil {
			return
		}

		switch *subCmd {
		case "stats":
			handleCatStats(event)
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
