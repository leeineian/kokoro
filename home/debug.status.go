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
	valStr := "false"
	if visible {
		valStr = "true"
	}

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

	statusStr := "DISABLED"
	if visible {
		statusStr = "ENABLED"
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: fmt.Sprintf("Status rotation has been **%s**.", statusStr)},
					},
				},
			},
			Flags: discordgo.MessageFlagsIsComponentsV2,
		},
	})
	if err != nil {
		sys.LogError("Failed to respond to status command: %v", err)
	}
}
