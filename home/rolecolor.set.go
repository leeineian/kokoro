package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleRoleColorSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	guildID := event.GuildID()
	if guildID == nil {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(sys.MsgRoleColorErrGuildOnly),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	roleID := data.Snowflake("role")
	err := sys.SetGuildRandomColorRole(sys.AppContext, *guildID, roleID)
	if err != nil {
		sys.LogDebug(sys.MsgDebugRoleColorUpdateFail, err)
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(sys.MsgRoleColorErrSetFail),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	// Start rotation daemon for this guild
	proc.StartRotationForGuild(sys.AppContext, event.Client(), *guildID, roleID)

	// Trigger immediate color update
	proc.UpdateRoleColor(sys.AppContext, event.Client(), *guildID, roleID)

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(fmt.Sprintf(sys.MsgRoleColorSetSuccess, roleID)),
			),
		).
		SetEphemeral(true).
		Build())
}
