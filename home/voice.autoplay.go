package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
)

func handleMusicAutoplay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	state, _ := data.OptString("state")
	enabled := state == "enable"

	var voiceState discord.VoiceState
	var ok bool
	if event.Member() != nil {
		voiceState, ok = event.Client().Caches.VoiceState(*event.GuildID(), event.User().ID)
	}

	if !ok || voiceState.ChannelID == nil {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().
			SetContent("You must be in a voice channel to configure autoplay.").
			SetEphemeral(true).
			Build())
		return
	}

	vm := proc.GetVoiceManager()
	sess := vm.Prepare(event.Client(), *event.GuildID(), *voiceState.ChannelID)

	sess.Autoplay = enabled

	status := "disabled"
	if enabled {
		status = "enabled"
	}

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetContent("📻 Autoplay has been **" + status + "**.").
		Build())
}
