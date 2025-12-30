package home

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleDebugStatus(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(options) == 0 {
		return
	}

	visible := options[0].BoolValue()
	valStr := fmt.Sprintf("%t", visible)

	err := sys.SetBotConfig("status_visible", valStr)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Components: []discordgo.MessageComponent{
					&discordgo.Container{
						Components: []discordgo.MessageComponent{
							&discordgo.TextDisplay{Content: fmt.Sprintf("Error saving config: %v", err)},
						},
					},
				},
				Flags: discordgo.MessageFlagsIsComponentsV2 | discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Force update
	proc.TriggerStatusUpdate(s)

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Status visibility set to: **%v**", visible),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		sys.LogError(sys.MsgDebugStatusCmdFail, err)
	}
}
