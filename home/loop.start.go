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
		parsed, err := proc.ParseDuration(durationStr)
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

			var ids []snowflake.ID
			for _, cfg := range configs {
				ids = append(ids, cfg.ChannelID)
			}

			_ = proc.BatchStartLoops(sys.AppContext, event.Client(), ids, duration)

			activeNow := proc.GetActiveLoops()
			started := 0
			for _, id := range ids {
				if _, ok := activeNow[id]; ok {
					started++
				}
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf("Started **%d** loop(s).", started)))).
				Build())
		}()
	} else {
		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, "❌ Invalid selection.", true)
			return
		}

		_ = event.DeferCreateMessage(true)
		go func() {
			err = proc.StartLoop(sys.AppContext, event.Client(), tID, duration)
			msg := "Loop started!"
			if err != nil {
				msg = fmt.Sprintf("❌ Failed to start: %v", err)
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay(msg))).
				Build())
		}()
	}
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
					Name:  "Start All Configured Loops",
					Value: "all",
				})
			}
		}

		for _, data := range configs {
			// Only show configs for the current guild
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				if ch.GuildID() != *event.GuildID() {
					continue
				}
			} else {
				// If not in cache, we can't be sure, but usually we only want to show what's relevant to this interaction
				// For safety, let's keep it if we can't verify guild, or better, skip it.
				continue
			}

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
					Name:  fmt.Sprintf("Start Loop: %s %s", displayName, status),
					Value: data.ChannelID.String(),
				})
			}
		}

	case "set":
		// Only show categories in the current guild
		guildID := *event.GuildID()
		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() == guildID && ch.Type() == discord.ChannelTypeGuildCategory {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf("📁 %s", ch.Name()),
						Value: ch.ID().String(),
					})
				}
			}
		}

	case "stop":
		activeLoops := proc.GetActiveLoops()
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
		guildID := *event.GuildID()

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
			// Check if the loop belongs to the current guild
			var belongsToGuild bool
			if ch, ok := event.Client().Caches.Channel(channelID); ok {
				if ch.GuildID() == guildID {
					belongsToGuild = true
				}
			}
			if !belongsToGuild {
				continue
			}

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
