package proc

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

var reminderSchedulerRunning = false

func init() {
	sys.OnSessionReady(func(s *discordgo.Session) {
		sys.RegisterDaemon(sys.LogReminder, func() { StartReminderScheduler(s) })
	})
}

// StartReminderScheduler starts the reminder scheduler daemon
func StartReminderScheduler(s *discordgo.Session) {
	if reminderSchedulerRunning {
		return
	}
	reminderSchedulerRunning = true

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			checkAndSendReminders(s)
		}
	}()
}

func checkAndSendReminders(s *discordgo.Session) {
	reminders, err := sys.GetDueReminders()
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQueryDue, err)
		return
	}

	for _, r := range reminders {
		// Send reminder
		go sendReminder(s, r)
	}
}

func sendReminder(s *discordgo.Session, r *sys.Reminder) {
	// Build the reminder message
	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", r.UserID, r.Message)

	var err error
	var targetChannelID = r.ChannelID

	if r.SendTo == "dm" {
		// Create DM channel
		dmChannel, dmErr := s.UserChannelCreate(r.UserID)
		if dmErr != nil {
			sys.LogReminder(sys.MsgReminderFailedToCreateDM, r.UserID, dmErr)
			// Try to send in original channel as fallback
		} else {
			targetChannelID = dmChannel.ID
		}
	}

	// Send to channel or DM with native container
	_, err = s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
		Components: []discordgo.MessageComponent{
			&discordgo.Container{
				Components: []discordgo.MessageComponent{
					&discordgo.TextDisplay{Content: reminderText},
				},
			},
		},
		Flags: discordgo.MessageFlagsIsComponentsV2,
	})

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToSend, r.ID, err)
		// Don't delete if we can't send - try again next tick
		return
	}

	// Delete the reminder from database
	err = sys.DeleteReminderByID(r.ID)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToDelete, r.ID, err)
	} else {
		sys.LogReminder(sys.MsgReminderSentAndDeleted, r.ID, r.UserID)
	}
}
