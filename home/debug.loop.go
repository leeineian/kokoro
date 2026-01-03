package home

import (
	"context"
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleDebugWebhookLooper routes webhook looper subcommands
func handleDebugWebhookLooper(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, subCmd string) {
	switch subCmd {
	case "list":
		handleLoopList(event)
	case "set":
		handleLoopSet(event, data)
	case "start":
		handleLoopStart(event, data)
	case "stop":
		handleLoopStop(event, data)
	case "purge":
		handleLoopPurge(event, data)
	default:
		loopRespond(event, "Unknown subcommand", true)
	}
}

func loopRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		).
		SetEphemeral(ephemeral).
		Build())
}

// handleLoopList lists configured loop channels
func handleLoopList(event *events.ApplicationCommandInteractionCreate) {
	configs, err := sys.GetAllLoopConfigs(context.Background())
	if err != nil {
		loopRespond(event, fmt.Sprintf("❌ Error loading configs: %v", err), true)
		return
	}

	if len(configs) == 0 {
		loopRespond(event, "ℹ️ No channels/categories are currently configured.", true)
		return
	}

	activeLoops := proc.GetActiveLoops()

	var description string
	for _, cfg := range configs {
		typeIcon := "💬"
		if cfg.ChannelType == "category" {
			typeIcon = "📁"
		}

		intervalStr := proc.FormatInterval(proc.IntervalMsToDuration(cfg.Interval))

		var status string
		if state, running := activeLoops[cfg.ChannelID]; running {
			status = fmt.Sprintf("🟢 Running (Round %d/%d)", state.CurrentRound, state.RoundsTotal)
		} else {
			status = "🟠 Configured (Ready)"
		}

		description += fmt.Sprintf("%s **%s** - Interval: %s\n└ %s\n\n", typeIcon, cfg.ChannelName, intervalStr, status)
	}

	embed := discord.NewEmbedBuilder().
		SetTitle("📋 Loop Configurations").
		SetDescription(description).
		SetColor(0x5865F2).
		Build()

	// Build select menu for deletion
	var selectOptions []discord.StringSelectMenuOption
	for _, cfg := range configs {
		emoji := "💬"
		if cfg.ChannelType == "category" {
			emoji = "📁"
		}
		intervalStr := proc.FormatInterval(proc.IntervalMsToDuration(cfg.Interval))
		selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
			debugTruncate(cfg.ChannelName, 100),
			cfg.ChannelID.String(),
		).WithDescription(fmt.Sprintf("%s • Interval: %s", cfg.ChannelType, intervalStr)).
			WithEmoji(discord.ComponentEmoji{Name: emoji}))
	}

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetEphemeral(true).
		SetEmbeds(embed).
		AddActionRow(
			discord.NewStringSelectMenu("delete_loop_config", "Select a configuration to delete", selectOptions...),
		).
		Build())
}

// handleLoopSet configures a channel for looping
func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelID := data.Snowflake("channel")

	channel, ok := event.Client().Caches.Channel(channelID)
	if !ok {
		loopRespond(event, "❌ Failed to fetch channel.", true)
		return
	}

	// Determine channel type
	channelType := "channel"
	if channel.Type() == discord.ChannelTypeGuildCategory {
		channelType = "category"
	}

	message := "@everyone"
	if msg, ok := data.OptString("message"); ok {
		message = msg
	}

	webhookAuthor := "LoopHook"
	if author, ok := data.OptString("webhook_author"); ok {
		webhookAuthor = author
	}

	webhookAvatar := ""
	if avatar, ok := data.OptString("webhook_avatar"); ok {
		webhookAvatar = avatar
	}

	activeChannelName := ""
	if name, ok := data.OptString("active_name"); ok {
		activeChannelName = name
	}

	inactiveChannelName := ""
	if name, ok := data.OptString("inactive_name"); ok {
		inactiveChannelName = name
	}

	config := &sys.LoopConfig{
		ChannelID:           channelID,
		ChannelName:         channel.Name(),
		ChannelType:         channelType,
		Rounds:              0,
		Interval:            0, // Default to infinite random mode
		ActiveChannelName:   activeChannelName,
		InactiveChannelName: inactiveChannelName,
		Message:             message,
		WebhookAuthor:       webhookAuthor,
		WebhookAvatar:       webhookAvatar,
		UseThread:           false,
	}

	if err := sys.AddLoopConfig(context.Background(), channelID, config); err != nil {
		loopRespond(event, fmt.Sprintf("❌ Failed to save configuration: %v", err), true)
		return
	}

	typeStr := "Channel"
	if channelType == "category" {
		typeStr = "Category"
	}

	loopRespond(event, fmt.Sprintf(
		"✅ **%s Configured**\n> **%s**\n> Interval: ∞ (Random)\n> Run `/debug loop start` to begin.",
		typeStr, channel.Name(),
	), true)
}

