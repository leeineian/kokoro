package home

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
)

func handleReminderList(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	userID := event.User().ID

	// Check for dismiss parameter
	if dismissIDStr, ok := data.OptString("dismiss"); ok {
		// Handle dismissal
		if dismissIDStr == "all" {
			count, err := sys.DeleteAllRemindersForUser(context.Background(), userID)
			if err != nil {
				sys.LogReminder(sys.MsgReminderFailedToDeleteAll, err)
				reminderRespondImmediate(event, sys.ErrReminderDismissAllFail)
				return
			}
			reminderRespondImmediate(event, fmt.Sprintf("Dismissed %d reminders!", count))
			return
		}

		dismissID, err := strconv.ParseInt(dismissIDStr, 10, 64)
		if err == nil {
			deleted, err := sys.DeleteReminder(context.Background(), dismissID, userID)
			if err != nil || !deleted {
				reminderRespondImmediate(event, sys.ErrReminderDismissFailed)
				return
			}
			reminderRespondImmediate(event, sys.MsgReminderDismissed)
			return
		}
	}

	// List reminders
	reminders, err := sys.GetRemindersForUser(context.Background(), userID)
	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQuery, err)
		reminderRespondImmediate(event, sys.ErrReminderFetchFailed)
		return
	}

	if len(reminders) == 0 {
		reminderRespondImmediate(event, sys.MsgReminderNoActive)
		return
	}

	// Build reminder list
	var content string
	content = fmt.Sprintf("📋 **Your Reminders** (%d active)\n\n", len(reminders))
	for i, r := range reminders {
		relTime := formatReminderRelativeTime(time.Now(), r.RemindAt)
		content += fmt.Sprintf("%d. **%s** - %s\n", i+1, reminderTruncate(r.Message, 50), relTime)
	}

	reminderRespondImmediate(event, content)
}

func handleReminderAutocomplete(event *events.AutocompleteInteractionCreate) {
	userID := event.User().ID

	reminders, err := sys.GetRemindersForUser(context.Background(), userID)
	if err != nil {
		sys.LogReminder(sys.MsgReminderAutocompleteFailed, err)
		return
	}

	var choices []discord.AutocompleteChoice
	// Add "dismiss all" option if there are reminders
	if len(reminders) > 0 {
		choices = append(choices, discord.AutocompleteChoiceString{
			Name:  fmt.Sprintf("Dismiss All (%d reminders)", len(reminders)),
			Value: "all",
		})
	}

	for _, r := range reminders {
		displayName := reminderTruncate(r.Message, 80)
		choices = append(choices, discord.AutocompleteChoiceString{
			Name:  displayName,
			Value: strconv.FormatInt(r.ID, 10),
		})
		if len(choices) >= 25 {
			break
		}
	}

	event.AutocompleteResult(choices)
}
