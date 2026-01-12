package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleRoleColorStats(event *events.ApplicationCommandInteractionCreate) {
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

	roleID, err := sys.GetGuildRandomColorRole(sys.AppContext, *guildID)
	if err != nil || roleID == 0 {
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(sys.MsgRoleColorErrNoRoleStats),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	content := sys.MsgRoleColorStatsHeader + "\n\n" + fmt.Sprintf(sys.MsgRoleColorStatsContent, roleID)

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(true)

	_ = event.CreateMessage(builder.Build())
}
