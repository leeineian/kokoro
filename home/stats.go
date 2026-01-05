package home

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

const (
	StatsAnsiReset    = "\u001b[0m"
	StatsAnsiPink     = "\u001b[35m"
	StatsAnsiPinkBold = "\u001b[35;1m"
	StatsCacheTTL     = 5000 * time.Millisecond
)

var (
	statsStartTime = time.Now().UTC()

	// Cache
	statsCacheMu sync.RWMutex
	statsCache   StatsCache
)

type StatsCache struct {
	System  StatsCachedData
	Metrics StatsCachedMetrics
}

type StatsCachedData struct {
	Data      string
	Timestamp time.Time
}

type StatsCachedMetrics struct {
	Data          StatsHealthMetrics
	Timestamp     time.Time
	InteractionID string
}

type StatsHealthMetrics struct {
	Ping        int64
	GatewayPing int64
	DBLatency   string
}

func statsTitle(text string) string {
	return fmt.Sprintf("%s%s%s", StatsAnsiPink, text, StatsAnsiReset)
}

func statsKey(text string) string {
	return fmt.Sprintf("%s> %s:%s", StatsAnsiPink, text, StatsAnsiReset)
}

func statsVal(text string) string {
	return fmt.Sprintf("%s%s%s", StatsAnsiPinkBold, text, StatsAnsiReset)
}

func init() {
	adminPerm := discord.PermissionAdministrator

	sys.RegisterCommand(discord.SlashCommandCreate{
		Name:                     "stats",
		Description:              "Display system and application statistics (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionBool{
				Name:        "ephemeral",
				Description: "Whether the message should be ephemeral (default: true)",
				Required:    false,
			},
		},
	}, handleStats)
}

func handleStats(event *events.ApplicationCommandInteractionCreate) {
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

func renderStatsContent(metrics StatsHealthMetrics) string {
	output := getSystemStats() + "\n\n" + getAppStats(metrics)
	return fmt.Sprintf("```ansi\n%s\n```", output)
}

func getSystemStats() string {
	statsCacheMu.RLock()
	if time.Since(statsCache.System.Timestamp) < StatsCacheTTL && statsCache.System.Data != "" {
		defer statsCacheMu.RUnlock()
		return statsCache.System.Data
	}
	statsCacheMu.RUnlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	usedMem := float64(m.HeapAlloc) / 1024 / 1024
	totalMem := float64(m.Sys) / 1024 / 1024

	lines := []string{
		statsTitle("System"),
		fmt.Sprintf("%s %s", statsKey("Platform"), statsVal(fmt.Sprintf("%s %s", runtime.GOOS, runtime.GOARCH))),
		fmt.Sprintf("%s %s", statsKey("Go Version"), statsVal(runtime.Version())),
		fmt.Sprintf("%s %s", statsKey("Memory"), statsVal(fmt.Sprintf("%.2f MB / %.2f MB (Sys)", usedMem, totalMem))),
		fmt.Sprintf("%s %s", statsKey("CPUs"), statsVal(fmt.Sprintf("%d", runtime.NumCPU()))),
		fmt.Sprintf("%s %s", statsKey("Goroutines"), statsVal(fmt.Sprintf("%d", runtime.NumGoroutine()))),
	}
	data := strings.Join(lines, "\n")

	statsCacheMu.Lock()
	statsCache.System = StatsCachedData{Data: data, Timestamp: time.Now().UTC()}
	statsCacheMu.Unlock()
	return data
}

func getAppStats(metrics StatsHealthMetrics) string {
	uptime := time.Since(statsStartTime)
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	uptimeStr := fmt.Sprintf("%dd %dh %dm", days, hours, minutes)

	lines := []string{
		statsTitle("App"),
		fmt.Sprintf("%s %s", statsKey("Library"), statsVal("Disgo")),
		fmt.Sprintf("%s %s", statsKey("Uptime"), statsVal(uptimeStr)),
	}

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
	_, _ = sys.GetBotConfig(sys.AppContext, "ping_test")
	metrics.DBLatency = fmt.Sprintf("%.2f", float64(time.Since(start).Microseconds())/1000.0)

	statsCacheMu.Lock()
	statsCache.Metrics = StatsCachedMetrics{
		Data:          metrics,
		Timestamp:     time.Now().UTC(),
		InteractionID: interactionID,
	}
	statsCacheMu.Unlock()

	return metrics
}
