package proc

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/leeineian/minder/sys"
)

var reminderSchedulerRunning = false

func init() {
	sys.OnClientReady(func(client *bot.Client) {
		sys.RegisterDaemon(sys.LogReminder, func() { StartReminderScheduler(client) })
	})
}

// StartReminderScheduler starts the reminder scheduler daemon
func StartReminderScheduler(client *bot.Client) {
	if reminderSchedulerRunning {
		return
	}
	reminderSchedulerRunning = true

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			checkAndSendReminders(client)
		}
	}()
}

func checkAndSendReminders(client *bot.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reminders, err := sys.GetDueReminders(ctx)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQueryDue, err)
		return
	}

	for _, r := range reminders {
		// Send reminder
		go sendReminder(client, r)
	}
}

func sendReminder(client *bot.Client, r *sys.Reminder) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. IDs are already snowflake.ID types
	channelID := r.ChannelID
	userID := r.UserID

	if channelID == 0 {
		sys.LogReminder("Invalid channel ID for reminder %d. Deleting.", r.ID)
		_ = sys.DeleteReminderByID(ctx, r.ID)
		return
	}

	if userID == 0 {
		sys.LogReminder("Invalid user ID for reminder %d. Deleting.", r.ID)
		_ = sys.DeleteReminderByID(ctx, r.ID)
		return
	}

	// 2. Build the reminder message with V2 components
	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", userID, r.Message)
	targetChannelID := channelID

	if r.SendTo == "dm" {
		// Create DM channel
		dmChannel, dmErr := client.Rest.CreateDMChannel(userID)
		if dmErr != nil {
			sys.LogReminder(sys.MsgReminderFailedToCreateDM, userID, dmErr)
			// Fallback: targetChannelID stays as the original channel
		} else {
			targetChannelID = dmChannel.ID()
		}
	}

	// Build message with V2 components (Container + TextDisplay)
	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(reminderText),
			),
		)

	_, err := client.Rest.CreateMessage(targetChannelID, builder.Build())

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToSend, r.ID, err)
		// Don't delete if we can't send - try again next tick
		return
	}

	// 3. Delete the reminder from database on success
	err = sys.DeleteReminderByID(ctx, r.ID)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToDelete, r.ID, err)
	} else {
		sys.LogReminder(sys.MsgReminderSentAndDeleted, r.ID, userID)
	}
}
