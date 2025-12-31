package home

import (
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

// Eightball command shared utilities
const eightballHttpClientTimeout = 10 * time.Second

var eightballHttpClient = &http.Client{
	Timeout: eightballHttpClientTimeout,
}

func eightballRespondErrorSync(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	if s == nil || i == nil || i.Interaction == nil {
		sys.LogEightball(sys.MsgEightballCannotSendError)
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
		sys.LogEightball(sys.MsgEightballFailedToSendError, err)
	}
}

func init() {
	sys.RegisterCommand(&discordgo.ApplicationCommand{
		Name:        "8ball",
		Description: "Eightball related commands",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "fortune",
				Description: "Get a random fortune",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "question",
						Description: "The question you want to ask the eightball",
						Required:    false,
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
		case "fortune":
			handleEightballFortune(s, i, options[0].Options)
		}
	})
}
