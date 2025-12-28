package proc

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/src/sys"
)

const (
	RotationInterval = 60 * time.Second
	IdleThreshold    = 60 * time.Second
)

var (
	StartTime       = time.Now()
	statusList      []func(*discordgo.Session) *discordgo.Activity
	statusTicker    *time.Ticker
	statusStopChan  chan struct{}
	configKeyStatus = "status_visible"

	// Idle handling
	lastInteraction = time.Now()
	interactionMu   sync.RWMutex
)

// MarkInteraction updates the last interaction time
func MarkInteraction() {
	interactionMu.Lock()
	lastInteraction = time.Now()
	interactionMu.Unlock()
}

// StartStatusRotator starts the status rotation daemon
func StartStatusRotator(s *discordgo.Session) {
	sys.RegisterInteractionCallback(MarkInteraction)

	// Initialize generators
	statusList = []func(*discordgo.Session) *discordgo.Activity{
		GenerateRemindersStatus,
		GenerateColorStatus,
		GenerateUptimeStatus,
		GenerateLatencyStatus,
		GenerateTimeStatus,
	}

	if statusTicker != nil {
		statusTicker.Stop()
	}
	if statusStopChan != nil {
		close(statusStopChan)
	}

	statusTicker = time.NewTicker(RotationInterval)
	statusStopChan = make(chan struct{})

	// Run immediately
	go updateStatus(s)

	go func() {
		for {
			select {
			case <-statusTicker.C:
				updateStatus(s)
			case <-statusStopChan:
				return
			}
		}
	}()
}

// TriggerStatusUpdate forces an immediate status update
func TriggerStatusUpdate(s *discordgo.Session) {
	updateStatus(s)
}

// updateStatus checks config and updates the bot's status
func updateStatus(s *discordgo.Session) {
	// Check visibility config
	visibleStr, err := sys.GetBotConfig(configKeyStatus)
	if err != nil {
		sys.LogStatusRotator("DB Error: %v", err)
		return
	}

	// Default to TRUE (enabled) if not set, to match existing behavior.
	// Only disable if explicitly set to "false".
	if visibleStr == "false" {
		// Ensure simplified status (e.g. just online or dnd without custom text)
		s.UpdateStatusComplex(discordgo.UpdateStatusData{
			Status:     "dnd",
			Activities: []*discordgo.Activity{},
		})
		return
	}

	// Pick random generator
	gen := statusList[rand.Intn(len(statusList))]
	activity := gen(s)

	// Fallback to uptime if generator returned nil
	if activity == nil {
		activity = GenerateUptimeStatus(s)
	}

	if activity != nil {
		// Update Status

		// Determine effective status based on idle time
		statusStr := "online"
		interactionMu.RLock()
		if time.Since(lastInteraction) > IdleThreshold {
			statusStr = "dnd"
		}
		interactionMu.RUnlock()

		err = s.UpdateStatusComplex(discordgo.UpdateStatusData{
			Status:     statusStr,
			Activities: []*discordgo.Activity{activity},
		})
		if err != nil {
			sys.LogStatusRotator("Failed to update status: %v", err)
		}
	}
}

// Generators

func GenerateRemindersStatus(s *discordgo.Session) *discordgo.Activity {
	count, err := sys.GetRemindersCount()
	if err != nil {
		sys.LogStatusRotator("Reminders count error: %v", err)
		return nil
	}
	if count == 0 {
		return nil
	}

	text := fmt.Sprintf("%d pending reminder", count)
	if count != 1 {
		text += "s"
	}

	return &discordgo.Activity{
		Name:  "Custom Status",
		Type:  discordgo.ActivityTypeCustom,
		State: text,
	}
}

func GenerateColorStatus(s *discordgo.Session) *discordgo.Activity {
	nextUpdate, guildID, found := GetNextUpdate()
	if !found {
		return nil
	}

	currentColor := GetCurrentColor(guildID)

	diff := time.Until(nextUpdate)
	minutes := int(diff.Minutes())
	if minutes < 0 {
		minutes = 0
	}
	// Round up logic from JS: Math.ceil
	if diff.Seconds() > 0 {
		minutes = int(diff.Minutes()) + 1
	}

	timeStr := fmt.Sprintf("%d minutes", minutes)
	if minutes == 1 {
		timeStr = "1 minute"
	}

	return &discordgo.Activity{
		Name:  "Custom Status",
		Type:  discordgo.ActivityTypeCustom,
		State: fmt.Sprintf("%s in %s", currentColor, timeStr),
	}
}

func GenerateUptimeStatus(s *discordgo.Session) *discordgo.Activity {
	uptime := time.Since(StartTime)
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60

	return &discordgo.Activity{
		Name:  "Custom Status",
		Type:  discordgo.ActivityTypeCustom,
		State: fmt.Sprintf("Uptime: %dh %dm", hours, minutes),
	}
}

func GenerateLatencyStatus(s *discordgo.Session) *discordgo.Activity {
	ping := s.HeartbeatLatency()
	if ping == 0 {
		return nil // Not yet available
	}

	return &discordgo.Activity{
		Name:  "Custom Status",
		Type:  discordgo.ActivityTypeCustom,
		State: fmt.Sprintf("Ping: %dms", ping.Milliseconds()),
	}
}

func GenerateTimeStatus(s *discordgo.Session) *discordgo.Activity {
	now := time.Now().UTC()
	timeStr := now.Format("15:04") // HH:MM

	return &discordgo.Activity{
		Name:  "Custom Status",
		Type:  discordgo.ActivityTypeCustom,
		State: fmt.Sprintf("Time: %s UTC", timeStr),
	}
}