// handleLoopStart starts loop(s)
func handleLoopStart(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID, intervalStr string

	if t, ok := data.OptString("target"); ok {
		targetID = t
	}
	if i, ok := data.OptString("interval"); ok {
		intervalStr = i
	}

	interval := proc.IntervalMsToDuration(0)
	if intervalStr != "" {
		parsed, err := proc.ParseIntervalString(intervalStr)
		if err != nil {
			loopRespond(event, fmt.Sprintf("❌ Invalid interval: %v", err), true)
			return
		}
		interval = parsed
	}

	if targetID == "all" || targetID == "" {
		// Start all configured loops
		configs, _ := sys.GetAllLoopConfigs(context.Background())
		if len(configs) == 0 {
			loopRespond(event, "❌ No channels configured! Use `/debug loop set` first.", true)
			return
		}

		started := 0
		for _, cfg := range configs {
			err := proc.StartLoop(event.Client(), cfg.ChannelID, interval)
			if err == nil {
				started++
			}
		}

		loopRespond(event, fmt.Sprintf("🚀 Started **%d** loop(s).", started), true)
	} else {
		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, "❌ Invalid selection.", true)
			return
		}

		err = proc.StartLoop(event.Client(), tID, interval)
		if err != nil {
			loopRespond(event, fmt.Sprintf("❌ Failed to start: %v", err), true)
			return
		}

		loopRespond(event, "🚀 Loop started!", true)
	}
}

// handleLoopStop stops loop(s)
func handleLoopStop(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID string

	if t, ok := data.OptString("target"); ok {
		targetID = t
	}

	if targetID == "all" {
		activeLoops := proc.GetActiveLoops()
		if len(activeLoops) == 0 {
			loopRespond(event, "ℹ️ No loops are currently running.", true)
			return
		}

		stopped := 0
		for channelID := range activeLoops {
			if proc.StopLoopInternal(channelID, event.Client()) {
				stopped++
			}
		}

		loopRespond(event, fmt.Sprintf("🛑 Stopped **%d** loop(s).", stopped), true)
	} else if targetID != "" {
		tID, err := snowflake.Parse(targetID)
		if err == nil && proc.StopLoopInternal(tID, event.Client()) {
			loopRespond(event, "✅ Stopped the selected loop.", true)
		} else {
			loopRespond(event, "❌ Could not find or stop the loop.", true)
		}
	} else {
		// Show selection UI
		activeLoops := proc.GetActiveLoops()
		if len(activeLoops) == 0 {
			loopRespond(event, "ℹ️ No loops are currently running.", true)
			return
		}

		var selectOptions []discord.StringSelectMenuOption
		configs, _ := sys.GetAllLoopConfigs(context.Background())

		selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
			"🛑 Stop All",
			"all",
		).WithDescription(fmt.Sprintf("Stop all %d running loops", len(activeLoops))))

		for _, cfg := range configs {
			if state, running := activeLoops[cfg.ChannelID]; running {
				emoji := "💬"
				if cfg.ChannelType == "category" {
					emoji = "📁"
				}
				selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
					cfg.ChannelName,
					cfg.ChannelID.String(),
				).WithDescription(fmt.Sprintf("Round %d/%d", state.CurrentRound, state.RoundsTotal)).
					WithEmoji(discord.ComponentEmoji{Name: emoji}))
			}
		}

		_ = event.CreateMessage(discord.NewMessageCreateBuilder().
			SetEphemeral(true).
			SetContent("**Active Loops:**\n\nSelect a loop to stop.").
			AddActionRow(
				discord.NewStringSelectMenu("stop_loop_select", "Select loop(s) to stop", selectOptions...),
			).
			Build())
	}
}

