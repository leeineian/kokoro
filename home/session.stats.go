package home

import (
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

func handleSessionStats(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	// Immediate response with loading indicator using V2 components
	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		SetEphemeral(ephemeral).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(sys.MsgSessionStatsLoading),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogDebug(sys.MsgSessionStatsSendFail, err)
		return
	}

	go func() {
		// Calculate Metrics
		interTime := snowflake.ID(event.ID()).Time()
		roundTrip := time.Since(interTime).Milliseconds()

		metrics := getStatsMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
		metrics.Ping = roundTrip

		statsCacheMu.Lock()
		statsCache.Metrics.Data = metrics
		statsCacheMu.Unlock()

		content := renderStatsContent(metrics)

		updateBuilder := discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true)

		updateBuilder.AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
			),
		)

		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), updateBuilder.Build())

		if ephemeral {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			timeout := time.After(5 * time.Minute)

			for {
				select {
				case <-ticker.C:
					liveMetrics := getStatsMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
					liveMetrics.Ping = roundTrip

					newContent := renderStatsContent(liveMetrics)

					newBuilder := discord.NewMessageUpdateBuilder().
						SetIsComponentsV2(true)

					newBuilder.AddComponents(
						discord.NewContainer(
							discord.NewTextDisplay(newContent),
						),
					)

					_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), newBuilder.Build())
				case <-timeout:
					return
				}
			}
		}
	}()
}
