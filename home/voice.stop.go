package home

import (
	"context"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
)

func handleMusicStop(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	vm := proc.GetVoiceManager()

	vm.Leave(context.Background(), *event.GuildID())

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetContent("🛑 Stopped and disconnected.").
		Build())
}
