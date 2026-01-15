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
						Name:         "queue",
						Description:  "Playback mode (now, next, or a number)",
						Required:     false,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "autoplay",
						Description: "Enable or disable autoplay after this song",
						Required:    false,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "loop",
						Description: "Loop the playback",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stop",
				Description: "Stop audio and leave",
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
		}
	})

	sys.RegisterAutocompleteHandler("voice", handleMusicAutocomplete)
}
