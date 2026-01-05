package home

import (
	"fmt"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

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
			if proc.StopLoopInternal(sys.AppContext, channelID, event.Client()) {
				stopped++
			}
		}

		loopRespond(event, fmt.Sprintf("🛑 Stopped **%d** loop(s).", stopped), true)
	} else if targetID != "" {
		tID, err := snowflake.Parse(targetID)
		if err == nil && proc.StopLoopInternal(sys.AppContext, tID, event.Client()) {
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
		configs, _ := sys.GetAllLoopConfigs(sys.AppContext)

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

				// Try to get latest name from cache
				displayName := cfg.ChannelName
				if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
					displayName = ch.Name()
				}

				selectOptions = append(selectOptions, discord.NewStringSelectMenuOption(
					displayName,
					cfg.ChannelID.String(),
				).WithDescription(fmt.Sprintf("Round %d/%d", state.CurrentRound, state.RoundsTotal)).
					WithEmoji(discord.ComponentEmoji{Name: emoji}))
			}
		}

		_ = event.CreateMessage(discord.NewMessageCreateBuilder().
			SetIsComponentsV2(true).
			SetEphemeral(true).
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay("# 🛑 Active Loops\nSelect a loop to stop."),
					discord.NewActionRow(
						discord.NewStringSelectMenu("stop_loop_select", "Select loop(s) to stop", selectOptions...),
					),
				),
			).
			Build())
	}
}

// handleStopLoopSelect handles the stop_loop_select select menu
func handleStopLoopSelect(event *events.ComponentInteractionCreate) {
	data := event.StringSelectMenuInteractionData()
	if len(data.Values) == 0 {
		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().SetContent("❌ No selection made.").Build())
		return
	}

	selection := data.Values[0]

	if selection == "all" {
		activeLoops := proc.GetActiveLoops()
		stopped := 0
		for channelID := range activeLoops {
			if proc.StopLoopInternal(sys.AppContext, channelID, event.Client()) {
				stopped++
			}
		}

		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			ClearComponents().
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(fmt.Sprintf("🛑 Stopped all **%d** running loops.", stopped)),
				),
			).
			Build())
	} else {
		cID, err := snowflake.Parse(selection)
		success := err == nil && proc.StopLoopInternal(sys.AppContext, cID, event.Client())
		msg := "❌ Could not find or stop the selected loop."
		if success {
			msg = "✅ Stopped the selected loop."
		}

		_ = event.UpdateMessage(discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true).
			ClearComponents().
			AddComponents(
				discord.NewContainer(
					discord.NewTextDisplay(msg),
				),
			).
			Build())
	}
}
