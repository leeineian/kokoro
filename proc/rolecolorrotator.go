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
	sys.OnClientReady(func(client *bot.Client) {
		sys.RegisterDaemon(sys.LogRoleColorRotator, func() { StartRoleColorRotator(client) })
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
func StartRoleColorRotator(client *bot.Client) {
	// Load all configured guilds
	configs, err := sys.GetAllGuildRandomColorConfigs(context.Background())
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
		go ScheduleNextUpdate(client, gID, rID)
	}
}

// StartRotationForGuild starts or restarts the rotation for a specific guild
func StartRotationForGuild(client *bot.Client, guildID, roleID snowflake.ID) {
	// Stop existing if any
	StopRotationForGuild(guildID)

	state := &roleState{
		guildID: guildID,
		roleID:  roleID,
	}
	roleStates.Store(guildID, state)

	ScheduleNextUpdate(client, guildID, roleID)
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
func ScheduleNextUpdate(client *bot.Client, guildID, roleID snowflake.ID) {
	// Calculate random duration
	minutes := mrand.Intn(maxMinutes-minMinutes+1) + minMinutes
	duration := time.Duration(minutes) * time.Minute

	nextUpdate := time.Now().Add(duration)
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

	sys.LogRoleColorRotator(sys.MsgRoleColorNextUpdate, guildID.String(), minutes)

	timer := time.AfterFunc(duration, func() {
		UpdateRoleColor(client, guildID, roleID)
		// Schedule next one recursively
		ScheduleNextUpdate(client, guildID, roleID)
	})

	rotatorTimers.Store(guildID, timer)
}

// UpdateRoleColor performs the immediate color update
func UpdateRoleColor(client *bot.Client, guildID, roleID snowflake.ID) error {
	var newColor int
	var lastHex string

	if val, ok := roleStates.Load(guildID); ok {
		lastHex = val.(*roleState).lastColor
	}

	// Try up to 10 times to get a unique, non-zero color
	for i := 0; i < 10; i++ {
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

	if err != nil {
		sys.LogRoleColorRotator(sys.MsgRoleColorUpdateFail, roleID.String(), guildID.String(), err)
		return err
	}

	hexColor := fmt.Sprintf("#%06X", newColor)
	sys.LogRoleColorRotator(sys.MsgRoleColorUpdated, roleID.String(), guildID.String(), hexColor)

	if val, ok := roleStates.Load(guildID); ok {
		val.(*roleState).lastColor = hexColor
	}
	return nil
}

// GetNextUpdate returns the nearest next update timestamp and the guild ID
func GetNextUpdate() (time.Time, snowflake.ID, bool) {
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
func GetCurrentColor(client *bot.Client, guildID snowflake.ID) string {
	val, ok := roleStates.Load(guildID)
	if !ok {
		return ""
	}
	state := val.(*roleState)

	// 1. Try Cache First (reflects reality)
	if role, ok := client.Caches.Role(state.guildID, state.roleID); ok {
		return fmt.Sprintf("#%06X", role.Color)
	}

	// 2. Fallback to our internal record (useful during startup/late cache)
	return state.lastColor
}

// ForceColorUpdate is unused but required for interface
func ForceColorUpdate(client *bot.Client, guildID, roleID snowflake.ID) {
	UpdateRoleColor(client, guildID, roleID)
}
