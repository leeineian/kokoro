package home

import (
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

// Cat command shared utilities
const catHttpClientTimeout = 10 * time.Second

var catHttpClient = &http.Client{
	Timeout: catHttpClientTimeout,
}

func catRespondErrorSync(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if s == nil || i == nil || i.Interaction == nil {
		sys.LogCat(sys.MsgCatCannotSendErrorResponse)
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: msg},
					},
				},
			},
		},
	})
	if err != nil {
		sys.LogCat(sys.MsgCatFailedToSendErrorResponse, err)
	}
}

func catRespondErrorFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if s == nil || i == nil || i.Interaction == nil {
		sys.LogCat(sys.MsgCatCannotSendErrorFollowup)
		return
	}

	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Flags: discordgo.MessageFlagsIsComponentsV2 | discordgo.MessageFlagsEphemeral,
		Components: []discordgo.MessageComponent{
			&discordgo.Container{
				Components: []discordgo.MessageComponent{
					&discordgo.TextDisplay{Content: msg},
				},
			},
		},
	}); err != nil {
		sys.LogCat(sys.MsgCatFailedToSendErrorFollowup, err)
	}
}

func init() {
	sys.RegisterCommand(&discordgo.ApplicationCommand{
		Name:        "cat",
		Description: "Cat related commands",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "fact",
				Description: "Get a random cat fact",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "image",
				Description: "Get a random cat image",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "say",
				Description: "Cowsay but cat",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message",
						Description: "The message for the cat to say",
						Required:    true,
						MaxLength:   2000,
						MinLength:   func() *int { i := 1; return &i }(),
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "msgcolor",
						Description: "Color of the message text",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "bubcolor",
						Description: "Color of the speech bubble",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "catcolor",
						Description: "Color of the cat",
						Required:    false,
						Choices:     getCatColorChoices(),
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "expression",
						Description: "The cat's facial expression",
						Required:    false,
						Choices:     getCatExpressionChoices(),
					},
				},
			},
		},
	}, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if len(options) == 0 {
			return
		}

		subCommand := options[0].Name

		switch subCommand {
		case "fact":
			handleCatFact(s, i)
		case "image":
			handleCatImage(s, i)
		case "say":
			handleCatSay(s, i, options[0].Options)
		}
	})
}
