package proc

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

const (
	minMinutes = 1
	maxMinutes = 10
)

func init() {
	sys.OnSessionReady(func(s *discordgo.Session) {
		sys.RegisterDaemon(sys.LogRoleColorRotator, func() { StartRoleColorRotator(s) })
	})
}

var (
	// Map to store active timers: map[guildID]*time.Timer
	rotatorTimers sync.Map

	// Tracking for Status Rotator
	nextUpdateMap   sync.Map // map[guildID]time.Time
	currentColorMap sync.Map // map[guildID]string
)

// StartRoleColorRotator initializes the role color rotator daemon
func StartRoleColorRotator(s *discordgo.Session) {
	// Load all configured guilds
	configs, err := sys.GetAllGuildRandomColorConfigs()
	if err != nil {
		sys.LogRoleColorRotator(sys.MsgRoleColorFailedToFetchConfigs, err)
		return
	}

	for guildID, roleID := range configs {
		// Start rotation for this guild
		go ScheduleNextUpdate(s, guildID, roleID)
	}
}

// StartRotationForGuild starts or restarts the rotation for a specific guild
func StartRotationForGuild(s *discordgo.Session, guildID, roleID string) {
	// Stop existing if any
	StopRotationForGuild(guildID)
	ScheduleNextUpdate(s, guildID, roleID)
}

// StopRotationForGuild stops the rotation for a specific guild
func StopRotationForGuild(guildID string) {
	if val, ok := rotatorTimers.Load(guildID); ok {
		if timer, ok := val.(*time.Timer); ok {
			timer.Stop()
		}
		rotatorTimers.Delete(guildID)
	}
	nextUpdateMap.Delete(guildID)
	currentColorMap.Delete(guildID)
}

// ScheduleNextUpdate schedules the next color update
func ScheduleNextUpdate(s *discordgo.Session, guildID, roleID string) {
	// Calculate random duration
	minutes := mrand.Intn(maxMinutes-minMinutes+1) + minMinutes
	duration := time.Duration(minutes) * time.Minute

	nextUpdate := time.Now().Add(duration)
	nextUpdateMap.Store(guildID, nextUpdate)

	// If current color is unknown, try to fetch it
	if _, ok := currentColorMap.Load(guildID); !ok {
		var role *discordgo.Role
		// Try Cache first
		role, err := s.State.Role(guildID, roleID)
		if err != nil {
			// Fallback to API: Fetch all roles
			stRoles, err2 := s.GuildRoles(guildID)
			if err2 == nil {
				for _, r := range stRoles {
					if r.ID == roleID {
						role = r
						break
					}
				}
			}
		}

		if role != nil {
			hexColor := fmt.Sprintf("#%06X", role.Color)
			currentColorMap.Store(guildID, hexColor)
		}
	}

	sys.LogRoleColorRotator(sys.MsgRoleColorNextUpdate, guildID, minutes)

	timer := time.AfterFunc(duration, func() {
		UpdateRoleColor(s, guildID, roleID)
		// Schedule next one recursively
		ScheduleNextUpdate(s, guildID, roleID)
	})

	rotatorTimers.Store(guildID, timer)
}

// UpdateRoleColor performs the immediate color update
func UpdateRoleColor(s *discordgo.Session, guildID, roleID string) error {
	var newColor int
	lastHex, _ := currentColorMap.Load(guildID)

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
		if lastHex == nil || hexColor != lastHex.(string) {
			break
		}
	}

	_, err := s.GuildRoleEdit(guildID, roleID, &discordgo.RoleParams{
		Color: &newColor,
	})

	if err != nil {
		sys.LogRoleColorRotator(sys.MsgRoleColorUpdateFail, roleID, guildID, err)
		return err
	}

	hexColor := fmt.Sprintf("#%06X", newColor)
	sys.LogRoleColorRotator(sys.MsgRoleColorUpdated, roleID, guildID, hexColor)

	currentColorMap.Store(guildID, hexColor)
	return nil
}

// GetNextUpdate returns the nearest next update timestamp and the guild ID
func GetNextUpdate() (time.Time, string, bool) {
	var nearest time.Time
	var nearestGuild string
	found := false

	nextUpdateMap.Range(func(key, value interface{}) bool {
		t := value.(time.Time)
		guildID := key.(string)
		if !found || t.Before(nearest) {
			nearest = t
			nearestGuild = guildID
			found = true
		}
		return true
	})

	return nearest, nearestGuild, found
}

// GetCurrentColor returns the current color for a guild
func GetCurrentColor(guildID string) string {
	if val, ok := currentColorMap.Load(guildID); ok {
		return val.(string)
	}
	return "Unknown"
}
