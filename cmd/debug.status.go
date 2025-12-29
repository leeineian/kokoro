package cmd

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
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay(fmt.Sprintf("Error saving config: %v", err)),
		), true)
		return
	}

	// Force update
	proc.TriggerStatusUpdate(s)

	statusStr := "DISABLED"
	if visible {
		statusStr = "ENABLED"
	}

	sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
		sys.NewTextDisplay(fmt.Sprintf("Status rotation has been **%s**.", statusStr)),
	), false)
}
