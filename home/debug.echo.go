package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
)

func handleDebugEcho(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	message := data.String("message")
	ephemeral := false
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(message),
			),
		).
		SetEphemeral(ephemeral).
		Build())
}
