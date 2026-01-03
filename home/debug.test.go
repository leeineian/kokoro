package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleTestError(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	key := data.String("key")

	errors := sys.GetUserErrors()
	var content string
	if msg, ok := errors[key]; ok {
		content = fmt.Sprintf("**%s**\n\n%s", key, msg)
	} else {
		content = fmt.Sprintf("❌ Error constant `%s` not found.", key)
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(true).
		Build())
}
