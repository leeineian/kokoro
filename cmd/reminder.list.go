package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleReminderList(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: sys.MessageFlagsIsComponentsV2,
		},
	})

	go func() {
		var dismissID string
		if len(options) > 0 {
			dismissID = options[0].StringValue()
		}

		userID := i.Member.User.ID

		// Handle dismissal
		if dismissID != "" {
			if dismissID == "all" {
				// Delete all reminders for this user
				result, err := sys.DB.Exec("DELETE FROM reminders WHERE user_id = ?", userID)
				if err != nil {
					sys.LogReminder("Failed to delete all reminders: %v", err)
					reminderRespondWithV2Container(s, i, "❌ Failed to dismiss all reminders.")
					return
				}

				rowsAffected, _ := result.RowsAffected()
				reminderRespondWithV2Container(s, i, fmt.Sprintf("✅ Dismissed all %d reminder(s)!", rowsAffected))
				return
			} else {
				// Delete specific reminder
				_, err := sys.DB.Exec("DELETE FROM reminders WHERE id = ? AND user_id = ?", dismissID, userID)
				if err != nil {
					sys.LogReminder("Failed to delete reminder: %v", err)
					reminderRespondWithV2Container(s, i, "❌ Failed to dismiss reminder.")
					return
				}
				reminderRespondWithV2Container(s, i, "✅ Reminder dismissed!")
				return
			}
		}

		// List all reminders
		rows, err := sys.DB.Query(`
			SELECT id, message, remind_at, send_to, channel_id
			FROM reminders
			WHERE user_id = ?
			ORDER BY remind_at ASC
		`, userID)

		if err != nil {
			sys.LogReminder("Failed to query reminders: %v", err)
			reminderRespondWithV2Container(s, i, "❌ Failed to fetch reminders.")
			return
		}
		defer rows.Close()

		var reminders []struct {
			ID        int64
			Message   string
			RemindAt  time.Time
			SendTo    string
			ChannelID string
		}

		for rows.Next() {
			var r struct {
				ID        int64
				Message   string
				RemindAt  time.Time
				SendTo    string
				ChannelID string
			}
			err := rows.Scan(&r.ID, &r.Message, &r.RemindAt, &r.SendTo, &r.ChannelID)
			if err != nil {
				sys.LogReminder("Failed to scan reminder: %v", err)
				continue
			}
			reminders = append(reminders, r)
		}

		if len(reminders) == 0 {
			reminderRespondWithV2Container(s, i, "📭 You have no active reminders.")
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

		reminderRespondWithV2Container(s, i, sb.String())
	}()
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
		rows, err := sys.DB.Query(`
			SELECT id, message, remind_at, send_to, channel_id
			FROM reminders
			WHERE user_id = ?
			ORDER BY remind_at ASC
			LIMIT 25
		`, userID)

		if err != nil {
			sys.LogReminder("Failed to query reminders for autocomplete: %v", err)
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionApplicationCommandAutocompleteResult,
				Data: &discordgo.InteractionResponseData{
					Choices: []*discordgo.ApplicationCommandOptionChoice{},
				},
			})
			return
		}
		defer rows.Close()

		choices := []*discordgo.ApplicationCommandOptionChoice{}

		// Add "Dismiss All" option first
		hasReminders := false
		var tempChoices []*discordgo.ApplicationCommandOptionChoice

		for rows.Next() {
			hasReminders = true
			var id int64
			var message, sendTo, channelID string
			var remindAt time.Time

			err := rows.Scan(&id, &message, &remindAt, &sendTo, &channelID)
			if err != nil {
				continue
			}

			// For autocomplete, use readable relative format (Discord timestamps don't render in autocomplete)
			relativeTime := formatReminderRelativeTime(time.Now(), remindAt)

			locationLabel := "Channel"
			if sendTo == "dm" {
				locationLabel = "DM"
			} else {
				if ch, err := s.State.Channel(channelID); err == nil {
					locationLabel = "#" + ch.Name
				} else if ch, err := s.Channel(channelID); err == nil {
					locationLabel = "#" + ch.Name
				}
			}

			choiceName := fmt.Sprintf("%s | %s | %s",
				reminderTruncate(message, 30),
				locationLabel,
				relativeTime,
			)

			tempChoices = append(tempChoices, &discordgo.ApplicationCommandOptionChoice{
				Name:  choiceName,
				Value: fmt.Sprintf("%d", id),
			})
		}

		if hasReminders {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  "❌ Dismiss All",
				Value: "all",
			})
			choices = append(choices, tempChoices...)
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{
				Choices: choices,
			},
		})
	}()
}
