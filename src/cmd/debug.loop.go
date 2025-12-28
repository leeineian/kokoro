package cmd

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/src/proc"
	"github.com/leeineian/minder/src/sys"
)

// handleDebugWebhookLooper routes webhook looper subcommands
func handleDebugWebhookLooper(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(options) == 0 {
		return
	}

	subCommand := options[0]

	switch subCommand.Name {
	case "list":
		handleLoopList(s, i)
	case "set":
		handleLoopSet(s, i, subCommand.Options)
	case "start":
		handleLoopStart(s, i, subCommand.Options)
	case "stop":
		handleLoopStop(s, i, subCommand.Options)
	case "purge":
		handleLoopPurge(s, i, subCommand.Options)
	default:
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("Unknown subcommand")), true)
	}
}

// handleLoopList lists configured loop channels
func handleLoopList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	configs, err := sys.GetAllLoopConfigs()
	if err != nil {
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay(fmt.Sprintf("❌ Error loading configs: %v", err)),
		), true)
		return
	}

	if len(configs) == 0 {
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay("ℹ️ No channels/categories are currently configured."),
		), true)
		return
	}

	activeLoops := proc.GetActiveLoops()

	var lines []string
	for _, cfg := range configs {
		typeIcon := "💬"
		if cfg.ChannelType == "category" {
			typeIcon = "📁"
		}

		intervalStr := proc.FormatInterval(proc.IntervalMsToDuration(cfg.Interval))

		var status string
		if state, running := activeLoops[cfg.ChannelID]; running {
			status = fmt.Sprintf("🟢 **Running** (Round %d/%d)", state.CurrentRound, state.RoundsTotal)
		} else {
			status = "🟠 **Configured** (Ready)"
		}

		lines = append(lines, fmt.Sprintf("%s **%s** - Interval: %s\n    %s", typeIcon, cfg.ChannelName, intervalStr, status))
	}

	// Build select menu for deletion
	var selectOptions []discordgo.SelectMenuOption
	for _, cfg := range configs {
		emoji := "💬"
		if cfg.ChannelType == "category" {
			emoji = "📁"
		}
		intervalStr := proc.FormatInterval(proc.IntervalMsToDuration(cfg.Interval))
		selectOptions = append(selectOptions, discordgo.SelectMenuOption{
			Label:       debugTruncate(cfg.ChannelName, 100),
			Value:       cfg.ChannelID,
			Description: fmt.Sprintf("%s • Interval: %s", cfg.ChannelType, intervalStr),
			Emoji: &discordgo.ComponentEmoji{
				Name: emoji,
			},
		})
	}

	content := "**Loop Configurations:**\n\n"
	for _, line := range lines {
		content += line + "\n\n"
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							CustomID:    "delete_loop_config",
							Placeholder: "Select a configuration to delete",
							Options:     selectOptions,
						},
					},
				},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleLoopSet configures a channel for looping
func handleLoopSet(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	// Parse options
	var channelID, activeChannelName, inactiveChannelName, message, webhookAuthor, webhookAvatar string

	for _, opt := range options {
		switch opt.Name {
		case "channel":
			channelID = opt.ChannelValue(s).ID
		case "active_name":
			activeChannelName = opt.StringValue()
		case "inactive_name":
			inactiveChannelName = opt.StringValue()
		case "message":
			message = opt.StringValue()
		case "webhook_author":
			webhookAuthor = opt.StringValue()
		case "webhook_avatar":
			webhookAvatar = opt.StringValue()
		}
	}

	if channelID == "" {
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay("❌ Please select a channel."),
		), true)
		return
	}

	// Defer reply
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		channel, err := s.Channel(channelID)
		if err != nil {
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay("❌ Failed to fetch channel."),
			))
			return
		}

		// Determine channel type
		channelType := "channel"
		if channel.Type == discordgo.ChannelTypeGuildCategory {
			channelType = "category"
		}

		if message == "" {
			message = "@everyone"
		}
		if webhookAuthor == "" {
			webhookAuthor = "LoopHook"
		}
		if webhookAvatar == "" && i.GuildID != "" {
			guild, _ := s.Guild(i.GuildID)
			if guild != nil && guild.Icon != "" {
				webhookAvatar = guild.IconURL("128")
			}
		}

		config := &sys.LoopConfig{
			ChannelID:           channelID,
			ChannelName:         channel.Name,
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

		if err := proc.SetLoopConfig(s, channelID, config); err != nil {
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay(fmt.Sprintf("❌ Failed to save configuration: %v", err)),
			))
			return
		}

		typeStr := "Channel"
		if channelType == "category" {
			typeStr = "Category"
		}

		sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay(fmt.Sprintf(
				"✅ **%s Configured**\n> **%s**\n> Interval: ∞ (Random)\n> Run `/debug loop start` to begin.",
				typeStr, channel.Name,
			)),
		))
	}()
}

