package home

import (
	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleDebugEcho(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	msg := ""
	ephemeral := true // Default to true

	if len(options) > 0 {
		for _, opt := range options {
			switch opt.Name {
			case "message":
				msg = opt.StringValue()
			case "ephemeral":
				ephemeral = opt.BoolValue()
			}
		}
	}

	var flags discordgo.MessageFlags = discordgo.MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}

	data := &discordgo.InteractionResponseData{
		Components: []discordgo.MessageComponent{
			&discordgo.Container{
				Components: []discordgo.MessageComponent{
					&discordgo.TextDisplay{Content: msg},
				},
			},
		},
		Flags: flags,
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
	if err != nil {
		sys.LogDebug("Failed to respond to echo: %v", err)
	}
}
