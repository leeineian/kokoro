package home

import (
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleRoleColorRefresh(event *events.ApplicationCommandInteractionCreate) {
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

	// Get the configured role
	roleID, err := sys.GetGuildRandomColorRole(sys.AppContext, *guildID)
	if err != nil || roleID == 0 {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(sys.MsgRoleColorErrNoRole),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	// Actually update the role color
	err = proc.UpdateRoleColor(sys.AppContext, event.Client(), *guildID, roleID)
	if err != nil {
		sys.LogDebug(sys.MsgDebugRoleColorRefreshFail, err)
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(sys.MsgRoleColorErrRefreshFail),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sys.MsgRoleColorRefreshSuccess),
			),
		).
		SetEphemeral(true).
		Build())
}
