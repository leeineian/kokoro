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
			var startedNames []string
			for _, cfg := range configs {
				if _, ok := activeNow[cfg.ChannelID]; ok {
					// Use latest name if available
					name := cfg.ChannelName
					if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
						name = ch.Name()
					}
					startedNames = append(startedNames, name)
				}
			}

			msg := "❌ No loops were started."
			if len(startedNames) > 0 {
				msg = fmt.Sprintf("Started **%d** loop(s) for: **%s**", len(startedNames), strings.Join(startedNames, "**, **"))
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay("> "+msg))).
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

			name := targetID
			if ch, ok := event.Client().Caches.Channel(tID); ok {
				name = ch.Name()
			}

			msg := fmt.Sprintf("✅ Started loop for: **%s**", name)
			if err != nil {
				msg = fmt.Sprintf("❌ Failed to start **%s**: %v", name, err)
			}

			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().
				SetIsComponentsV2(true).
				AddComponents(discord.NewContainer(discord.NewTextDisplay("> "+msg))).
				Build())
		}()
	}
}
