package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func handleReminder(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	subCmd := data.SubCommandName
	if subCmd == nil {
		return
	}

	switch *subCmd {
	case "stats":
		handleReminderStats(event)
	case "set":
		handleReminderSet(event, data)
	case "list":
		handleReminderList(event, data)
	}
}

func handleReminderSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	message := data.String("message")
	whenStr := data.String("when")
	sendTo := "channel"
	if st, ok := data.OptString("sendto"); ok {
		sendTo = st
	}

	parsedTime, err := parseNaturalTime(whenStr)
	if err != nil {
		reminderRespondImmediate(event, ErrReminderParseFailed)
		return
	}

	if parsedTime.Before(time.Now().UTC()) {
		reminderRespondImmediate(event, ErrReminderPastTime)
		return
	}

	userID := event.User().ID
	channelID := event.Channel().ID()
	var guildID snowflake.ID
	if event.GuildID() != nil {
		guildID = *event.GuildID()
	}

	reminder := &Reminder{
		UserID:    userID,
		ChannelID: channelID,
		GuildID:   guildID,
		Message:   message,
		RemindAt:  parsedTime,
		SendTo:    sendTo,
	}

	if err := hooks.AddReminder(hooks.AppContext, reminder); err != nil {
		hooks.LogInfo(MsgReminderFailedToSave, err)
		reminderRespondImmediate(event, ErrReminderSaveFailed)
		return
	}

	relativeTime := formatReminderRelativeTime(time.Now().UTC(), parsedTime)
	response := fmt.Sprintf(MsgReminderSetSuccess, relativeTime, message)

	reminderRespondImmediate(event, response)
}

func handleReminderList(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	userID := event.User().ID

	if dismissIDStr, ok := data.OptString("dismiss"); ok {
		if dismissIDStr == "all" {
			count, err := hooks.DeleteAllRemindersForUser(hooks.AppContext, userID)
			if err != nil {
				hooks.LogInfo(MsgReminderFailedToDeleteAll, err)
				reminderRespondImmediate(event, ErrReminderDismissAllFail)
				return
			}
			reminderRespondImmediate(event, fmt.Sprintf(MsgReminderDismissedBatch, count))
			return
		}

		dismissID, err := strconv.ParseInt(dismissIDStr, 10, 64)
		if err == nil {
			deleted, err := hooks.DeleteReminder(hooks.AppContext, dismissID, userID)
			if err != nil || !deleted {
				reminderRespondImmediate(event, ErrReminderDismissFailed)
				return
			}
			reminderRespondImmediate(event, MsgReminderDismissed)
			return
		}
	}

	reminders, err := hooks.GetRemindersForUser(hooks.AppContext, userID)
	if err != nil {
		hooks.LogInfo(MsgReminderFailedToQuery, err)
		reminderRespondImmediate(event, ErrReminderFetchFailed)
		return
	}

	if len(reminders) == 0 {
		reminderRespondImmediate(event, MsgReminderNoActive)
		return
	}

	var content strings.Builder
	content.WriteString(fmt.Sprintf(MsgReminderListHeader, len(reminders)))
	for i, r := range reminders {
		relTime := formatReminderRelativeTime(time.Now().UTC(), r.RemindAt)
		content.WriteString(fmt.Sprintf(MsgReminderListItem, i+1, hooks.Truncate(r.Message, 50), relTime))
	}

	reminderRespondImmediate(event, content.String())
}

func handleReminderStats(event *events.ApplicationCommandInteractionCreate) {
	userID := event.User().ID
	reminders, err := hooks.GetRemindersForUser(hooks.AppContext, userID)
	if err != nil {
		reminderRespondImmediate(event, ErrReminderFetchFailed)
		return
	}

	if len(reminders) == 0 {
		reminderRespondImmediate(event, MsgReminderNoActive)
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(MsgReminderStatsHeader, len(reminders)))

	for i, r := range reminders {
		if i >= 5 {
			sb.WriteString(fmt.Sprintf(MsgReminderStatsMore, len(reminders)-5))
			break
		}

		relTime := formatReminderRelativeTime(time.Now().UTC(), r.RemindAt)
		truncatedMsg := hooks.Truncate(r.Message, 50)

		sb.WriteString(fmt.Sprintf("**%d.** \"%s\"\n", i+1, truncatedMsg))
		sb.WriteString(fmt.Sprintf(MsgReminderStatsDue, relTime, r.RemindAt.Format("Jan 02, 15:04")))
		if r.SendTo == "dm" {
			sb.WriteString(MsgReminderStatsDM)
		}
		sb.WriteString("\n")
	}

	err = hooks.RespondInteractionV2(*event.Client(), event, sb.String(), true)
	if err != nil {
		hooks.LogInfo(MsgReminderRespondError, err)
	}
}

func handleReminderAutocomplete(event *events.AutocompleteInteractionCreate) {
	focusedValue := ""
	for _, opt := range event.Data.Options {
		if opt.Focused {
			focusedValue = strings.ToLower(opt.String())
			break
		}
	}

	userID := event.User().ID
	reminders, err := hooks.GetRemindersForUser(hooks.AppContext, userID)
	if err != nil {
		hooks.LogInfo(MsgReminderAutocompleteFailed, err)
		return
	}

	var choices []discord.AutocompleteChoice
	if len(reminders) > 0 {
		if focusedValue == "" || strings.Contains("all", focusedValue) || strings.Contains(strings.ToLower(fmt.Sprintf(MsgReminderChoiceAll, len(reminders))), focusedValue) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  fmt.Sprintf(MsgReminderChoiceAll, len(reminders)),
				Value: "all",
			})
		}
	}

	for _, r := range reminders {
		displayName := hooks.Truncate(r.Message, 80)
		if focusedValue == "" || strings.Contains(strings.ToLower(displayName), focusedValue) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  displayName,
				Value: strconv.FormatInt(r.ID, 10),
			})
		}
		if len(choices) >= 25 {
			break
		}
	}

	event.AutocompleteResult(choices)
}

func reminderRespondImmediate(event *events.ApplicationCommandInteractionCreate, content string) {
	err := hooks.RespondInteractionV2(*event.Client(), event, content, true)
	if err != nil {
		hooks.LogInfo(MsgReminderRespondError, err)
	}
}
