package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopSet configures a channel for looping
func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelIDStr, _ := data.OptString("category")
	channelID, err := snowflake.Parse(channelIDStr)
	if err != nil {
		loopRespond(event, "❌ Invalid channel selection.", true)
		return
	}

	// Always defer to be safe
	_ = event.DeferCreateMessage(true)

	go func() {
		channel, ok := event.Client().Caches.Channel(channelID)
		if !ok {
			loopRespond(event, "❌ Failed to fetch channel.", true)
			return
		}

		// Validate that channel is a category
		if channel.Type() != discord.ChannelTypeGuildCategory {
			loopRespond(event, "❌ Only **categories** are supported. Please select a category channel.", true)
			return
		}

		message := "@everyone"
		if msg, ok := data.OptString("message"); ok {
			message = msg
		}

		webhookAuthor := "LoopHook"
		if author, ok := data.OptString("webhook_author"); ok {
			webhookAuthor = author
		}

		webhookAvatar := ""
		if avatar, ok := data.OptString("webhook_avatar"); ok {
			webhookAvatar = avatar
		}

		config := &sys.LoopConfig{
			ChannelID:     channelID,
			ChannelName:   channel.Name(),
			ChannelType:   "category",
			Rounds:        0,
			Interval:      0, // Default to infinite random mode
			Message:       message,
			WebhookAuthor: webhookAuthor,
			WebhookAvatar: webhookAvatar,
			UseThread:     false,
		}

		if err := proc.SetLoopConfig(sys.AppContext, event.Client(), channelID, config); err != nil {
			loopRespond(event, fmt.Sprintf("❌ Failed to save configuration: %v", err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(
			"✅ **Category Configured**\n> **%s**\n> Duration: ∞ (Random)\n> Run `/loop start` to begin.",
			channel.Name(),
		), true)
	}()
}
