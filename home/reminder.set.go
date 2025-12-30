package home

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleReminderSet(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	// Defer response with ComponentsV2 flag
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsIsComponentsV2,
		},
	})

	go func() {
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
			reminderRespondWithV2Container(s, i, "❌ Failed to parse the date/time. Try formats like 'tomorrow', 'in 2 hours', 'next friday at 3pm'.")
			return
		}

		remindAt := *result
		if remindAt.Before(now) {
			reminderRespondWithV2Container(s, i, "❌ The reminder time must be in the future!")
			return
		}

		// Save to database
		channelID := i.ChannelID
		guildID := i.GuildID

		_, err = sys.DB.Exec(`
			INSERT INTO reminders (user_id, channel_id, guild_id, message, remind_at, send_to)
			VALUES (?, ?, ?, ?, ?, ?)
		`, i.Member.User.ID, channelID, guildID, message, remindAt, sendTo)

		if err != nil {
			sys.LogReminder("Failed to save reminder: %v", err)
			reminderRespondWithV2Container(s, i, "❌ Failed to save reminder. Please try again.")
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

		reminderRespondWithV2Container(s, i, responseText)
	}()
}
