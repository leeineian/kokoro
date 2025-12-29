package proc

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/src/sys"
)

func init() {
	sys.OnSessionReady(func(s *discordgo.Session) {
		sys.RegisterDaemon(sys.LogStatusRotator, func() { StartStatusRotator(s) })
	})
}

func GetRotationInterval() time.Duration {
	return time.Duration(15+rand.Intn(46)) * time.Second
}

var (
	StartTime       = time.Now()
	statusList      []func(*discordgo.Session) string
	statusStopChan  chan struct{}
	lastStatusIdx   int = -1
	configKeyStatus     = "status_visible"
)

func StartStatusRotator(s *discordgo.Session) {
	statusList = []func(*discordgo.Session) string{
		GetRemindersStatus,
		GetColorStatus,
		GetUptimeStatus,
		GetLatencyStatus,
		GetTimeStatus,
	}

	if statusStopChan != nil {
		close(statusStopChan)
	}
	statusStopChan = make(chan struct{})

	go func() {
		for {
			next := GetRotationInterval()
			updateStatus(s, next)
			select {
			case <-time.After(next):
			case <-statusStopChan:
				return
			}
		}
	}()
}

func TriggerStatusUpdate(s *discordgo.Session) {
	updateStatus(s, 0)
}

func updateStatus(s *discordgo.Session, nextInterval time.Duration) {
	if s == nil || s.State == nil || s.State.User == nil {
		return
	}

	visibleStr, err := sys.GetBotConfig(configKeyStatus)
	if err != nil || visibleStr == "false" {
		s.UpdateStatusComplex(discordgo.UpdateStatusData{
			Status: "online",
		})
		return
	}

	idx := rand.Intn(len(statusList))
	if lastStatusIdx != -1 && len(statusList) > 1 && idx == lastStatusIdx {
		idx = (idx + 1) % len(statusList)
	}
	lastStatusIdx = idx

	gen := statusList[idx]
	text := gen(s)
	if text == "" {
		text = GetUptimeStatus(s)
	}

	activity := &discordgo.Activity{
		Name:          text,
		Type:          discordgo.ActivityTypeStreaming,
		URL:           "https://www.twitch.tv/videos/1110069047",
		ApplicationID: s.State.User.ID,
	}

	err = s.UpdateStatusComplex(discordgo.UpdateStatusData{
		Status:     "online",
		Activities: []*discordgo.Activity{activity},
	})

	if err != nil {
		sys.LogStatusRotator("Update failed: %v", err)
	} else {
		if nextInterval > 0 {
			sys.LogStatusRotator("Status rotated to: %s (Next rotate in %v)", text, nextInterval)
		} else {
			sys.LogStatusRotator("Status rotated to: %s", text)
		}
	}
}

// Generators

func GetRemindersStatus(s *discordgo.Session) string {
	count, _ := sys.GetRemindersCount()
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Reminder: %d", count)
}

func GetColorStatus(s *discordgo.Session) string {
	nextUpdate, guildID, found := GetNextUpdate()
	if !found {
		return ""
	}
	currentColor := GetCurrentColor(guildID)
	diff := time.Until(nextUpdate)
	return fmt.Sprintf("Color: %s in %dm", currentColor, int(diff.Minutes()))
}

func GetUptimeStatus(s *discordgo.Session) string {
	uptime := time.Since(StartTime)
	return fmt.Sprintf("Uptime: %dh %dm", int(uptime.Hours()), int(uptime.Minutes())%60)
}

func GetLatencyStatus(s *discordgo.Session) string {
	ping := s.HeartbeatLatency()
	if ping == 0 {
		return ""
	}
	return fmt.Sprintf("Ping: %dms", ping.Milliseconds())
}

func GetTimeStatus(s *discordgo.Session) string {
	return "Time: " + time.Now().UTC().Format("15:04") + " UTC"
}