// handleLoopStart starts loop(s)
func handleLoopStart(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var targetID, intervalStr string

	for _, opt := range options {
		switch opt.Name {
		case "target":
			targetID = opt.StringValue()
		case "interval":
			intervalStr = opt.StringValue()
		}
	}

	// Defer reply
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		interval := proc.IntervalMsToDuration(0)
		if intervalStr != "" {
			parsed, err := proc.ParseIntervalString(intervalStr)
			if err != nil {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay(fmt.Sprintf("❌ Invalid interval: %v", err)),
				))
				return
			}
			interval = parsed
		}

		if targetID == "all" || targetID == "" {
			// Start all configured loops
			configs := proc.GetConfiguredChannels()
			if len(configs) == 0 {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("❌ No channels configured! Use `/debug loop set` first."),
				))
				return
			}

			started := 0
			for channelID := range configs {
				err := proc.StartLoop(s, channelID, interval)
				if err == nil {
					started++
				}
			}

			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay(fmt.Sprintf("🚀 Started **%d** loop(s).", started)),
			))
		} else {
			err := proc.StartLoop(s, targetID, interval)
			if err != nil {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay(fmt.Sprintf("❌ Failed to start: %v", err)),
				))
				return
			}

			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay("🚀 Loop started!"),
			))
		}
	}()
}

// handleLoopStop stops loop(s)
func handleLoopStop(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var targetID string

	for _, opt := range options {
		if opt.Name == "target" {
			targetID = opt.StringValue()
		}
	}

	// Defer reply
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		if targetID == "all" {
			activeLoops := proc.GetActiveLoops()
			if len(activeLoops) == 0 {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("ℹ️ No loops are currently running."),
				))
				return
			}

			stopped := 0
			for channelID := range activeLoops {
				if proc.StopLoopInternal(channelID, s) {
					stopped++
				}
			}

			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay(fmt.Sprintf("🛑 Stopped **%d** loop(s).", stopped)),
			))
		} else if targetID != "" {
			if proc.StopLoopInternal(targetID, s) {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("✅ Stopped the selected loop."),
				))
			} else {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("❌ Could not find or stop the loop."),
				))
			}
		} else {
			// Show selection UI
			activeLoops := proc.GetActiveLoops()
			if len(activeLoops) == 0 {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("ℹ️ No loops are currently running."),
				))
				return
			}

			var selectOptions []discordgo.SelectMenuOption
			configs := proc.GetConfiguredChannels()

			selectOptions = append(selectOptions, discordgo.SelectMenuOption{
				Label:       "🛑 Stop All",
				Value:       "all",
				Description: fmt.Sprintf("Stop all %d running loops", len(activeLoops)),
			})

			for channelID, state := range activeLoops {
				cfg := configs[channelID]
				if cfg == nil {
					continue
				}
				emoji := "💬"
				if cfg.Config.ChannelType == "category" {
					emoji = "📁"
				}
				selectOptions = append(selectOptions, discordgo.SelectMenuOption{
					Label:       cfg.Config.ChannelName,
					Value:       channelID,
					Description: fmt.Sprintf("Round %d/%d", state.CurrentRound, state.RoundsTotal),
					Emoji: &discordgo.ComponentEmoji{
						Name: emoji,
					},
				})
			}

			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: debugStrPtr("**Active Loops:**\n\nSelect a loop to stop."),
				Components: &[]discordgo.MessageComponent{
					discordgo.ActionsRow{
						Components: []discordgo.MessageComponent{
							discordgo.SelectMenu{
								CustomID:    "stop_loop_select",
								Placeholder: "Select loop(s) to stop",
								Options:     selectOptions,
							},
						},
					},
				},
			})
		}
	}()
}

