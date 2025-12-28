package cmd

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/src/sys"
	"github.com/sho0pi/naturaltime"
)

// Reminder command shared utilities
var reminderParser *naturaltime.Parser

func initReminderParser() {
	var err error
	reminderParser, err = naturaltime.New()
	if err != nil {
		sys.LogFatal("Failed to initialize naturaltime parser: %v", err)
	}
}

func reminderRespondWithV2Container(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	container := sys.NewV2Container(sys.NewTextDisplay(content))
	if err := sys.EditInteractionV2(s, i.Interaction, container); err != nil {
		sys.LogReminder("Error editing interaction response: %v", err)
	}
}

func formatReminderRelativeTime(from, to time.Time) string {
	duration := to.Sub(from)

	if duration < time.Minute {
		return "in less than a minute"
	}

	if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "in 1 minute"
		}
		return fmt.Sprintf("in %d minutes", minutes)
	}

	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "in 1 hour"
		}
		return fmt.Sprintf("in %d hours", hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return "in 1 day"
	}
	if days < 7 {
		return fmt.Sprintf("in %d days", days)
	}

	weeks := days / 7
	if weeks == 1 {
		return "in 1 week"
	}
	if weeks < 4 {
		return fmt.Sprintf("in %d weeks", weeks)
	}

	months := days / 30
	if months == 1 {
		return "in 1 month"
	}
	if months < 12 {
		return fmt.Sprintf("in %d months", months)
	}

	years := days / 365
	if years == 1 {
		return "in 1 year"
	}
	return fmt.Sprintf("in %d years", years)
}

func reminderTruncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func RegisterReminderHandlers() {
	initReminderParser()

	// Register reminder command
	sys.RegisterCommand(&discordgo.ApplicationCommand{
		Name:        "reminder",
		Description: "Manage reminders",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "set",
				Description: "Set a new reminder",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message",
						Description: "The reminder message",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "when",
						Description: "When to remind (e.g., 'tomorrow', 'in 1 week', 'next friday at 3pm')",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "sendto",
						Description: "Where to send the reminder",
						Required:    false,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "This Channel (Default)", Value: "channel"},
							{Name: "Direct Message", Value: "dm"},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List and dismiss reminders",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "dismiss",
						Description:  "Select a reminder to dismiss",
						Required:     false,
						Autocomplete: true,
					},
				},
			},
		},
	}, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if len(options) == 0 {
			return
		}

		switch options[0].Name {
		case "set":
			handleReminderSet(s, i, options[0].Options)
		case "list":
			handleReminderList(s, i, options[0].Options)
		}
	})

	// Register autocomplete handler
	sys.RegisterAutocompleteHandler("reminder", handleReminderAutocomplete)
}
