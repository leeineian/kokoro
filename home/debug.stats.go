package home

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

func handleDebugStats(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
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
				discord.NewTextDisplay("⏳ Loading stats..."),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogDebug("Failed to send initial stats: %v", err)
		return
	}

	go func() {
		// Calculate Metrics
		interTime := snowflake.ID(event.ID()).Time()
		roundTrip := time.Since(interTime).Milliseconds()

		metrics := getDebugMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
		metrics.Ping = roundTrip

		debugCacheMu.Lock()
		debugStatsCache.Metrics.Data = metrics
		debugCacheMu.Unlock()

		content := renderDebugStatsContent(metrics)

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
					liveMetrics := getDebugMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
					liveMetrics.Ping = roundTrip

					newContent := renderDebugStatsContent(liveMetrics)

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

func handleDebugPing(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		SetEphemeral(ephemeral).
		AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay("🏓 Pinging..."),
			),
		)

	err := event.CreateMessage(builder.Build())
	if err != nil {
		sys.LogDebug("Failed to send ping: %v", err)
		return
	}

	go func() {
		interTime := snowflake.ID(event.ID()).Time()
		latency := time.Since(interTime).Milliseconds()

		content := fmt.Sprintf("# Pong! 🏓\n\n> **Latency:** %dms", latency)

		updateBuilder := discord.NewMessageUpdateBuilder().
			SetIsComponentsV2(true)

		updateBuilder.AddComponents(
			discord.NewContainer(
				discord.NewTextDisplay(content),
				discord.NewActionRow(
					discord.NewSuccessButton("🔄 Refresh", "ping_refresh"),
				),
			),
		)

		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), updateBuilder.Build())
	}()
}

func handleDebugPingRefresh(event *events.ComponentInteractionCreate) {
	interTime := snowflake.ID(event.ID()).Time()
	latency := time.Since(interTime).Milliseconds()

	content := fmt.Sprintf("# Pong! 🔁\n\n> **Latency:** %dms", latency)

	updateBuilder := discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true)

	updateBuilder.AddComponents(
		discord.NewContainer(
			discord.NewTextDisplay(content),
			discord.NewActionRow(
				discord.NewSuccessButton("🔄 Refresh", "ping_refresh"),
			),
		),
	)

	_ = event.UpdateMessage(updateBuilder.Build())
}

func renderDebugStatsContent(metrics DebugHealthMetrics) string {
	output := getDebugSystemStats() + "\n\n" + getDebugAppStats(metrics)
	return fmt.Sprintf("```ansi\n%s\n```", output)
}

func getDebugSystemStats() string {
	debugCacheMu.RLock()
	if time.Since(debugStatsCache.System.Timestamp) < DebugCacheTTL && debugStatsCache.System.Data != "" {
		defer debugCacheMu.RUnlock()
		return debugStatsCache.System.Data
	}
	debugCacheMu.RUnlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	usedMem := float64(m.HeapAlloc) / 1024 / 1024
	totalMem := float64(m.Sys) / 1024 / 1024

	lines := []string{
		debugTitle("System"),
		fmt.Sprintf("%s %s", debugKey("Platform"), debugVal(fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH))),
		fmt.Sprintf("%s %s", debugKey("Go Version"), debugVal(runtime.Version())),
		fmt.Sprintf("%s %s", debugKey("Memory"), debugVal(fmt.Sprintf("%.2f MB / %.2f MB (Sys)", usedMem, totalMem))),
		fmt.Sprintf("%s %s", debugKey("CPUs"), debugVal(fmt.Sprintf("%d", runtime.NumCPU()))),
		fmt.Sprintf("%s %s", debugKey("Goroutines"), debugVal(fmt.Sprintf("%d", runtime.NumGoroutine()))),
	}
	data := strings.Join(lines, "\n")

	debugCacheMu.Lock()
	debugStatsCache.System = DebugCachedData{Data: data, Timestamp: time.Now()}
	debugCacheMu.Unlock()
	return data
}

func getDebugAppStats(metrics DebugHealthMetrics) string {
	uptime := time.Since(debugStartTime)
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	uptimeStr := fmt.Sprintf("%dd %dh %dm", days, hours, minutes)

	lines := []string{
		debugTitle("App"),
		fmt.Sprintf("%s %s", debugKey("Library"), debugVal("Disgo")),
		fmt.Sprintf("%s %s", debugKey("Uptime"), debugVal(uptimeStr)),
	}

	if metrics.GatewayPing > 0 {
		lines = append(lines, fmt.Sprintf("%s %s", debugKey("Gateway"), debugVal(fmt.Sprintf("%dms", metrics.GatewayPing))))
	}
	if metrics.Ping > 0 {
		lines = append(lines, fmt.Sprintf("%s %s", debugKey("API Latency"), debugVal(fmt.Sprintf("%dms", metrics.Ping))))
	}
	if metrics.DBLatency != "" {
		lines = append(lines, fmt.Sprintf("%s %s", debugKey("Database"), debugVal(metrics.DBLatency+"ms")))
	}

	return strings.Join(lines, "\n")
}

func getDebugMetrics(interactionID string, gatewayLatency int64, includePing bool) DebugHealthMetrics {
	debugCacheMu.RLock()
	if debugStatsCache.Metrics.InteractionID == interactionID && time.Since(debugStatsCache.Metrics.Timestamp) < DebugCacheTTL {
		defer debugCacheMu.RUnlock()
		return debugStatsCache.Metrics.Data
	}
	debugCacheMu.RUnlock()

	metrics := DebugHealthMetrics{}

	if includePing {
		metrics.GatewayPing = gatewayLatency
	}

	start := time.Now()
	_, _ = sys.GetBotConfig(context.Background(), "ping_test")
	metrics.DBLatency = fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)

	debugCacheMu.Lock()
	debugStatsCache.Metrics = DebugCachedMetrics{
		Data:          metrics,
		Timestamp:     time.Now(),
		InteractionID: interactionID,
	}
	debugCacheMu.Unlock()

	return metrics
}
