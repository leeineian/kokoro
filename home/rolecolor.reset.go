package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleRoleColorReset(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ This command can only be used in a server."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	err := sys.SetGuildRandomColorRole(sys.AppContext, *guildID, 0)
	if err != nil {
		sys.LogDebug(sys.MsgDebugRoleColorResetFail, err)
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to reset role color configuration."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	// Stop rotation daemon
	proc.StopRotationForGuild(*guildID)

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("✅ Role color rotation has been disabled."),
			),
		).
		SetEphemeral(true).
		Build())
}
