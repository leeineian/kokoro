package proc

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

var reminderSchedulerRunning = false

func init() {
	sys.OnSessionReady(func(s *discordgo.Session) {
		sys.RegisterDaemon(sys.LogReminder, func() { StartReminderScheduler(s, sys.DB) })
	})
}

// StartReminderScheduler starts the reminder scheduler daemon
func StartReminderScheduler(s *discordgo.Session, db *sql.DB) {
	if reminderSchedulerRunning {
		return
	}
	reminderSchedulerRunning = true

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			checkAndSendReminders(s, db)
		}
	}()
}

func checkAndSendReminders(s *discordgo.Session, db *sql.DB) {
	now := time.Now()

	rows, err := db.Query(`
		SELECT id, user_id, channel_id, message, send_to
		FROM reminders
		WHERE remind_at <= ?
		ORDER BY remind_at ASC
	`, now)

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQueryDue, err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var userID, channelID, message, sendTo string

		err := rows.Scan(&id, &userID, &channelID, &message, &sendTo)
		if err != nil {
			sys.LogReminder(sys.MsgReminderFailedToScan, err)
			continue
		}

		// Send reminder
		go sendReminder(s, db, id, userID, channelID, message, sendTo)
	}
}

func sendReminder(s *discordgo.Session, db *sql.DB, id int64, userID, channelID, message, sendTo string) {
	// Build the reminder message
	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", userID, message)

	var err error
	var targetChannelID = channelID

	if sendTo == "dm" {
		// Create DM channel
		dmChannel, dmErr := s.UserChannelCreate(userID)
		if dmErr != nil {
			sys.LogReminder(sys.MsgReminderFailedToCreateDM, userID, dmErr)
			// Try to send in original channel as fallback
			sendTo = "channel"
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
		sys.LogReminder(sys.MsgReminderFailedToSend, id, err)
		// Don't delete if we can't send - try again next tick
		return
	}

	// Delete the reminder from database
	_, err = db.Exec("DELETE FROM reminders WHERE id = ?", id)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToDelete, id, err)
	} else {
		sys.LogReminder(sys.MsgReminderSentAndDeleted, id, userID)
	}
}
