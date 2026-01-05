package home

import (
	"fmt"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopStart starts loop(s)
func handleLoopStart(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID, durationStr string

	if t, ok := data.OptString("target"); ok {
		targetID = t
	}
	if d, ok := data.OptString("duration"); ok {
		durationStr = d
	}

	duration := proc.IntervalMsToDuration(0)
	if durationStr != "" {
		parsed, err := proc.ParseDurationString(durationStr)
		if err != nil {
			loopRespond(event, fmt.Sprintf("❌ Invalid duration: %v", err), true)
			return
		}
		duration = parsed
	}

	if targetID == "all" {
		// Acknowledge immediately
		_ = event.DeferCreateMessage(true)

		go func() {
			configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
			if len(configs) == 0 {
				_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
					SetIsComponentsV2(true).
					AddComponents(discord.NewContainer(discord.NewTextDisplay("❌ No channels configured!"))).
					Build())
				return
			}

			started := 0
			for _, cfg := range configs {
				if err := proc.StartLoop(sys.AppContext, event.Client(), cfg.ChannelID, duration); err == nil {
					started++
				}
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf("🚀 Started **%d** loop(s).", started)))).
				Build())
		}()
	} else if targetID != "" {
		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, "❌ Invalid selection.", true)
			return
		}

		_ = event.DeferCreateMessage(true)
		go func() {
			err = proc.StartLoop(sys.AppContext, event.Client(), tID, duration)
			msg := "🚀 Loop started!"
			if err != nil {
				msg = fmt.Sprintf("❌ Failed to start: %v", err)
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay(msg))).
				Build())
		}()
	} else {
		// Show selection UI
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
		if len(configs) == 0 {
			loopRespond(event, "❌ No channels configured! Use `/loop set` first.", true)
			return
		}

		var selectOptions []discord.StringSelectMenuOption
		activeLoops := proc.GetActiveLoops()

		if len(configs) > 1 {
			selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
				"🚀 Start All",
				"all",
			).WithDescription(fmt.Sprintf("Start all %d configured loops", len(configs))))
		}

		for _, cfg := range configs {
			_, running := activeLoops[cfg.ChannelID]
			status := "Idle"
			emoji := "💬"
			if running {
				status = "Running"
			}
			if cfg.ChannelType == "category" {
				emoji = "📁"
			}

			// Try to get latest name from cache
			displayName := cfg.ChannelName
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				displayName = ch.Name()
			}

			selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
				displayName,
				cfg.ChannelID.String(),
			).WithDescription(status).
				WithEmoji(discord.ComponentEmoji{Name: emoji}))
		}

		_ = event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			SetEphemeral(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("# 🚀 Start Loop\nSelect a configuration to start."),
					discord.NewActionRow(
						discord.NewStringSelectMenu("start_loop_select", "Select a loop to start", selectOptions...),
					),
				),
			).
			Build())
	}
}

// handleStartLoopSelect handles the start_loop_select select menu
func handleStartLoopSelect(event *events.ComponentInteractionCreate) {
	data := event.StringSelectMenuInteractionData()
	if len(data.Values) == 0 {
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().SetContent("❌ No selection made.").Build())
		return
	}

	selection := data.Values[0]
	duration := proc.IntervalMsToDuration(0) // Default to random for select menu

	// Defer immediately because StartLoop/Webhook prep can be slow
	_ = event.DeferUpdateMessage()

	go func() {
		if selection == "all" {
			configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
			started := 0
			for _, cfg := range configs {
				if err := proc.StartLoop(sys.AppContext, event.Client(), cfg.ChannelID, duration); err == nil {
					started++
				}
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				ClearComponents().
				AddComponents(
					discord.NewContainer(
						discord.NewTextDisplay(fmt.Sprintf("🚀 Started **%d** loop(s).", started)),
					),
				).
				Build())
		} else {
			cID, err := snowflake.Parse(selection)
			if err != nil {
				_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().SetContent("❌ Invalid selection.").Build())
				return
			}

			err = proc.StartLoop(sys.AppContext, event.Client(), cID, duration)
			msg := "🚀 Loop started!"
			if err != nil {
				msg = fmt.Sprintf("❌ Failed to start: %v", err)
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				ClearComponents().
				AddComponents(
					discord.NewContainer(
						discord.NewTextDisplay(msg),
					),
				).
				Build())
		}
	}()
}

// handleWebhookLooperAutocomplete provides autocomplete for webhook looper commands
func handleWebhookLooperAutocomplete(event *events.AutocompleteInteractionCreate, subCmd string, focusedOpt string) {
	var choices []discord.AutocompleteChoice

	switch subCmd {
	case "start":
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
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

			// Try to get latest name from cache
			displayName := data.ChannelName
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				displayName = ch.Name()
			}

			// Filter by channel name
			if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{
					Name:  fmt.Sprintf("🚀 Start Loop: %s %s", displayName, status),
					Value: data.ChannelID.String(),
				})
			}
		}

	case "stop":
		activeLoops := proc.GetActiveLoops()
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)

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
					// Try to get latest name from cache
					if ch, ok := event.Client().Caches.Channel(channelID); ok {
						name = ch.Name()
					}
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
