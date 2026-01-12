package home

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/leeineian/minder/sys"
	"github.com/sho0pi/naturaltime"
)

var reminderParser *naturaltime.Parser

func initReminderParser() {
	var err error
	reminderParser, err = naturaltime.New()
	if err != nil {
		sys.LogFatal(sys.MsgReminderNaturalTimeInitFail, err)
	}
}

func reminderRespondImmediate(event *events.ApplicationCommandInteractionCreate, content string) {
	err := event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(true).
		Build())
	if err != nil {
		sys.LogReminder(sys.MsgReminderRespondError, err)
	}
}

func formatReminderRelativeTime(from, to time.Time) string {
	duration := to.Sub(from)

	if duration < time.Minute {
		return sys.MsgReminderRelLessMinute
	}

	if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return sys.MsgReminderRelMinute
		}
		return fmt.Sprintf(sys.MsgReminderRelMinutes, minutes)
	}

	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return sys.MsgReminderRelHour
		}
		return fmt.Sprintf(sys.MsgReminderRelHours, hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return sys.MsgReminderRelDay
	}
	if days < 7 {
		return fmt.Sprintf(sys.MsgReminderRelDays, days)
	}

	weeks := days / 7
	if weeks == 1 {
		return sys.MsgReminderRelWeek
	}
	if weeks < 4 {
		return fmt.Sprintf(sys.MsgReminderRelWeeks, weeks)
	}

	months := days / 30
	if months == 1 {
		return sys.MsgReminderRelMonth
	}
	if months < 12 {
		return fmt.Sprintf(sys.MsgReminderRelMonths, months)
	}

	years := days / 365
	if years == 1 {
		return sys.MsgReminderRelYear
	}
	return fmt.Sprintf(sys.MsgReminderRelYears, years)
}

func reminderTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func init() {
	initReminderParser()

	// Register reminder command
	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "reminder",
		Description: "Manage reminders",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "set",
				Description: "Set a new reminder",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "message",
						Description: "The reminder message",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "when",
						Description: "When to remind (e.g., 'tomorrow', 'in 1 week', 'next friday at 3pm')",
						Required:    true,
					},
					discord.ApplicationCommandOptionString{
						Name:        "sendto",
						Description: "Where to send the reminder",
						Required:    false,
						Choices: []discord.ApplicationCommandOptionChoiceString{
							{Name: "This Channel (Default)", Value: "channel"},
							{Name: "Direct Message", Value: "dm"},
						},
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "list",
				Description: "List and dismiss reminders",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "dismiss",
						Description:  "Select a reminder to dismiss",
						Required:     false,
						Autocomplete: true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "View a summary of your active reminders",
			},
		},
	}, func(event *events.ApplicationCommandInteractionCreate) {
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
	})

	// Register autocomplete handler
	sys.RegisterAutocompleteHandler("reminder", handleReminderAutocomplete)
}
