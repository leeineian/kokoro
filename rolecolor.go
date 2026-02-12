package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

// ============================================================================
// Role Color System Constants
// ============================================================================

const (
	MsgRoleColorFailedToFetchConfigs = "Failed to fetch configs: %v"
	MsgRoleColorNextUpdate           = "Guild %s next update in %d minutes"
	MsgRoleColorUpdateFail           = "Failed to update role %s in guild %s: %v"
	MsgRoleColorUpdated              = "Updated role %s in guild %s to %s"
	MsgRoleColorErrGuildOnly         = "This command can only be used in a server."
	MsgRoleColorErrSetFail           = "Failed to set role color configuration."
	MsgRoleColorErrResetFail         = "Failed to reset role color configuration."
	MsgRoleColorErrNoRole            = "No role is configured for color rotation."
	MsgRoleColorErrRefreshFail       = "Failed to refresh role color."
	MsgRoleColorErrNoRoleStats       = "No random color role is currently configured for this server. Use `/rolecolor set` to start!"
	MsgRoleColorSetSuccess           = "Role <@&%s> will now have random colors!"
	MsgRoleColorResetSuccess         = "Role color rotation has been disabled."
	MsgRoleColorRefreshSuccess       = "Role color has been refreshed!"
	MsgRoleColorStatsHeader          = "**Random Role Color Status**"
	MsgRoleColorStatsContent         = "**Current Role:** <@&%s>\n" +
		"**Status:** `Active`\n\n" +
		"The bot will periodically change the color of this role to a random vibrant hue."
)

// ===========================
// Command Registration
// ===========================

func init() {
	adminPerm := discord.PermissionAdministrator

	OnClientReady(func(ctx context.Context, client bot.Client) {
		RegisterDaemon(LogRoleColorRotator, func(ctx context.Context) (bool, func(), func()) { return StartRoleColorRotator(ctx, client) })
	})

	RegisterCommand(discord.SlashCommandCreate{
		Name:                     "rolecolor",
		Description:              "Random Role Color Utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Set the role to randomly color",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionRole{
						Name:        "role",
						Description: "The role to color",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "reset",
				Description: "Reset configuration",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "refresh",
				Description: "Force an immediate color change",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View current random color role configuration",
			},
		},
	}, handleRoleColor)
}

// ===========================
// Constants
// ===========================

const (
	minMinutes = 1
	maxMinutes = 10
)

// ===========================
// Role Color System Types
// ===========================

// roleState tracks what we are rotating and the last known value
type roleState struct {
	sync.RWMutex
	guildID   snowflake.ID
	roleID    snowflake.ID
	lastColor string // #HEX
	active    bool   // prevents ghost rotations
}

var (
	// Map to store active timers: map[guildID]*time.Timer
	rotatorTimers sync.Map

	// Tracking for Status Rotator
	nextUpdateMap sync.Map // map[guildID]time.Time
	roleStates    sync.Map // map[guildID]*roleState
)

// handleRoleColor routes rolecolor subcommands to their respective handlers
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

// StartRoleColorRotator initializes the role color rotator daemon
func StartRoleColorRotator(ctx context.Context, client bot.Client) (bool, func(), func()) {
	// Load all configured guilds
	configs, err := GetAllGuildRandomColorConfigs(ctx)
	if err != nil {
		LogRoleColorRotator(MsgRoleColorFailedToFetchConfigs, err)
		return false, nil, nil
	}

	if len(configs) == 0 {
		return false, nil, nil
	}

	return true, func() {
		for gID, rID := range configs {
			state := &roleState{
				guildID: gID,
				roleID:  rID,
				active:  true,
			}
			roleStates.Store(gID, state)

			// Start rotation for this guild
			ScheduleNextUpdate(ctx, client, gID, rID)
		}
	}, func() { ShutdownRoleColorRotator(ctx) }
}

// ShutdownRoleColorRotator stops all active rotation timers
func ShutdownRoleColorRotator(ctx context.Context) {
	LogRoleColorRotator("Shutting down Role Color Rotator...")
	rotatorTimers.Range(func(key, value any) bool {
		if timer, ok := value.(*time.Timer); ok {
			timer.Stop()
		}
		rotatorTimers.Delete(key)
		return true
	})
}

// StartRotationForGuild starts or restarts the rotation for a specific guild
func StartRotationForGuild(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) {
	// Stop existing if any
	StopRotationForGuild(guildID)

	state := &roleState{
		guildID: guildID,
		roleID:  roleID,
		active:  true,
	}
	roleStates.Store(guildID, state)

	ScheduleNextUpdate(ctx, client, guildID, roleID)
}

