package home

import (
	"fmt"

	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/proc"
	"github.com/leeineian/minder/sys"
)

// handleLoopErase handles the /loop erase subcommand
func handleLoopErase(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if targetID, ok := data.OptString("target"); ok {
		handleLoopSelect(event, targetID)
		return
	}
}

// handleLoopSelect handles a specific loop configuration (erase)
func handleLoopSelect(event *events.ApplicationCommandInteractionCreate, targetID string) {
	_ = event.DeferCreateMessage(true)

	if targetID == "all" {
		go func() {
			configs, _ := sys.GetAllLoopConfigs(sys.AppContext)
			if len(configs) == 0 {
				loopRespond(event, "ℹ️ No configurations were found to erase.", true)
				return
			}

			count := 0
			for _, cfg := range configs {
				if err := proc.DeleteLoopConfig(sys.AppContext, cfg.ChannelID, event.Client()); err == nil {
					count++
				}
			}

			loopRespond(event, fmt.Sprintf("✅ Erased **%d** configuration(s).", count), true)
		}()
		return
	}

	tID, err := snowflake.Parse(targetID)
	if err != nil {
		loopRespond(event, "❌ Invalid selection.", true)
		return
	}

	go func() {
		cfg, err := sys.GetLoopConfig(sys.AppContext, tID)
		if err != nil || cfg == nil {
			loopRespond(event, "❌ Configuration not found.", true)
			return
		}

		// Try to get latest name from cache
		currentName := cfg.ChannelName
		if channel, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
			currentName = channel.Name()
		}

		err = proc.DeleteLoopConfig(sys.AppContext, tID, event.Client())
		if err != nil {
			loopRespond(event, fmt.Sprintf("❌ Failed to delete configuration for **%s**: %v", currentName, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf("✅ Deleted configuration for **%s**.", currentName), true)
	}()
}