// handleLoopPurge purges webhooks from a category
func handleLoopPurge(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	categoryID := data.Snowflake("category")

	category, ok := event.Client().Caches.Channel(categoryID)
	if !ok || category.Type() != discord.ChannelTypeGuildCategory {
		loopRespond(event, "❌ Invalid category.", true)
		return
	}

	guildID := category.GuildID()

	totalDeleted := 0
	for ch := range event.Client().Caches.Channels() {
		if ch.GuildID() != guildID {
			continue
		}
		if textCh, ok := ch.(discord.GuildTextChannel); ok {
			if textCh.ParentID() != nil && *textCh.ParentID() == categoryID {
				webhooks, err := event.Client().Rest.GetWebhooks(textCh.ID())
				if err != nil {
					continue
				}

				for _, wh := range webhooks {
					if wh.Name() == proc.LoopWebhookName {
						_ = event.Client().Rest.DeleteWebhook(wh.ID())
						totalDeleted++
					}
				}
			}
		}
	}

	loopRespond(event, fmt.Sprintf("✅ **Purge Complete**\n\nDeleted **%d** webhook(s) from **%s**.", totalDeleted, category.Name()), true)
}

// debugWebhookLooperAutocomplete provides autocomplete for webhook looper commands
func debugWebhookLooperAutocomplete(event *events.AutocompleteInteractionCreate, subCmd string, focusedOpt string) {
	var choices []discord.AutocompleteChoice

	switch subCmd {
	case "start":
		configs, _ := sys.GetAllLoopConfigs(context.Background())
		activeLoops := proc.GetActiveLoops()

		// Add "all" option if there are multiple configs and it matches the filter
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(strings.ToLower("start all configured loops"), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  "🚀 Start All Configured Loops",
					Value: "all",
				})
			}
		}

		for _, data := range configs {
			_, isRunning := activeLoops[data.ChannelID]
			status := "⚪ (Idle)"
			if isRunning {
				status = "🟢 (Running)"
			}

			// Filter by channel name
			if focusedOpt == "" || strings.Contains(strings.ToLower(data.ChannelName), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf("🚀 Start Loop: %s %s", data.ChannelName, status),
					Value: data.ChannelID.String(),
				})
			}
		}

	case "stop":
		activeLoops := proc.GetActiveLoops()
		configs, _ := sys.GetAllLoopConfigs(context.Background())

		// Add "all" option if there are multiple running loops and it matches the filter
		if len(activeLoops) > 1 {
			if focusedOpt == "" || strings.Contains(strings.ToLower("stop all running loops"), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  "🛑 Stop All Running Loops",
					Value: "all",
				})
			}
		}

		for channelID := range activeLoops {
			name := channelID.String()
			for _, cfg := range configs {
				if cfg.ChannelID == channelID {
					name = cfg.ChannelName
					break
				}
			}

			// Filter by channel name
			if focusedOpt == "" || strings.Contains(strings.ToLower(name), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf("🛑 Stop Loop: %s", name),
					Value: channelID.String(),
				})
			}
		}
	}

	// Limit to 25
	if len(choices) > 25 {
		choices = choices[:25]
	}

	event.AutocompleteResult(choices)
}

// handleDebugLoopConfigDelete handles the delete_loop_config select menu
func handleDebugLoopConfigDelete(event *events.ComponentInteractionCreate) {
	data := event.StringSelectMenuInteractionData()
	if len(data.Values) == 0 {
		return
	}

	channelID := data.Values[0]
	cID, err := snowflake.Parse(channelID)
	if err != nil {
		return
	}

	config, _ := sys.GetLoopConfig(context.Background(), cID)
	configName := "Unknown"
	if config != nil {
		configName = config.ChannelName
	}

	if err := proc.DeleteLoopConfig(cID, event.Client()); err != nil {
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetContent("❌ Failed to delete configuration.").
			Build())
		return
	}

	_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetContent(fmt.Sprintf("✅ Deleted configuration for **%s**.", configName)).
		Build())
}

// handleDebugStopLoopSelect handles the stop_loop_select select menu
func handleDebugStopLoopSelect(event *events.ComponentInteractionCreate) {
	data := event.StringSelectMenuInteractionData()
	if len(data.Values) == 0 {
		return
	}

	selection := data.Values[0]

	if selection == "all" {
		activeLoops := proc.GetActiveLoops()
		stopped := 0
		for channelID := range activeLoops {
			if proc.StopLoopInternal(channelID, event.Client()) {
				stopped++
			}
		}

		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetContent(fmt.Sprintf("🛑 Stopped all **%d** running loops.", stopped)).
			Build())
	} else {
		cID, err := snowflake.Parse(selection)
		success := err == nil && proc.StopLoopInternal(cID, event.Client())
		msg := "❌ Could not find or stop the selected loop."
		if success {
			msg = "✅ Stopped the selected loop."
		}

		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetContent(msg).
			Build())
	}
}

// Unused imports removed - these helpers are exported from proc package
var _ bot.Client
var _ snowflake.ID
