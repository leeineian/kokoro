package home

import (
	"context"
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleDebugRoleColor(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, subCmd string) {
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

	switch subCmd {
	case "set":
		roleID := data.Snowflake("role")
		err := sys.SetGuildRandomColorRole(context.Background(), *guildID, roleID)
		if err != nil {
			sys.LogDebug(sys.MsgDebugRoleColorUpdateFail, err)
			event.CreateMessage(discord.NewMessageCreateBuilder().
				SetIsComponentsV2(true).
				AddComponents(
					discord.NewContainer(
						discord.NewTextDisplay("❌ Failed to set role color configuration."),
					),
				).
				SetEphemeral(true).
				Build())
			return
		}

		// Start rotation daemon for this guild
		proc.StartRotationForGuild(event.Client(), *guildID, roleID)

		// Trigger immediate color update
		proc.UpdateRoleColor(event.Client(), *guildID, roleID)

		event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(fmt.Sprintf("✅ Role <@&%s> will now have random colors!", roleID)),
				),
			).
			SetEphemeral(true).
			Build())

	case "reset":
		err := sys.SetGuildRandomColorRole(context.Background(), *guildID, 0)
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

	case "refresh":
		// Get the configured role
		roleID, err := sys.GetGuildRandomColorRole(context.Background(), *guildID)
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
		err = proc.UpdateRoleColor(event.Client(), *guildID, roleID)
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
}
