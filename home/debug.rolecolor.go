package home

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
		roleColorRespond(s, i, "Unknown subcommand")
	}
}

func roleColorRespond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: []discordgo.MessageComponent{
				&discordgo.Container{
					Components: []discordgo.MessageComponent{
						&discordgo.TextDisplay{Content: content},
					},
				},
			},
			Flags: discordgo.MessageFlagsIsComponentsV2 | discordgo.MessageFlagsEphemeral,
		},
	})
}

func roleColorEdit(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Components: &[]discordgo.MessageComponent{
			&discordgo.Container{
				Components: []discordgo.MessageComponent{
					&discordgo.TextDisplay{Content: content},
				},
			},
		},
	})
}

func handleRoleColorSet(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	role := options[0].RoleValue(s, i.GuildID)
	if role == nil {
		roleColorRespond(s, i, "Invalid role provided.")
		return
	}

	// Update DB
	_, err := sys.DB.Exec(`
		INSERT INTO guild_configs (guild_id, random_color_role_id, updated_at) 
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(guild_id) DO UPDATE SET random_color_role_id = ?, updated_at = CURRENT_TIMESTAMP
	`, i.GuildID, role.ID, role.ID)

	if err != nil {
		sys.LogError(sys.MsgDebugRoleColorUpdateFail, err)
		roleColorRespond(s, i, "❌ Failed to save configuration.")
		return
	}

	// Start Rotator
	proc.StartRotationForGuild(s, i.GuildID, role.ID)

	// Trigger immediate update
	proc.UpdateRoleColor(s, i.GuildID, role.ID)

	roleColorRespond(s, i, fmt.Sprintf("✅ **Random Color Role Set**\nTarget Role: <@&%s>\n\nThe color will now update periodically.", role.ID))
}

func handleRoleColorReset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, err := sys.DB.Exec("UPDATE guild_configs SET random_color_role_id = NULL WHERE guild_id = ?", i.GuildID)
	if err != nil {
		sys.LogError(sys.MsgDebugRoleColorResetFail, err)
		roleColorRespond(s, i, "❌ Failed to reset configuration.")
		return
	}

	proc.StopRotationForGuild(i.GuildID)

	roleColorRespond(s, i, "✅ **Configuration Reset**\nRandom role color disabled.")
}

func handleRoleColorRefresh(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Fetch role ID from DB
	var roleID string
	err := sys.DB.QueryRow("SELECT random_color_role_id FROM guild_configs WHERE guild_id = ?", i.GuildID).Scan(&roleID)
	if err != nil || roleID == "" {
		roleColorRespond(s, i, "❌ No role configured. Use `/debug rolecolor set` first.")
		return
	}

	err = proc.UpdateRoleColor(s, i.GuildID, roleID)
	if err != nil {
		roleColorRespond(s, i, "❌ Failed to refresh role color.")
		return
	}

	roleColorRespond(s, i, "🎨 Role color has been refreshed!")
}
