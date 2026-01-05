package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopSet configures a channel for looping
func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelID := data.Snowflake("channel")

	channel, ok := event.Client().Caches.Channel(channelID)
	if !ok {
		loopRespond(event, "❌ Failed to fetch channel.", true)
		return
	}

	// Determine channel type
	channelType := "channel"
	if channel.Type() == discord.ChannelTypeGuildCategory {
		channelType = "category"
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
		ChannelType:   channelType,
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

	typeStr := "Channel"
	if channelType == "category" {
		typeStr = "Category"
	}

	loopRespond(event, fmt.Sprintf(
		"✅ **%s Configured**\n> **%s**\n> Duration: ∞ (Random)\n> Run `/loop start` to begin.",
		typeStr, channel.Name(),
	), true)
}
