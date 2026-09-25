package app

import (
	"fmt"
	"log"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func handleRoleColor(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "stats":
		handleRoleColorStats(event)
	case "set":
		handleRoleColorSet(event, data)
	case "reset":
		handleRoleColorReset(event)
	case "refresh":
		handleRoleColorRefresh(event)
	default:
		log.Printf("Unknown rolecolor subcommand: %s", subCmd)
	}
}

func handleRoleColorSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	roleID := data.Snowflake("role")

	if guild, ok := event.Client().Caches.Guild(*guildID); ok {
		self, ok := event.Client().Caches.Member(*guildID, event.Client().ApplicationID)
		if ok {
			var perms discord.Permissions
			if guild.OwnerID == event.Client().ApplicationID {
				perms = discord.PermissionsAll
			} else {
				for _, rID := range self.RoleIDs {
					if r, ok := event.Client().Caches.Role(*guildID, rID); ok {
						perms |= r.Permissions
					}
				}
				if everyone, ok := event.Client().Caches.Role(*guildID, snowflake.ID(*guildID)); ok {
					perms |= everyone.Permissions
				}
			}

			if !perms.Has(discord.PermissionManageRoles) && !perms.Has(discord.PermissionAdministrator) {
				roleColorRespond(event, "❌ Bot lacks `Manage Roles` permission.")
				return
			}

			targetRole, ok := event.Client().Caches.Role(*guildID, roleID)
			if ok {
				highestPos := -1
				for _, rID := range self.RoleIDs {
					if r, ok := event.Client().Caches.Role(*guildID, rID); ok {
						if r.Position > highestPos {
							highestPos = r.Position
						}
					}
				}
				if everyone, ok := event.Client().Caches.Role(*guildID, snowflake.ID(*guildID)); ok {
					if everyone.Position > highestPos {
						highestPos = everyone.Position
					}
				}

				if targetRole.Position >= highestPos {
					roleColorRespond(event, "❌ The role <@&"+roleID.String()+"> is above or equal to my highest role. I cannot edit it.")
					return
				}
			}
		}
	}

	err := hooks.SetGuildRandomColorRole(hooks.AppContext, *guildID, roleID)
	if err != nil {
		hooks.LogDebug(MsgDebugRoleColorUpdateFail, err)
		roleColorRespond(event, MsgRoleColorErrSetFail)
		return
	}

	StartRotationForGuild(hooks.AppContext, *event.Client(), *guildID, roleID)

	err = UpdateRoleColor(hooks.AppContext, *event.Client(), *guildID, roleID)
	if err != nil {
		// If immediate update fails, stop rotation and tell user
		StopRotationForGuild(*guildID)
		roleColorRespond(event, fmt.Sprintf("❌ Failed to set role color: %v", err))
		return
	}

	roleColorRespond(event, fmt.Sprintf(MsgRoleColorSetSuccess, roleID))
}

func handleRoleColorReset(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	err := hooks.SetGuildRandomColorRole(hooks.AppContext, *guildID, 0)
	if err != nil {
		hooks.LogDebug(MsgDebugRoleColorResetFail, err)
		roleColorRespond(event, MsgRoleColorErrResetFail)
		return
	}

	StopRotationForGuild(*guildID)

	roleColorRespond(event, MsgRoleColorResetSuccess)
}

func handleRoleColorRefresh(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	roleID, err := hooks.GetGuildRandomColorRole(hooks.AppContext, *guildID)
	if err != nil || roleID == 0 {
		roleColorRespond(event, MsgRoleColorErrNoRole)
		return
	}

	err = UpdateRoleColor(hooks.AppContext, *event.Client(), *guildID, roleID)
	if err != nil {
		hooks.LogDebug(MsgDebugRoleColorRefreshFail, err)
		roleColorRespond(event, MsgRoleColorErrRefreshFail)
		return
	}

	roleColorRespond(event, MsgRoleColorRefreshSuccess)
}

func handleRoleColorStats(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	roleID, err := hooks.GetGuildRandomColorRole(hooks.AppContext, *guildID)
	if err != nil || roleID == 0 {
		roleColorRespond(event, MsgRoleColorErrNoRoleStats)
		return
	}

	nextUpdate, _, found := GetNextUpdate(hooks.AppContext)
	colorStr := GetCurrentColor(hooks.AppContext, *event.Client(), *guildID)

	content := MsgRoleColorStatsHeader + "\n\n" + fmt.Sprintf(MsgRoleColorStatsContent, roleID)
	if colorStr != "" {
		content += fmt.Sprintf("\n**Current Color:** `%s`", colorStr)
	}
	if found {
		timeRemaining := time.Until(nextUpdate).Round(time.Second)
		if timeRemaining < 0 {
			timeRemaining = 0
		}
		content += fmt.Sprintf("\n**Next Update:** in %v", timeRemaining)
	}

	roleColorRespond(event, content)
}

func roleColorRespond(event *events.ApplicationCommandInteractionCreate, content string) {
	_ = hooks.RespondInteractionV2(*event.Client(), event, content, true)
}
