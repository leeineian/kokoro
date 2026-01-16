package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/sho0pi/naturaltime"
)

// ===========================
// Command Registration
// ===========================

func init() {
	initReminderParser()

	OnClientReady(func(ctx context.Context, client *bot.Client) {
		RegisterDaemon(LogReminder, func(ctx context.Context) (bool, func(), func()) { return StartReminderScheduler(ctx, client) })
	})

	// Register reminder command
	RegisterCommand(discord.SlashCommandCreate{
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
	}, handleReminder)

	// Register autocomplete handler
	RegisterAutocompleteHandler("reminder", handleReminderAutocomplete)
}

// ===========================
// Reminder System Globals
// ===========================

var (
	reminderSchedulerRunning int32
	reminderParser           *naturaltime.Parser
)

// initReminderParser initializes the natural language time parser
func initReminderParser() {
	var err error
	reminderParser, err = naturaltime.New()
	if err != nil {
		LogFatal(MsgReminderNaturalTimeInitFail, err)
	}
}

// handleReminder routes reminder subcommands to their respective handlers
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

// reminderRespondImmediate sends an ephemeral response message
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
		LogReminder(MsgReminderRespondError, err)
	}
}

// handleReminderSet creates a new reminder for the user
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

	if err := AddReminder(AppContext, reminder); err != nil {
		LogReminder(MsgReminderFailedToSave, err)
		reminderRespondImmediate(event, ErrReminderSaveFailed)
		return
	}

	relativeTime := formatReminderRelativeTime(time.Now().UTC(), parsedTime)
	response := fmt.Sprintf(MsgReminderSetSuccess, relativeTime, message)

	reminderRespondImmediate(event, response)
}

// parseNaturalTime parses natural language time expressions into a time.Time
func parseNaturalTime(input string) (time.Time, error) {
	now := time.Now().UTC()

	result, err := reminderParser.ParseDate(input, now)
	if err == nil && result != nil {
		return *result, nil
	}

	if d, err := time.ParseDuration(input); err == nil {
		return now.Add(d), nil
	}

	return time.Time{}, fmt.Errorf("could not parse time: %s", input)
}

// handleReminderList lists all reminders for the user or dismisses a specific one
func handleReminderList(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	userID := event.User().ID

	if dismissIDStr, ok := data.OptString("dismiss"); ok {
		if dismissIDStr == "all" {
			count, err := DeleteAllRemindersForUser(AppContext, userID)
			if err != nil {
				LogReminder(MsgReminderFailedToDeleteAll, err)
				reminderRespondImmediate(event, ErrReminderDismissAllFail)
				return
			}
			reminderRespondImmediate(event, fmt.Sprintf(MsgReminderDismissedBatch, count))
			return
		}

		dismissID, err := strconv.ParseInt(dismissIDStr, 10, 64)
		if err == nil {
			deleted, err := DeleteReminder(AppContext, dismissID, userID)
			if err != nil || !deleted {
				reminderRespondImmediate(event, ErrReminderDismissFailed)
				return
			}
			reminderRespondImmediate(event, MsgReminderDismissed)
			return
		}
	}

	reminders, err := GetRemindersForUser(AppContext, userID)
	if err != nil {
		LogReminder(MsgReminderFailedToQuery, err)
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
		content.WriteString(fmt.Sprintf(MsgReminderListItem, i+1, Truncate(r.Message, 50), relTime))
	}

	reminderRespondImmediate(event, content.String())
}

// handleReminderStats displays a summary of the user's active reminders
func handleReminderStats(event *events.ApplicationCommandInteractionCreate) {
	userID := event.User().ID
	reminders, err := GetRemindersForUser(AppContext, userID)
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
		truncatedMsg := Truncate(r.Message, 50)

		sb.WriteString(fmt.Sprintf("**%d.** \"%s\"\n", i+1, truncatedMsg))
		sb.WriteString(fmt.Sprintf(MsgReminderStatsDue, relTime, r.RemindAt.Format("Jan 02, 15:04")))
		if r.SendTo == "dm" {
			sb.WriteString(MsgReminderStatsDM)
		}
		sb.WriteString("\n")
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sb.String()),
			),
		).
		SetEphemeral(true)

	err = event.CreateMessage(builder.Build())
	if err != nil {
		LogReminder(MsgReminderRespondError, err)
	}
}