// StopRotationForGuild stops the rotation for a specific guild
func StopRotationForGuild(guildID snowflake.ID) {
	if val, ok := roleStates.Load(guildID); ok {
		state := val.(*roleState)
		state.Lock()
		state.active = false
		state.Unlock()
	}

	if val, ok := rotatorTimers.Load(guildID); ok {
		if timer, ok := val.(*time.Timer); ok {
			timer.Stop()
		}
		rotatorTimers.Delete(guildID)
	}
	nextUpdateMap.Delete(guildID)
	roleStates.Delete(guildID)
}

// ScheduleNextUpdate schedules the next color update
func ScheduleNextUpdate(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) {
	if ctx.Err() != nil {
		return
	}

	// Verify we are still active
	val, ok := roleStates.Load(guildID)
	if !ok {
		return
	}
	state := val.(*roleState)
	state.RLock()
	if !state.active {
		state.RUnlock()
		return
	}
	state.RUnlock()

	// Calculate random duration
	minutes := RandomIntRange(minMinutes, maxMinutes)
	duration := time.Duration(minutes) * time.Minute

	nextUpdate := time.Now().UTC().Add(duration)
	nextUpdateMap.Store(guildID, nextUpdate)

	// If current color is unknown, try to fetch it
	state.Lock()
	if state.lastColor == "" {
		if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
			state.lastColor = fmt.Sprintf("#%06X", role.Color)
		}
	}
	state.Unlock()

	// Format guild identifier (avoid Rest calls in background loop if possible)
	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	}
	LogRoleColorRotator(MsgRoleColorNextUpdate, guildLabel, minutes)

	timer := time.AfterFunc(duration, func() {
		// Verify active again before updating
		state.RLock()
		active := state.active
		state.RUnlock()
		if !active {
			return
		}

		_ = UpdateRoleColor(ctx, client, guildID, roleID)
		// Schedule next one recursively
		ScheduleNextUpdate(ctx, client, guildID, roleID)
	})

	rotatorTimers.Store(guildID, timer)
}

// UpdateRoleColor performs the immediate color update
func UpdateRoleColor(ctx context.Context, client bot.Client, guildID, roleID snowflake.ID) error {
	var newColor int
	var lastHex string

	val, ok := roleStates.Load(guildID)
	if !ok {
		return fmt.Errorf("rotation not active")
	}
	state := val.(*roleState)

	state.RLock()
	lastHex = state.lastColor
	active := state.active
	state.RUnlock()

	if !active {
		return fmt.Errorf("rotation not active")
	}

	// Try up to 10 times to get a unique, non-zero color
	for range 10 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			// Fallback if crypto/rand fails
			newColor = RandomIntRange(1, 16777214) // Avoid 0 and white-ish 0xFFFFFF if needed, but 0xFFFFFF is fine
		} else {
			// Ensure 24-bit color (0x0 to 0xFFFFFF)
			newColor = int(binary.BigEndian.Uint32(b[:]) & 0xFFFFFF)
		}

		if newColor == 0 {
			continue
		}

		hexColor := fmt.Sprintf("#%06X", newColor)
		if lastHex == "" || hexColor != lastHex {
			break
		}
	}

	if newColor == 0 {
		newColor = 0xE91E63 // Fallback color
	}

	_, err := client.Rest.UpdateRole(guildID, roleID, discord.RoleUpdate{
		Color: &newColor,
	})

	// Format identifiers for logging
	roleLabel := roleID.String()
	if role, ok := client.Caches.Role(guildID, roleID); ok {
		roleLabel = fmt.Sprintf("%s (%s)", role.Name, roleID)
	}

	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	}

	if err != nil {
		LogRoleColorRotator(MsgRoleColorUpdateFail, roleLabel, guildLabel, err)
		return err
	}

	hexColor := ColorizeHex(newColor)
	LogRoleColorRotator(MsgRoleColorUpdated, roleLabel, guildLabel, hexColor)

	state.Lock()
	state.lastColor = fmt.Sprintf("#%06X", newColor)
	state.Unlock()
	return nil
}

// GetNextUpdate returns the nearest next update timestamp and the guild ID
func GetNextUpdate(ctx context.Context) (time.Time, snowflake.ID, bool) {
	var nearest time.Time
	var nearestGuild snowflake.ID
	found := false

	nextUpdateMap.Range(func(key, value any) bool {
		t := value.(time.Time)
		guildID := key.(snowflake.ID)
		if !found || t.Before(nearest) {
			nearest = t
			nearestGuild = guildID
			found = true
		}
		return true
	})

	return nearest, nearestGuild, found
}

