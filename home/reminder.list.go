package home

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleReminderList(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var dismissID string
	if len(options) > 0 {
		dismissID = options[0].StringValue()
	}

	userID := i.Member.User.ID

	// Handle dismissal
	if dismissID != "" {
		if dismissID == "all" {
			// Delete all reminders for this user
			rowsAffected, err := sys.DeleteAllRemindersForUser(userID)
			if err != nil {
				sys.LogReminder(sys.MsgReminderFailedToDeleteAll, err)
				reminderRespondImmediate(s, i, sys.ErrReminderDismissAllFail)
				return
			}

			reminderRespondImmediate(s, i, fmt.Sprintf("✅ Dismissed all %d reminder(s)!", rowsAffected))
			return
		} else {
			// Delete specific reminder
			var intID int64
			fmt.Sscanf(dismissID, "%d", &intID)

			deleted, err := sys.DeleteReminder(intID, userID)
			if err != nil {
				sys.LogReminder(sys.MsgReminderFailedToDeleteGeneral, err)
				reminderRespondImmediate(s, i, sys.ErrReminderDismissFailed)
				return
			}
			if !deleted {
				reminderRespondImmediate(s, i, "❌ Reminder not found or already dismissed.")
				return
			}
			reminderRespondImmediate(s, i, sys.MsgReminderDismissed)
			return
		}
	}

	// List all reminders
	reminders, err := sys.GetRemindersForUser(userID)

	if err != nil {
		sys.LogReminder(sys.MsgReminderFailedToQuery, err)
		reminderRespondImmediate(s, i, sys.ErrReminderFetchFailed)
		return
	}

	if len(reminders) == 0 {
		reminderRespondImmediate(s, i, sys.MsgReminderNoActive)
		return
	}

	// Build response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📝 **You have %d active reminder(s):**\n\n", len(reminders)))

	for idx, r := range reminders {
		location := fmt.Sprintf("<#%s>", r.ChannelID)
		if r.SendTo == "dm" {
			location = "Direct Message"
		}

		// Use Discord timestamp for relative time
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   📍 %s | ⏰ <t:%d:R>\n\n",
			idx+1,
			reminderTruncate(r.Message, 50),
			location,
			r.RemindAt.Unix(),
		))
	}

	sb.WriteString("\n💡 Use `/reminder list dismiss:<reminder>` to dismiss a specific reminder or all reminders.")

	reminderRespondImmediate(s, i, sb.String())
}

func handleReminderAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Find the focused option
	var focusedOption *discordgo.ApplicationCommandInteractionDataOption
	for _, opt := range data.Options {
		if opt.Name == "list" {
			for _, subOpt := range opt.Options {
				if subOpt.Focused {
					focusedOption = subOpt
					break
				}
			}
		}
	}

	if focusedOption == nil {
		return
	}

	go func() {
		userID := i.Member.User.ID

		// Query reminders
		reminders, err := sys.GetRemindersForUser(userID)

		if err != nil {
			sys.LogReminder(sys.MsgReminderAutocompleteFailed, err)
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionApplicationCommandAutocompleteResult,
				Data: &discordgo.InteractionResponseData{
					Choices: []*discordgo.ApplicationCommandOptionChoice{},
				},
			})
			return
		}

		choices := []*discordgo.ApplicationCommandOptionChoice{}

		// Add "Dismiss All" option first
		if len(reminders) > 0 {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  "❌ Dismiss All",
				Value: "all",
			})
		}

		for idx, r := range reminders {
			if idx >= 24 { // Discord limit is 25
				break
			}

			// For autocomplete, use readable relative format (Discord timestamps don't render in autocomplete)
			relativeTime := formatReminderRelativeTime(time.Now(), r.RemindAt)

			locationLabel := "Channel"
			if r.SendTo == "dm" {
				locationLabel = "DM"
			} else {
				if ch, err := s.State.Channel(r.ChannelID); err == nil {
					locationLabel = "#" + ch.Name
				} else if ch, err := s.Channel(r.ChannelID); err == nil {
					locationLabel = "#" + ch.Name
				}
			}

			choiceName := fmt.Sprintf("%s | %s | %s",
				reminderTruncate(r.Message, 30),
				locationLabel,
				relativeTime,
			)

			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  choiceName,
				Value: fmt.Sprintf("%d", r.ID),
			})
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{
				Choices: choices,
			},
		})
	}()
}
