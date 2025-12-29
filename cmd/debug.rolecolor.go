package cmd

import (
	"fmt"

	"github.com/bwmarrin/discordgo"

	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

func handleDebugRoleColor(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(options) == 0 {
		return
	}

	subCommand := options[0]

	switch subCommand.Name {
	case "set":
		handleRoleColorSet(s, i, subCommand.Options)
	case "reset":
		handleRoleColorReset(s, i)
	case "refresh":
		handleRoleColorRefresh(s, i)
	default:
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("Unknown subcommand")), true)
	}
}

func handleRoleColorSet(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	role := options[0].RoleValue(s, i.GuildID)
	if role == nil {
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("Invalid role provided.")), true)
		return
	}

	// Defer
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		// Update DB
		_, err := sys.DB.Exec(`
		INSERT INTO guild_configs (guild_id, random_color_role_id, updated_at) 
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(guild_id) DO UPDATE SET random_color_role_id = ?, updated_at = CURRENT_TIMESTAMP
	`, i.GuildID, role.ID, role.ID)

		if err != nil {
			sys.LogError("Failed to update guild config: %v", err)
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("❌ Failed to save configuration.")))
			return
		}

		// Start Rotator
		proc.StartRotationForGuild(s, i.GuildID, role.ID)

		// Trigger immediate update
		proc.UpdateRoleColor(s, i.GuildID, role.ID)

		sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay(fmt.Sprintf("✅ **Random Color Role Set**\nTarget Role: <@&%s>\n\nThe color will now update periodically.", role.ID)),
		))
	}()
}

func handleRoleColorReset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		_, err := sys.DB.Exec("UPDATE guild_configs SET random_color_role_id = NULL WHERE guild_id = ?", i.GuildID)
		if err != nil {
			sys.LogError("Failed to reset guild config: %v", err)
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("❌ Failed to reset configuration.")))
			return
		}

		proc.StopRotationForGuild(i.GuildID)

		sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay("✅ **Configuration Reset**\nRandom role color disabled."),
		))
	}()
}

func handleRoleColorRefresh(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		// Fetch role ID from DB
		var roleID string
		err := sys.DB.QueryRow("SELECT random_color_role_id FROM guild_configs WHERE guild_id = ?", i.GuildID).Scan(&roleID)
		if err != nil || roleID == "" {
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("❌ No role configured. Use `/debug rolecolor set` first.")))
			return
		}

		err = proc.UpdateRoleColor(s, i.GuildID, roleID)
		if err != nil {
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("❌ Failed to refresh role color.")))
			return
		}

		sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("🎨 Role color has been refreshed!")))
	}()
}
