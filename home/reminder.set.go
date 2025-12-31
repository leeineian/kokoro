package home

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleReminderSet(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var message, whenStr, sendTo string
	sendTo = "channel" // default

	for _, opt := range options {
		switch opt.Name {
		case "message":
			message = opt.StringValue()
		case "when":
			whenStr = opt.StringValue()
		case "sendto":
			sendTo = opt.StringValue()
		}
	}

	// Parse the natural language date
	now := time.Now()
	result, err := reminderParser.ParseDate(whenStr, now)
	if err != nil || result == nil {
		reminderRespondImmediate(s, i, sys.ErrReminderParseFailed)
		return
	}

	remindAt := *result
	if remindAt.Before(now) {
		reminderRespondImmediate(s, i, sys.ErrReminderPastTime)
		return
	}

	// Save to database
	err = sys.AddReminder(&sys.Reminder{
		UserID:    i.Member.User.ID,
		ChannelID: i.ChannelID,
		GuildID:   i.GuildID,
		Message:   message,
		RemindAt:  remindAt,
		SendTo:    sendTo,
	})

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToSave, err)
		reminderRespondImmediate(s, i, sys.ErrReminderSaveFailed)
		return
	}

	// Format response
	location := "this channel"
	if sendTo == "dm" {
		location = "your DMs"
	}

	// Use Discord timestamp formatting: <t:UNIX:F> for full date/time, <t:UNIX:R> for relative
	unixTime := remindAt.Unix()
	responseText := fmt.Sprintf("✅ **Reminder set!**\n\n**Message:** %s\n**When:** <t:%d:F> (<t:%d:R>)\n**Where:** %s",
		message,
		unixTime,
		unixTime,
		location,
	)

	reminderRespondImmediate(s, i, responseText)
}
