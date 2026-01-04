package proc

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

const (
	minMinutes = 1
	maxMinutes = 10
)

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogRoleColorRotator, func(ctx context.Context) { StartRoleColorRotator(ctx, client) })
	})
}

// RoleState tracks what we are rotating and the last known value
type roleState struct {
	guildID   snowflake.ID
	roleID    snowflake.ID
	lastColor string // #HEX
}

var (
	// Map to store active timers: map[guildID]*time.Timer
	rotatorTimers sync.Map

	// Tracking for Status Rotator
	nextUpdateMap sync.Map // map[guildID]time.Time
	roleStates    sync.Map // map[guildID]*roleState
)

// StartRoleColorRotator initializes the role color rotator daemon
func StartRoleColorRotator(ctx context.Context, client *bot.Client) {
	// Move the startup logic into a goroutine to avoid blocking other daemons
	go func() {
		// Load all configured guilds
		configs, err := sys.GetAllGuildRandomColorConfigs(ctx)
		if err != nil {
			sys.LogRoleColorRotator(sys.MsgRoleColorFailedToFetchConfigs, err)
			return
		}

		for gID, rID := range configs {
			state := &roleState{
				guildID: gID,
				roleID:  rID,
			}
			roleStates.Store(gID, state)

			// Start rotation for this guild
			ScheduleNextUpdate(ctx, client, gID, rID)
		}
	}()
}

// StartRotationForGuild starts or restarts the rotation for a specific guild
func StartRotationForGuild(ctx context.Context, client *bot.Client, guildID, roleID snowflake.ID) {
	// Stop existing if any
	StopRotationForGuild(guildID)

	state := &roleState{
		guildID: guildID,
		roleID:  roleID,
	}
	roleStates.Store(guildID, state)

	ScheduleNextUpdate(ctx, client, guildID, roleID)
}

// StopRotationForGuild stops the rotation for a specific guild
func StopRotationForGuild(guildID snowflake.ID) {
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
func ScheduleNextUpdate(ctx context.Context, client *bot.Client, guildID, roleID snowflake.ID) {
	// Calculate random duration
	minutes := mrand.Intn(maxMinutes-minMinutes+1) + minMinutes
	duration := time.Duration(minutes) * time.Minute

	nextUpdate := time.Now().UTC().Add(duration)
	nextUpdateMap.Store(guildID, nextUpdate)

	// If current color is unknown, try to fetch it
	if val, ok := roleStates.Load(guildID); ok {
		state := val.(*roleState)
		if state.lastColor == "" {
			if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
				state.lastColor = fmt.Sprintf("#%06X", role.Color)
			}
		}
	}

	// Format guild identifier
	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	} else {
		// Fallback to Rest if cache is empty (useful on startup)
		if g, err := client.Rest.GetGuild(guildID, false); err == nil {
			guildLabel = fmt.Sprintf("%s (%s)", g.Name, guildID)
		}
	}
	sys.LogRoleColorRotator(sys.MsgRoleColorNextUpdate, guildLabel, minutes)

	timer := time.AfterFunc(duration, func() {
		// Use a local context or AppContext for the background update since the original ctx might be gone
		// But ideally we want to respect the lifecycle.
		// For now, let's use sys.AppContext as a fallback if ctx is done,
		// but passing it in shows the intent.
		UpdateRoleColor(ctx, client, guildID, roleID)
		// Schedule next one recursively
		ScheduleNextUpdate(ctx, client, guildID, roleID)
	})

	rotatorTimers.Store(guildID, timer)
}

// UpdateRoleColor performs the immediate color update
func UpdateRoleColor(ctx context.Context, client *bot.Client, guildID, roleID snowflake.ID) error {
	var newColor int
	var lastHex string

	if val, ok := roleStates.Load(guildID); ok {
		lastHex = val.(*roleState).lastColor
	}

	// Try up to 10 times to get a unique, non-zero color
	for range 10 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			// Fallback if crypto/rand fails
			newColor = mrand.Intn(16777215) + 1
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

	_, err := client.Rest.UpdateRole(guildID, roleID, discord.RoleUpdate{
		Color: &newColor,
	})

	// Format identifiers for logging
	roleLabel := roleID.String()
	if role, ok := client.Caches.Role(guildID, roleID); ok {
		roleLabel = fmt.Sprintf("%s (%s)", role.Name, roleID)
	} else if roles, err := client.Rest.GetRoles(guildID); err == nil {
		for _, r := range roles {
			if r.ID == roleID {
				roleLabel = fmt.Sprintf("%s (%s)", r.Name, roleID)
				break
			}
		}
	}

	guildLabel := guildID.String()
	if guild, ok := client.Caches.Guild(guildID); ok {
		guildLabel = fmt.Sprintf("%s (%s)", guild.Name, guildID)
	} else if g, err := client.Rest.GetGuild(guildID, false); err == nil {
		guildLabel = fmt.Sprintf("%s (%s)", g.Name, guildID)
	}

	if err != nil {
		sys.LogRoleColorRotator(sys.MsgRoleColorUpdateFail, roleLabel, guildLabel, err)
		return err
	}

	hexColor := sys.ColorizeHex(newColor)
	sys.LogRoleColorRotator(sys.MsgRoleColorUpdated, roleLabel, guildLabel, hexColor)

	if val, ok := roleStates.Load(guildID); ok {
		val.(*roleState).lastColor = fmt.Sprintf("#%06X", newColor)
	}
	return nil
}

// GetNextUpdate returns the nearest next update timestamp and the guild ID
func GetNextUpdate(ctx context.Context) (time.Time, snowflake.ID, bool) {
	var nearest time.Time
	var nearestGuild snowflake.ID
	found := false

	nextUpdateMap.Range(func(key, value interface{}) bool {
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
func GetCurrentColor(ctx context.Context, client *bot.Client, guildID snowflake.ID) string {
	val, ok, _ := GetCurrentColorInt(ctx, client, guildID)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%06X", val)
}

// GetCurrentColorInt returns the current color for a guild as an integer
func GetCurrentColorInt(ctx context.Context, client *bot.Client, guildID snowflake.ID) (int, bool, bool) {
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
	if state.lastColor != "" {
		var colorInt int
		fmt.Sscanf(state.lastColor, "#%X", &colorInt)
		return colorInt, true, false
	}

	return 0, false, false
}

// ForceColorUpdate is unused but required for interface
func ForceColorUpdate(ctx context.Context, client *bot.Client, guildID, roleID snowflake.ID) {
	UpdateRoleColor(ctx, client, guildID, roleID)
}
