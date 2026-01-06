package home

import (
	"fmt"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopList lists configured loop channels
func handleLoopList(event *events.ApplicationCommandInteractionCreate) {
	_ = event.DeferCreateMessage(true)

	go func() {
		configs, err := sys.GetAllLoopConfigs(sys.AppContext)
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
			// Try to get latest name from cache
			currentName := cfg.ChannelName
			if channel, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				if channel.Name() != cfg.ChannelName {
					currentName = channel.Name()
					// Update DB asynchronously
					go func(id snowflake.ID, name string) {
						_ = sys.UpdateLoopChannelName(sys.AppContext, id, name)
					}(cfg.ChannelID, currentName)
				}
			}

			// Always category (loop system is category-only)
			typeIcon := "📁"

			intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(cfg.Interval))

			var status string
			if state, running := activeLoops[cfg.ChannelID]; running {
				if cfg.Interval > 0 {
					status = fmt.Sprintf("🟢 Running (Burst Mode • Round %d)", state.CurrentRound)
				} else {
					status = fmt.Sprintf("🟢 Running (Round %d/%d)", state.CurrentRound, state.RoundsTotal)
				}

				if !state.NextRun.IsZero() {
					// We are waiting for the next random batch
					status += fmt.Sprintf(" (Next: <t:%d:R>)", state.NextRun.Unix())
				} else if !state.EndTime.IsZero() {
					// We are currently in a round or a timed session
					if state.EndTime.After(time.Now().UTC()) {
						status += fmt.Sprintf(" (Ends: <t:%d:R>)", state.EndTime.Unix())
					} else {
						status += " (Finishing...)"
					}
				}
			} else {
				status = "🟠 Configured (Ready)"
			}

			description += fmt.Sprintf("%s **%s** - Duration: %s\n└ %s\n\n", typeIcon, currentName, intervalStr, status)
		}

		// Build the content for the V2 component
		content := "# 📋 Loop Configurations\n\n" + description

		// Build select menu for deletion
		var selectOptions []discord.StringSelectMenuOption
		for _, cfg := range configs {
			// Use cached name if available for the select menu too
			displayName := cfg.ChannelName
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				displayName = ch.Name()
			}

			intervalStr := proc.FormatDuration(proc.IntervalMsToDuration(cfg.Interval))

			// Always category emoji
			selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
				loopTruncate(displayName, 100),
				cfg.ChannelID.String(),
			).WithDescription(fmt.Sprintf("Category • Duration: %s", intervalStr)).
				WithEmoji(discord.ComponentEmoji{Name: "📁"}))
		}

		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(content),
					discord.NewActionRow(
						discord.NewStringSelectMenu("delete_loop_config", "Select a configuration to delete", selectOptions...),
					),
				),
			).
			Build())
	}()
}

// handleDeleteLoopConfig handles the delete_loop_config select menu
func handleDeleteLoopConfig(event *events.ComponentInteractionCreate) {
	data := event.StringSelectMenuInteractionData()
	if len(data.Values) == 0 {
		return
	}

	channelID := data.Values[0]
	cID, err := snowflake.Parse(channelID)
	if err != nil {
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().SetContent("❌ Invalid selection.").Build())
		return
	}

	_ = proc.DeleteLoopConfig(sys.AppContext, cID, event.Client())
	config, _ := sys.GetLoopConfig(sys.AppContext, cID)
	configName := "Unknown"
	if config != nil {
		configName = config.ChannelName
	}

	_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		ClearComponents().
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(fmt.Sprintf("✅ Deleted configuration for **%s**.", configName)),
			),
		).
		Build())
}