// handleLoopPurge purges webhooks from a category
func handleLoopPurge(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	var categoryID string

	for _, opt := range options {
		if opt.Name == "category" {
			categoryID = opt.ChannelValue(s).ID
		}
	}

	if categoryID == "" {
		sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay("❌ Please select a category."),
		), true)
		return
	}

	// Defer reply
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	go func() {
		category, err := s.Channel(categoryID)
		if err != nil || category.Type != discordgo.ChannelTypeGuildCategory {
			sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
				sys.NewTextDisplay("❌ Invalid category."),
			))
			return
		}

		guild, err := s.State.Guild(category.GuildID)
		if err != nil {
			guild, err = s.Guild(category.GuildID)
			if err != nil {
				sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
					sys.NewTextDisplay("❌ Failed to fetch guild."),
				))
				return
			}
		}

		totalDeleted := 0
		for _, ch := range guild.Channels {
			if ch.ParentID != categoryID || ch.Type != discordgo.ChannelTypeGuildText {
				continue
			}

			webhooks, err := s.ChannelWebhooks(ch.ID)
			if err != nil {
				continue
			}

			for _, wh := range webhooks {
				if err := s.WebhookDelete(wh.ID); err == nil {
					totalDeleted++
				}
			}
		}

		sys.EditInteractionV2(s, i.Interaction, sys.NewV2Container(
			sys.NewTextDisplay(fmt.Sprintf("✅ **Purge Complete**\n\nDeleted **%d** webhook(s) from **%s**.", totalDeleted, category.Name)),
		))
	}()
}

// debugWebhookLooperAutocomplete provides autocomplete for webhook looper commands
func debugWebhookLooperAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Navigate to subcommand group
	if len(data.Options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: []*discordgo.ApplicationCommandOptionChoice{}},
		})
		return
	}

	// Find the loop subcommand group
	var subCommandGroup *discordgo.ApplicationCommandInteractionDataOption
	for _, opt := range data.Options {
		if opt.Name == "loop" {
			subCommandGroup = opt
			break
		}
	}

	if subCommandGroup == nil || len(subCommandGroup.Options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{Choices: []*discordgo.ApplicationCommandOptionChoice{}},
		})
		return
	}

	subCommand := subCommandGroup.Options[0]

	var choices []*discordgo.ApplicationCommandOptionChoice

	switch subCommand.Name {
	case "start":
		configs := proc.GetConfiguredChannels()
		activeLoops := proc.GetActiveLoops()

		if len(configs) > 1 {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  "🚀 Start All Configured Loops",
				Value: "all",
			})
		}

		for channelID, data := range configs {
			_, isRunning := activeLoops[channelID]
			status := "⚪ (Idle)"
			if isRunning {
				status = "🟢 (Running)"
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  fmt.Sprintf("🚀 Start Loop: %s %s", data.Config.ChannelName, status),
				Value: channelID,
			})
		}

	case "stop":
		activeLoops := proc.GetActiveLoops()
		configs := proc.GetConfiguredChannels()

		if len(activeLoops) > 1 {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  "🛑 Stop All Running Loops",
				Value: "all",
			})
		}

		for channelID := range activeLoops {
			cfg := configs[channelID]
			name := channelID
			if cfg != nil {
				name = cfg.Config.ChannelName
			}
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
				Name:  fmt.Sprintf("🛑 Stop Loop: %s", name),
				Value: channelID,
			})
		}
	}

	// Limit to 25
	if len(choices) > 25 {
		choices = choices[:25]
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// handleDebugLoopConfigDelete handles the delete_loop_config select menu
func handleDebugLoopConfigDelete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}

	channelID := data.Values[0]

	config, _ := sys.GetLoopConfig(channelID)
	configName := "Unknown"
	if config != nil {
		configName = config.ChannelName
	}

	if err := proc.DeleteLoopConfig(channelID); err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "❌ Failed to delete configuration.",
				Components: []discordgo.MessageComponent{},
			},
		})
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ Deleted configuration for **%s**.", configName),
			Components: []discordgo.MessageComponent{},
		},
	})
}

// handleDebugStopLoopSelect handles the stop_loop_select select menu
func handleDebugStopLoopSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	if len(data.Values) == 0 {
		return
	}

	selection := data.Values[0]

	if selection == "all" {
		activeLoops := proc.GetActiveLoops()
		stopped := 0
		for channelID := range activeLoops {
			if proc.StopLoopInternal(channelID, s) {
				stopped++
			}
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    fmt.Sprintf("🛑 Stopped all **%d** running loops.", stopped),
				Components: []discordgo.MessageComponent{},
			},
		})
	} else {
		success := proc.StopLoopInternal(selection, s)
		msg := "❌ Could not find or stop the selected loop."
		if success {
			msg = "✅ Stopped the selected loop."
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    msg,
				Components: []discordgo.MessageComponent{},
			},
		})
	}
}
