package home

import (
	"context"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleDebugStatus(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	visible := data.Bool("visible")

	if visible {
		sys.SetBotConfig(context.Background(), "status_visible", "true")
	} else {
		sys.SetBotConfig(context.Background(), "status_visible", "false")
	}

	content := "✅ Status visibility updated!"
	if visible {
		content = "✅ Status rotation enabled!"
	} else {
		content = "✅ Status rotation disabled!"
	}

	err := event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(true).
		Build())
	if err != nil {
		sys.LogDebug(sys.MsgDebugStatusCmdFail, err)
	}
}