// handleReminderAutocomplete provides autocomplete suggestions for reminder dismissal
func handleReminderAutocomplete(event *events.AutocompleteInteractionCreate) {
	focusedValue := ""
	for _, opt := range event.Data.Options {
		if opt.Focused {
			focusedValue = strings.ToLower(opt.String())
			break
		}
	}

	userID := event.User().ID
	reminders, err := GetRemindersForUser(AppContext, userID)
	if err != nil {
		LogReminder(MsgReminderAutocompleteFailed, err)
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
		displayName := Truncate(r.Message, 80)
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

// formatReminderRelativeTime formats a duration as a human-readable relative time string
func formatReminderRelativeTime(from, to time.Time) string {
	duration := to.Sub(from)

	if duration < time.Minute {
		return MsgReminderRelLessMinute
	}

	if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return MsgReminderRelMinute
		}
		return fmt.Sprintf(MsgReminderRelMinutes, minutes)
	}

	if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return MsgReminderRelHour
		}
		return fmt.Sprintf(MsgReminderRelHours, hours)
	}

	days := int(duration.Hours() / 24)
	if days == 1 {
		return MsgReminderRelDay
	}
	if days < 7 {
		return fmt.Sprintf(MsgReminderRelDays, days)
	}

	weeks := days / 7
	if weeks == 1 {
		return MsgReminderRelWeek
	}
	if weeks < 4 {
		return fmt.Sprintf(MsgReminderRelWeeks, weeks)
	}

	months := days / 30
	if months == 1 {
		return MsgReminderRelMonth
	}
	if months < 12 {
		return fmt.Sprintf(MsgReminderRelMonths, months)
	}

	years := days / 365
	if years == 1 {
		return MsgReminderRelYear
	}
	return fmt.Sprintf(MsgReminderRelYears, years)
}

// StartReminderScheduler starts the reminder scheduler daemon
func StartReminderScheduler(ctx context.Context, client *bot.Client) (bool, func(), func()) {
	if !atomic.CompareAndSwapInt32(&reminderSchedulerRunning, 0, 1) {
		return false, nil, nil
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
		}, func() {
			LogReminder("Shutting down Reminder System...")
		}
}

// checkAndSendReminders checks for due reminders and sends them
func checkAndSendReminders(parentCtx context.Context, client *bot.Client) {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	// Atomically fetch and delete due reminders to prevent race conditions
	reminders, err := ClaimDueReminders(ctx)
	if err != nil {
		LogReminder(MsgReminderFailedToQueryDue, err)
		return
	}

	for _, r := range reminders {
		// Send reminder
		safeGo(func() { sendReminder(parentCtx, client, r) })
	}
}

// sendReminder sends a reminder to the user via DM or channel
func sendReminder(parentCtx context.Context, client *bot.Client, r *Reminder) {
	channelID := r.ChannelID
	userID := r.UserID

	if channelID == 0 || userID == 0 {
		LogReminder("Invalid IDs for reminder %d. Skipping.", r.ID)
		return
	}

	reminderText := fmt.Sprintf("🔔 **Reminder for <@%s>**\n\n%s", userID, r.Message)
	targetChannelID := channelID

	if r.SendTo == "dm" {
		dmChannel, dmErr := client.Rest.CreateDMChannel(userID, rest.WithCtx(parentCtx))
		if dmErr != nil {
			LogReminder(MsgReminderFailedToCreateDM, userID, dmErr)
		} else {
			targetChannelID = dmChannel.ID()
		}
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(reminderText),
			),
		)

	_, err := client.Rest.CreateMessage(targetChannelID, builder.Build(), rest.WithCtx(parentCtx))

	if err != nil {
		LogReminder(MsgReminderFailedToSend, r.ID, err)
		return
	}

	LogReminder(MsgReminderSentAndDeleted, r.ID, userID)
}

// ===========================
// Utilities
// ===========================

// Truncate truncates a string to the specified length with ellipsis at the end.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
