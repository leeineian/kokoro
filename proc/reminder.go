package proc

import (
	"context"
	"fmt"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/leeineian/minder/sys"
)

var reminderSchedulerRunning = false

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogReminder, func(ctx context.Context) (bool, func()) { return StartReminderScheduler(ctx, client) })
	})
}

// StartReminderScheduler starts the reminder scheduler daemon
func StartReminderScheduler(ctx context.Context, client *bot.Client) (bool, func()) {
	if reminderSchedulerRunning {
		return false, nil
	}
	reminderSchedulerRunning = true

	count, _ := sys.GetRemindersCount(ctx)
	if count == 0 {
		return false, nil
	}

	return true, func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				checkAndSendReminders(ctx, client)
			case <-ctx.Done():
				return
			}
		}
	}
}

func checkAndSendReminders(parentCtx context.Context, client *bot.Client) {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	// Atomically fetch and delete due reminders to prevent race conditions
	reminders, err := sys.ClaimDueReminders(ctx)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQueryDue, err)
		return
	}

	for _, r := range reminders {
		// Send reminder
		go sendReminder(parentCtx, client, r)
	}
}

func sendReminder(parentCtx context.Context, client *bot.Client, r *sys.Reminder) {
	// IDs are already snowflake.ID types
	channelID := r.ChannelID
	userID := r.UserID

	if channelID == 0 || userID == 0 {
		sys.LogReminder("Invalid IDs for reminder %d. Skipping.", r.ID)
		return
	}

	// 2. Build the reminder message with V2 components
	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", userID, r.Message)
	targetChannelID := channelID

	if r.SendTo == "dm" {
		// Create DM channel
		dmChannel, dmErr := client.Rest.CreateDMChannel(userID, rest.WithCtx(parentCtx))
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

	_, err := client.Rest.CreateMessage(targetChannelID, builder.Build(), rest.WithCtx(parentCtx))

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToSend, r.ID, err)
		// Option: Re-insert into DB if we wanted "at least once" retry logic,
		// but removing the race condition was the primary goal.
		return
	}

	sys.LogReminder(sys.MsgReminderSentAndDeleted, r.ID, userID)
}
