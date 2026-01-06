package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleSessionStatus(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	visible := data.Bool("visible")

	if visible {
		sys.SetBotConfig(sys.AppContext, "status_visible", "true")
	} else {
		sys.SetBotConfig(sys.AppContext, "status_visible", "false")
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
