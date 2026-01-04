package home

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

func handleReminderSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	message := data.String("message")
	whenStr := data.String("when")
	sendTo := "channel"
	if st, ok := data.OptString("sendto"); ok {
		sendTo = st
	}

	// Parse the time using naturaltime ParseDate
	parsedTime, err := parseNaturalTime(whenStr)
	if err != nil {
		reminderRespondImmediate(event, sys.ErrReminderParseFailed)
		return
	}

	// Ensure the time is in the future
	if parsedTime.Before(time.Now().UTC()) {
		reminderRespondImmediate(event, sys.ErrReminderPastTime)
		return
	}

	// Get user and channel info
	userID := event.User().ID
	channelID := event.Channel().ID()
	var guildID snowflake.ID
	if event.GuildID() != nil {
		guildID = *event.GuildID()
	}

	// Save the reminder
	reminder := &sys.Reminder{
		UserID:    userID,
		ChannelID: channelID,
		GuildID:   guildID,
		Message:   message,
		RemindAt:  parsedTime,
		SendTo:    sendTo,
	}

	if err := sys.AddReminder(sys.AppContext, reminder); err != nil {
		sys.LogReminder(sys.MsgReminderFailedToSave, err)
		reminderRespondImmediate(event, sys.ErrReminderSaveFailed)
		return
	}

	// Create response
	relativeTime := formatReminderRelativeTime(time.Now().UTC(), parsedTime)
	response := fmt.Sprintf("✅ Reminder set for %s\n\n📝 %s", relativeTime, message)

	reminderRespondImmediate(event, response)
}

// parseNaturalTime parses natural language time expressions
func parseNaturalTime(input string) (time.Time, error) {
	now := time.Now().UTC()

	// Try using naturaltime parser with ParseDate method
	result, err := reminderParser.ParseDate(input, now)
	if err == nil && result != nil {
		return *result, nil
	}

	// Fallback: try to parse duration-like inputs
	if d, err := time.ParseDuration(input); err == nil {
		return now.Add(d), nil
	}

	return time.Time{}, fmt.Errorf("could not parse time: %s", input)
}