// GetCurrentColor returns the current color for a guild, prioritizing cache
func GetCurrentColor(ctx context.Context, client bot.Client, guildID snowflake.ID) string {
	val, ok, _ := GetCurrentColorInt(ctx, client, guildID)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%06X", val)
}

// GetCurrentColorInt returns the current color for a guild as an integer
func GetCurrentColorInt(ctx context.Context, client bot.Client, guildID snowflake.ID) (int, bool, bool) {
	val, ok := roleStates.Load(guildID)
	if !ok {
		return 0, false, false
	}
	state := val.(*roleState)

	// 1. Try Cache First (reflects reality)
	if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
		return role.Color, true, true
	}

	// 2. Fallback to our internal record
	state.RLock()
	defer state.RUnlock()
	if state.lastColor != "" {
		var colorInt int
		fmt.Sscanf(state.lastColor, "#%X", &colorInt)
		return colorInt, true, false
	}

	return 0, false, false
}

// ===========================
// Handler Functions
// ===========================

// ===========================
// Subcommand Handlers
// ===========================

// handleRoleColorSet starts or restarts role color rotation for a guild
func handleRoleColorSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	roleID := data.Snowflake("role")

	// Verify permissions and hierarchy
	if guild, ok := event.Client().Caches.Guild(*guildID); ok {
		self, ok := event.Client().Caches.Member(*guildID, event.Client().ApplicationID)
		if ok {
			// Calculate bot permissions
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

			// Manage Roles permission
			if !perms.Has(discord.PermissionManageRoles) && !perms.Has(discord.PermissionAdministrator) {
				roleColorRespond(event, "❌ Bot lacks `Manage Roles` permission.")
				return
			}

			// Role hierarchy (Discord limitation)
			targetRole, ok := event.Client().Caches.Role(*guildID, roleID)
			if ok {
				// Find highest bot role position
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

	err := SetGuildRandomColorRole(AppContext, *guildID, roleID)
	if err != nil {
		LogDebug(MsgDebugRoleColorUpdateFail, err)
		roleColorRespond(event, MsgRoleColorErrSetFail)
		return
	}

	// Start rotation daemon for this guild
	StartRotationForGuild(AppContext, *event.Client(), *guildID, roleID)

	// Trigger immediate color update and check result
	err = UpdateRoleColor(AppContext, *event.Client(), *guildID, roleID)
	if err != nil {
		// If immediate update fails, stop rotation and tell user
		StopRotationForGuild(*guildID)
		roleColorRespond(event, fmt.Sprintf("❌ Failed to set role color: %v", err))
		return
	}

	roleColorRespond(event, fmt.Sprintf(MsgRoleColorSetSuccess, roleID))
}

// handleRoleColorReset stops role color rotation for a guild
func handleRoleColorReset(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	err := SetGuildRandomColorRole(AppContext, *guildID, 0)
	if err != nil {
		LogDebug(MsgDebugRoleColorResetFail, err)
		roleColorRespond(event, MsgRoleColorErrResetFail)
		return
	}

	// Stop rotation daemon
	StopRotationForGuild(*guildID)

	roleColorRespond(event, MsgRoleColorResetSuccess)
}

// handleRoleColorRefresh immediately updates the role color
func handleRoleColorRefresh(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	// Get the configured role
	roleID, err := GetGuildRandomColorRole(AppContext, *guildID)
	if err != nil || roleID == 0 {
		roleColorRespond(event, MsgRoleColorErrNoRole)
		return
	}

	// Actually update the role color
	err = UpdateRoleColor(AppContext, *event.Client(), *guildID, roleID)
	if err != nil {
		LogDebug(MsgDebugRoleColorRefreshFail, err)
		roleColorRespond(event, MsgRoleColorErrRefreshFail)
		return
	}

	roleColorRespond(event, MsgRoleColorRefreshSuccess)
}

// handleRoleColorStats displays the current role color rotation status
func handleRoleColorStats(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		roleColorRespond(event, MsgRoleColorErrGuildOnly)
		return
	}

	roleID, err := GetGuildRandomColorRole(AppContext, *guildID)
	if err != nil || roleID == 0 {
		roleColorRespond(event, MsgRoleColorErrNoRoleStats)
		return
	}

	nextUpdate, _, found := GetNextUpdate(AppContext)
	colorStr := GetCurrentColor(AppContext, *event.Client(), *guildID)

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

// roleColorRespond sends a response message for rolecolor commands
func roleColorRespond(event *events.ApplicationCommandInteractionCreate, content string) {
	_ = RespondInteractionV2(*event.Client(), event, content, true)
}
