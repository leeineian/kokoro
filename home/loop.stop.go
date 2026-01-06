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

	// Always defer for loop stop as it might involve multiple database/memory operations
	_ = event.DeferCreateMessage(true)

	if targetID == "all" {
		go func() {
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
		}()
	} else {
		tID, err := snowflake.Parse(targetID)
		go func() {
			if err == nil && proc.StopLoopInternal(sys.AppContext, tID, event.Client()) {
				loopRespond(event, "✅ Stopped the selected loop.", true)
			} else {
				loopRespond(event, "❌ Could not find or stop the loop.", true)
			}
		}()
	}
}
