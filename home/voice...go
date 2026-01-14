package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func init() {
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "voice",
		Description: "Voice System",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "play",
				Description: "Play audio from a URL",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "query",
						Description:  "The URL or song name to play",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "queue",
						Description: "Playback mode",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Play Now", Value: "now"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stop",
				Description: "Stop audio and leave",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "autoplay",
				Description: "Enable or disable autoplay",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "state",
						Description: "Enable or disable autoplay",
						Required:    true,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "Enable", Value: "enable"},
							{Name: "Disable", Value: "disable"},
						},
					},
				},
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
		data := event.SlashCommandInteractionData()
		if data.SubCommandName == nil {
			return
		}

		switch *data.SubCommandName {
		case "play":
			handleMusicPlay(event, data)
		case "stop":
			handleMusicStop(event, data)
		case "autoplay":
			handleMusicAutoplay(event, data)
		}
	})

	sys.RegisterAutocompleteHandler("voice", handleMusicAutocomplete)
}
