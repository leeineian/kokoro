package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleCatStats(event *events.ApplicationCommandInteractionCreate) {
	// For cat, there isn't a database config, so we display the system status and available APIs
	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sys.MsgCatSystemStatus),
			),
		).
		SetEphemeral(true)

	_ = event.CreateMessage(builder.Build())
}
