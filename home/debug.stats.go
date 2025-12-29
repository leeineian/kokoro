package home

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

func handleDebugStats(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	ephemeral := true
	if len(options) > 0 {
		for _, opt := range options {
			if opt.Name == "ephemeral" {
				ephemeral = opt.BoolValue()
			}
		}
	}

	// Defer reply
	if err := sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("⏳ Loading stats...")), ephemeral); err != nil {
		sys.LogDebug("Failed to defer stats: %v", err)
		return
	}

	go func() {
		// Calculate Metrics (Ping) - Round Trip
		interTime, _ := discordgo.SnowflakeTimestamp(i.Interaction.ID)
		var roundTrip int64

		msg, err := s.InteractionResponse(i.Interaction)
		if err == nil && msg != nil {
			msgTime, _ := discordgo.SnowflakeTimestamp(msg.ID)
			roundTrip = msgTime.Sub(interTime).Milliseconds()
		} else {
			roundTrip = time.Since(interTime).Milliseconds()
		}

		metrics := getDebugMetrics(i.Interaction.ID, s, i.Member.User.ID, true)
		metrics.Ping = roundTrip

		debugCacheMu.Lock()
		debugStatsCache.Metrics.Data = metrics
		debugCacheMu.Unlock()

		container := renderDebugStats(s, metrics)
		if err := sys.EditInteractionV2(s, i.Interaction, container); err != nil {
			sys.LogDebug("Failed to edit stats: %v", err)
			return
		}

		if ephemeral {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			timeout := time.After(5 * time.Minute)

			for {
				select {
				case <-ticker.C:
					liveMetrics := getDebugMetrics(i.Interaction.ID, s, i.Member.User.ID, true)
					liveMetrics.Ping = roundTrip

					newContainer := renderDebugStats(s, liveMetrics)
					if err := sys.EditInteractionV2(s, i.Interaction, newContainer); err != nil {
						return
					}
				case <-timeout:
					return
				}
			}
		}
	}()
}

func handleDebugPing(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	ephemeral := true
	if len(options) > 0 {
		for _, opt := range options {
			if opt.Name == "ephemeral" {
				ephemeral = opt.BoolValue()
			}
		}
	}

	if err := sys.RespondInteractionV2(s, i.Interaction, sys.NewV2Container(sys.NewTextDisplay("🏓 Pinging...")), ephemeral); err != nil {
		return
	}

	go func() {
		var latency int64
		interTime, _ := discordgo.SnowflakeTimestamp(i.Interaction.ID)

		msg, err := s.InteractionResponse(i.Interaction)
		if err == nil && msg != nil {
			msgTime, _ := discordgo.SnowflakeTimestamp(msg.ID)
			latency = msgTime.Sub(interTime).Milliseconds()
		} else {
			latency = time.Since(interTime).Milliseconds()
		}

		container := buildDebugPingContainer(latency, "# Pong!")
		if err := sys.EditInteractionV2(s, i.Interaction, container); err != nil {
			sys.LogDebug("Failed to edit ping: %v", err)
		}
	}()
}

func handleDebugPingRefresh(s *discordgo.Session, i *discordgo.InteractionCreate) {
	interTime, _ := discordgo.SnowflakeTimestamp(i.Interaction.ID)
	latency := time.Since(interTime).Milliseconds()

	container := buildDebugPingContainer(latency, "# 🔁 Pong!")

	if err := sys.UpdateInteractionV2(s, i.Interaction, container); err != nil {
		sys.LogDebug("Failed to update ping refresh: %v", err)
	}
}

func renderDebugStats(s *discordgo.Session, metrics DebugHealthMetrics) sys.Container {
	output := getDebugSystemStats() + "\n\n" + getDebugAppStats(s, metrics)

	return sys.NewV2Container(
		sys.NewTextDisplay(fmt.Sprintf("```ansi\n%s\n```", output)),
	)
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

func getDebugAppStats(s *discordgo.Session, metrics DebugHealthMetrics) string {
	uptime := time.Since(debugStartTime)
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	uptimeStr := fmt.Sprintf("%dd %dh %dm", days, hours, minutes)

	guildCount := len(s.State.Guilds)
	userCount := 0
	for _, g := range s.State.Guilds {
		userCount += g.MemberCount
	}

	lines := []string{
		debugTitle("App"),
		fmt.Sprintf("%s %s", debugKey("Library"), debugVal("DiscordGo")),
		fmt.Sprintf("%s %s", debugKey("Uptime"), debugVal(uptimeStr)),
		fmt.Sprintf("%s %s", debugKey("Servers"), debugVal(fmt.Sprintf("%d", guildCount))),
		fmt.Sprintf("%s %s", debugKey("Users"), debugVal(fmt.Sprintf("%d", userCount))),
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

func getDebugMetrics(interactionID string, s *discordgo.Session, userID string, includePing bool) DebugHealthMetrics {
	debugCacheMu.RLock()
	if debugStatsCache.Metrics.InteractionID == interactionID && time.Since(debugStatsCache.Metrics.Timestamp) < DebugCacheTTL {
		defer debugCacheMu.RUnlock()
		return debugStatsCache.Metrics.Data
	}
	debugCacheMu.RUnlock()

	metrics := DebugHealthMetrics{}

	if includePing {
		metrics.GatewayPing = s.HeartbeatLatency().Milliseconds()
	}

	start := time.Now()
	var count int
	if sys.DB != nil {
		_ = sys.DB.QueryRow("SELECT COUNT(*) FROM reminders WHERE user_id = ?", userID).Scan(&count)
	}
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

func buildDebugPingContainer(latency int64, titleStr string) sys.Container {
	latencyStyle := discordgo.SuccessButton
	if latency >= 100 {
		latencyStyle = discordgo.DangerButton
	}

	btnLatency := discordgo.Button{
		Label:    fmt.Sprintf("%dms", latency),
		CustomID: "ping_refresh",
		Style:    latencyStyle,
	}

	return sys.NewV2Container(
		sys.NewSection(titleStr, btnLatency),
	)
}
