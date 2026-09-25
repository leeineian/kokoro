package app

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func handleBotStats(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	err := hooks.RespondInteractionV2(*event.Client(), event, MsgBotStatsLoading, ephemeral)
	if err != nil {
		return
	}

	safeGo(func() {
		interTime := snowflake.ID(event.ID()).Time()
		roundTrip := time.Since(interTime).Milliseconds()

		metrics := getStatsMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
		metrics.Ping = roundTrip

		statsCacheMu.Lock()
		statsCache.Metrics.Data = metrics
		statsCacheMu.Unlock()

		content := renderStatsContent(metrics)
		_ = hooks.EditInteractionV2(*event.Client(), event, content)

		if ephemeral {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			timeout := time.After(5 * time.Minute)

			failCount := 0
			for {
				select {
				case <-ticker.C:
					live := getStatsMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)

					// Re-calculate round trip for the update call to keep it somewhat accurate
					startUpdate := time.Now()
					content := renderStatsContent(live)
					err := hooks.EditInteractionV2(*event.Client(), event, content)

					if err != nil {
						failCount++
						if failCount > 3 {
							return
						}
					} else {
						failCount = 0
						// Update the ping for the NEXT display cycle based on this successful update
						live.Ping = time.Since(startUpdate).Milliseconds()
						statsCacheMu.Lock()
						statsCache.Metrics.Data.Ping = live.Ping
						statsCacheMu.Unlock()
					}
				case <-timeout:
					return
				case <-hooks.AppContext.Done():
					return
				}
			}
		}
	})
}

func getStatsMetrics(interactionID string, gatewayLatency int64, includePing bool) StatsHealthMetrics {
	statsCacheMu.RLock()
	if statsCache.Metrics.InteractionID == interactionID && time.Since(statsCache.Metrics.Timestamp) < StatsCacheTTL {
		defer statsCacheMu.RUnlock()
		return statsCache.Metrics.Data
	}
	statsCacheMu.RUnlock()

	metrics := StatsHealthMetrics{}
	if includePing {
		metrics.GatewayPing = gatewayLatency
	}

	start := time.Now().UTC()
	_, _ = hooks.GetAppConfig(hooks.AppContext, "ping_test")
	metrics.DBLatency = fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)

	statsCacheMu.Lock()
	statsCache.Metrics = StatsCachedMetrics{Data: metrics, Timestamp: time.Now().UTC(), InteractionID: interactionID}
	statsCacheMu.Unlock()
	return metrics
}

func renderStatsContent(metrics StatsHealthMetrics) string {
	return fmt.Sprintf("```ansi\n%s\n\n%s\n```", getSystemStats(), getAppStats(metrics))
}

func getSystemStats() string {
	statsCacheMu.RLock()
	if time.Since(statsCache.System.Timestamp) < StatsCacheTTL && statsCache.System.Data != "" {
		defer statsCacheMu.RUnlock()
		return statsCache.System.Data
	}
	statsCacheMu.RUnlock()

	var (
		m runtime.MemStats
	)
	runtime.ReadMemStats(&m)
	usedMem := float64(m.HeapAlloc) / 1024 / 1024
	totalMem := float64(m.Sys) / 1024 / 1024

	data := strings.Join([]string{
		statsTitle("System"),
		fmt.Sprintf("%s %s", statsKey("Platform"), statsVal(fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH))),
		fmt.Sprintf("%s %s", statsKey("Go Version"), statsVal(runtime.Version())),
		fmt.Sprintf("%s %s", statsKey("Memory"), statsVal(fmt.Sprintf("%.2f MB / %.2f MB (Sys)", usedMem, totalMem))),
		fmt.Sprintf("%s %s", statsKey("CPUs"), statsVal(fmt.Sprintf("%d", runtime.NumCPU()))),
		fmt.Sprintf("%s %s", statsKey("Goroutines"), statsVal(fmt.Sprintf("%d", runtime.NumGoroutine()))),
	}, "\n")

	statsCacheMu.Lock()
	statsCache.System = StatsCachedData{Data: data, Timestamp: time.Now().UTC()}
	statsCacheMu.Unlock()
	return data
}

func getAppStats(metrics StatsHealthMetrics) string {
	uptime := time.Since(statsStartTime)
	uptimeStr := fmt.Sprintf("%dd %dh %dm", int(uptime.Hours())/24, int(uptime.Hours())%24, int(uptime.Minutes())%60)
	lines := []string{statsTitle("App"), fmt.Sprintf("%s %s", statsKey("Library"), statsVal("Disgo")), fmt.Sprintf("%s %s", statsKey("Uptime"), statsVal(uptimeStr))}
	if metrics.GatewayPing > 0 {
		lines = append(lines, fmt.Sprintf("%s %s", statsKey("Gateway"), statsVal(fmt.Sprintf("%dms", metrics.GatewayPing))))
	}
	if metrics.Ping > 0 {
		lines = append(lines, fmt.Sprintf("%s %s", statsKey("API Latency"), statsVal(fmt.Sprintf("%dms", metrics.Ping))))
	}
	if metrics.DBLatency != "" {
		lines = append(lines, fmt.Sprintf("%s %s", statsKey("Database"), statsVal(metrics.DBLatency+"ms")))
	}
	return strings.Join(lines, "\n")
}

func statsTitle(t string) string { return StatsAnsiPink + t + StatsAnsiReset }
func statsKey(t string) string   { return StatsAnsiPink + "> " + t + ":" + StatsAnsiReset }
func statsVal(t string) string   { return StatsAnsiPinkBold + t + StatsAnsiReset }
