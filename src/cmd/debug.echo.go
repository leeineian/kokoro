package cmd

import (
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/src/sys"
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

	container := sys.NewV2Container(sys.NewTextDisplay(msg))
	if err := sys.RespondInteractionV2(s, i.Interaction, container, ephemeral); err != nil {
		log.Printf("[DEBUG] Failed to respond to echo: %v", err)
	}
}
