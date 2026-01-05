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
					discord.NewTextDisplay("❌ This command can only be used in a server."),
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
					discord.NewTextDisplay("❌ No role is configured for color rotation."),
				),
			).
			SetEphemeral(true).
			Build())
		return
	}

	// Actually update the role color
	err = proc.UpdateRoleColor(sys.AppContext, event.Client(), *guildID, roleID)
	if err != nil {
		sys.LogDebug("Failed to refresh role color: %v", err)
		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("❌ Failed to refresh role color."),
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
				discord.NewTextDisplay("🎨 Role color has been refreshed!"),
			),
		).
		SetEphemeral(true).
		Build())
}
