package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

// ===========================
// Command Registration
// ===========================

func init() {
	adminPerm := discord.PermissionAdministrator

	OnClientReady(func(ctx context.Context, client *bot.Client) {
		RegisterDaemon(LogStatusRotator, func(ctx context.Context) (bool, func(), func()) { return StartStatusRotator(ctx, client) })
	})

	RegisterCommand(discord.SlashCommandCreate{
		Name:                     "session",
		Description:              "Session management utilities (Admin Only)",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Contexts: []discord.InteractionContextType{
			discord.InteractionContextTypeGuild,
		},
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "reboot",
				Description: "Restart the bot process",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "build",
						Description: "Whether to rebuild the binary before restarting (default: false)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "shutdown",
				Description: "Shut down the bot process",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: "Display system and application statistics",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "status",
				Description: "Configure bot status visibility",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "visible",
						Description: "Enable or disable status rotation",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "console",
				Description: "View recent bot logs",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionBool{
						Name:        "ephemeral",
						Description: "Whether the message should be ephemeral (default: true)",
						Required:    false,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "truncate",
						Description: "Whether to clear the log file before viewing (default: false)",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "cleanup",
				Description: "Clear all guild commands from the current server",
			},
		},
	}, handleSession)

	RegisterComponentHandler("console:", handleConsolePagination)
}

// ===========================
// Types
// ===========================

// StatsHealthMetrics contains health check metrics for the bot
type StatsHealthMetrics struct {
	Ping        int64
	GatewayPing int64
	DBLatency   string
}

// StatsCachedData stores cached data with a timestamp
type StatsCachedData struct {
	Data      string
	Timestamp time.Time
}

// StatsCachedMetrics stores cached metrics with interaction tracking
type StatsCachedMetrics struct {
	Data          StatsHealthMetrics
	Timestamp     time.Time
	InteractionID string
}

// StatsCache stores all cached stats data
type StatsCache struct {
	System  StatsCachedData
	Metrics StatsCachedMetrics
}

// ===========================
// Globals & Constants
// ===========================

const (
	StatsAnsiReset    = "\u001b[0m"
	StatsAnsiPink     = "\u001b[35m"
	StatsAnsiPinkBold = "\u001b[35;1m"
	StatsCacheTTL     = 5 * time.Second
)

var (
	// Status Rotator State
	StartTime       = time.Now().UTC()
	statusList      []func(context.Context, *bot.Client) string
	lastStatusText  string
	statusMu        sync.RWMutex
	configKeyStatus = "status_visible"

	// Stats State
	statsStartTime = time.Now().UTC()
	statsCacheMu   sync.RWMutex
	statsCache     StatsCache
)

// ===========================
// Status Rotator Logic
// ===========================

// GetRotationInterval returns the status rotation interval
func GetRotationInterval() time.Duration {
	return time.Duration(15+rand.Intn(46)) * time.Second
}

// StartStatusRotator starts the status rotation daemon
func StartStatusRotator(ctx context.Context, client *bot.Client) (bool, func(), func()) {
	// Always start the daemon even if currently disabled, so it can be re-enabled at runtime
	statusList = []func(context.Context, *bot.Client) string{
		GetRemindersStatus,
		GetColorStatus,
		GetUptimeStatus,
		GetLatencyStatus,
		GetTimeStatus,
	}

	return true, func() {
			next := GetRotationInterval()
			updateStatus(ctx, client, next)
			for {
				select {
				case <-time.After(next):
					next = GetRotationInterval()
					updateStatus(ctx, client, next)
				case <-ctx.Done():
					return
				}
			}
		}, func() { // Shutdown hook
			LogStatusRotator("Shutting down Status Rotator...")
		}
}

// updateStatus updates the bot's status with the next status in rotation
func updateStatus(ctx context.Context, client *bot.Client, nextInterval time.Duration) {
	if client == nil {
		return
	}

	visibleStr, err := GetBotConfig(ctx, configKeyStatus)
	if err != nil || visibleStr == "false" {
		client.SetPresence(ctx, gateway.WithOnlineStatus(discord.OnlineStatusOnline))
		return
	}

	var availableStatuses []string
	for _, gen := range statusList {
		if text := gen(ctx, client); text != "" {
			availableStatuses = append(availableStatuses, text)
		}
	}

	if len(availableStatuses) == 0 {
		availableStatuses = append(availableStatuses, GetUptimeStatus(ctx, client))
	}

	statusMu.RLock()
	last := lastStatusText
	statusMu.RUnlock()

	var finalChoices []string
	for _, s := range availableStatuses {
		if s != last {
			finalChoices = append(finalChoices, s)
		}
	}

	var selectedStatus string
	if len(finalChoices) > 0 {
		selectedStatus = finalChoices[rand.Intn(len(finalChoices))]
	} else {
		selectedStatus = availableStatuses[0]
	}

	statusMu.Lock()
	lastStatusText = selectedStatus
	statusMu.Unlock()

	err = client.SetPresence(ctx,
		gateway.WithOnlineStatus(discord.OnlineStatusOnline),
		gateway.WithStreamingActivity(selectedStatus, GlobalConfig.StreamingURL),
	)

	if err != nil {
		LogStatusRotator(MsgStatusUpdateFail, err)
	} else {
		logStatus := selectedStatus
		re := regexp.MustCompile(`#([A-Fa-f0-9]{6})`)
		logStatus = re.ReplaceAllStringFunc(selectedStatus, func(match string) string {
			colorInt, _ := strconv.ParseUint(match[1:], 16, 64)
			return ColorizeHex(int(colorInt))
		})

		if nextInterval > 0 {
			LogStatusRotator(MsgStatusRotated, logStatus, nextInterval)
		} else {
			LogStatusRotator(MsgStatusRotatedNoInterval, logStatus)
		}
	}
}

// GetRemindersStatus returns a status string showing next reminder time
func GetRemindersStatus(ctx context.Context, client *bot.Client) string {
	count, _ := GetRemindersCount(ctx)
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("Reminder: %d", count)
}

// GetColorStatus returns a status string showing next role color update
func GetColorStatus(ctx context.Context, client *bot.Client) string {
	nextUpdate, guildID, found := GetNextUpdate(ctx)
	if !found {
		return ""
	}
	currentColor := GetCurrentColor(ctx, client, guildID)
	if currentColor == "" {
		return ""
	}
	diff := time.Until(nextUpdate)
	return fmt.Sprintf("Color: %s in %dm", currentColor, int(diff.Minutes()))
}

// GetUptimeStatus returns a status string showing bot uptime
func GetUptimeStatus(ctx context.Context, client *bot.Client) string {
	uptime := time.Since(StartTime)
	return fmt.Sprintf("Uptime: %dh %dm %ds", int(uptime.Hours()), int(uptime.Minutes())%60, int(uptime.Seconds())%60)
}

// GetLatencyStatus returns a status string showing gateway latency
func GetLatencyStatus(ctx context.Context, client *bot.Client) string {
	ping := client.Gateway.Latency()
	if ping == 0 {
		return ""
	}
	return fmt.Sprintf("Ping: %dms", ping.Milliseconds())
}

// GetTimeStatus returns a status string showing current UTC time
func GetTimeStatus(ctx context.Context, client *bot.Client) string {
	return "Time: " + time.Now().Local().Format("15:04:05") + " (Local)"
}

// ===========================
// Command Handlers
// ===========================

// handleSession routes session subcommands to their respective handlers
func handleSession(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "reboot":
		handleSessionReboot(event, data)
	case "shutdown":
		handleSessionShutdown(event)
	case "stats":
		handleSessionStats(event, data)
	case "status":
		handleSessionStatus(event, data)
	case "console":
		handleSessionConsole(event, data)
	case "cleanup":
		handleSessionCleanup(event)
	default:
		log.Printf("Unknown session subcommand: %s", subCmd)
	}
}

func handleSessionReboot(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	build, _ := data.OptBool("build")
	LogWarn(MsgSessionRebootCommanded, event.User().Username, event.User().ID)

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionRebooting))).
		SetEphemeral(true).
		Build())

	if build {
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
			discord.NewMessageUpdateBuilder().AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionRebootBuilding))).Build())

		exePath, err := os.Executable()
		if err != nil {
			exePath = GetProjectName()
		}

		cmd := exec.Command("go", "build", "-o", exePath, ".")
		output, err := cmd.CombinedOutput()
		if err != nil {
			_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
				discord.NewMessageUpdateBuilder().AddComponents(discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf(MsgSessionRebootBuildFail, string(output))))).Build())
			return
		}

		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
			discord.NewMessageUpdateBuilder().AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionRebootBuildSuccess+"\n"+MsgSessionRebooting))).Build())
	}

	RestartRequested = true
	time.Sleep(1500 * time.Millisecond)
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}

func handleSessionShutdown(event *events.ApplicationCommandInteractionCreate) {
	LogWarn(MsgSessionShutdownCommanded, event.User().Username, event.User().ID)
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionShuttingDown))).
		SetEphemeral(true).
		Build())
	time.Sleep(1 * time.Second)
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}

func handleSessionStatus(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	visible := data.Bool("visible")
	if visible {
		SetBotConfig(AppContext, "status_visible", "true")
	} else {
		SetBotConfig(AppContext, "status_visible", "false")
	}

	content := MsgSessionStatusUpdated
	if visible {
		content = MsgSessionStatusEnabled
	} else {
		content = MsgSessionStatusDisabled
	}

	err := event.CreateMessage(discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		AddComponents(discord.NewContainer(discord.NewTextDisplay(content))).
		SetEphemeral(true).
		Build())
	if err != nil {
		LogDebug(MsgDebugStatusCmdFail, err)
	}
}

func handleSessionStats(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}

	builder := discord.NewMessageCreateBuilder().
		SetIsComponentsV2(true).
		SetEphemeral(ephemeral).
		AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionStatsLoading)))

	err := event.CreateMessage(builder.Build())
	if err != nil {
		return
	}

	go func() {
		interTime := snowflake.ID(event.ID()).Time()
		roundTrip := time.Since(interTime).Milliseconds()

		metrics := getStatsMetrics(event.ID().String(), event.Client().Gateway.Latency().Milliseconds(), true)
		metrics.Ping = roundTrip

		statsCacheMu.Lock()
		statsCache.Metrics.Data = metrics
		statsCacheMu.Unlock()

		content := renderStatsContent(metrics)
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
			discord.NewMessageUpdateBuilder().SetIsComponentsV2(true).AddComponents(discord.NewContainer(discord.NewTextDisplay(content))).Build())

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
					_, err := event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(),
						discord.NewMessageUpdateBuilder().SetIsComponentsV2(true).AddComponents(discord.NewContainer(discord.NewTextDisplay(content))).Build())

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
				case <-AppContext.Done():
					return
				}
			}
		}
	}()
}

func handleSessionCleanup(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("This command can only be used in a server.").SetEphemeral(true).Build())
		return
	}

	_, err := event.Client().Rest.SetGuildCommands(event.ApplicationID(), *guildID, []discord.ApplicationCommandCreate{})
	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent(fmt.Sprintf("Failed to clear commands: %v", err)).SetEphemeral(true).Build())
		return
	}

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("Successfully cleared all guild commands from this server.").SetEphemeral(true).Build())
}

func handleSessionConsole(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	ephemeral := true
	if eph, ok := data.OptBool("ephemeral"); ok {
		ephemeral = eph
	}
	if trunc, ok := data.OptBool("truncate"); ok && trunc {
		logPath := GetLogPath()
		if logPath != "" {
			_ = os.Truncate(logPath, 0)
			LogInfo("Log file truncated by user %s", event.User().Username)
		}
	}
	renderConsole(event, 20, 0, ephemeral)
}

// ===========================
// Stats Helpers
// ===========================

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
	_, _ = GetBotConfig(AppContext, "ping_test")
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

	var m runtime.MemStats
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

// ===========================
// Console Helpers
// ===========================

func handleConsolePagination(event *events.ComponentInteractionCreate) {
	data := event.Data
	var direction string
	var count, offset int
	if menu, ok := data.(discord.StringSelectMenuInteractionData); ok {
		parts := strings.Split(menu.Values[0], ":")
		direction, count, offset = parts[0], Atoi(parts[1]), Atoi(parts[2])
	}
	newOffset := offset
	switch direction {
	case "up":
		newOffset += count
	case "down":
		newOffset -= count
		if newOffset < 0 {
			newOffset = 0
		}
	case "top":
		newOffset = 1000000
	case "bottom":
		newOffset = 0
	}
	renderConsole(event, count, newOffset, true)
}

func renderConsole(event any, count, offset int, ephemeral bool) {
	path := GetLogPath()
	if path == "" {
		if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
			_ = ev.CreateMessage(discord.NewMessageCreateBuilder().SetContent(MsgSessionConsoleDisabled).SetEphemeral(ephemeral).Build())
		} else if ev, ok := event.(*events.ComponentInteractionCreate); ok {
			_ = ev.UpdateMessage(discord.NewMessageUpdateBuilder().AddComponents(discord.NewContainer(discord.NewTextDisplay(MsgSessionConsoleDisabled))).Build())
		}
		return
	}
	logs, hasMore, actual, err := readLogLines(path, count, offset)
	if err != nil {
		return
	}
	var opts []discord.StringSelectMenuOption
	if hasMore {
		opts = append(opts, discord.NewStringSelectMenuOption(MsgSessionConsoleBtnOldest, fmt.Sprintf("top:%d:%d", count, actual)).WithDescription("Jump to oldest"))
		opts = append(opts, discord.NewStringSelectMenuOption(MsgSessionConsoleBtnOlder, fmt.Sprintf("up:%d:%d", count, actual)).WithDescription("View older"))
	}
	opts = append(opts, discord.NewStringSelectMenuOption(MsgSessionConsoleBtnRefresh, fmt.Sprintf("refresh:%d:%d", count, actual)).WithDescription("Reload current"))
	if actual > 0 {
		opts = append(opts, discord.NewStringSelectMenuOption(MsgSessionConsoleBtnNewer, fmt.Sprintf("down:%d:%d", count, actual)).WithDescription("View newer"))
		opts = append(opts, discord.NewStringSelectMenuOption(MsgSessionConsoleBtnLatest, fmt.Sprintf("bottom:%d:%d", count, actual)).WithDescription("Jump to latest"))
	}
	nav := discord.NewStringSelectMenu("console:nav", "Navigate Logs...", opts...)
	container := discord.NewContainer(discord.NewTextDisplay(fmt.Sprintf("```ansi\n%s\n```", logs)), discord.NewActionRow(nav))
	if ev, ok := event.(*events.ComponentInteractionCreate); ok {
		_ = ev.UpdateMessage(discord.NewMessageUpdateBuilder().SetIsComponentsV2(true).SetComponents(container).Build())
	} else if ev, ok := event.(*events.ApplicationCommandInteractionCreate); ok {
		_ = ev.CreateMessage(discord.NewMessageCreateBuilder().SetIsComponentsV2(true).SetEphemeral(ephemeral).AddComponents(container).Build())
	}
}

func readLogLines(path string, count, offset int) (string, bool, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, 0, err
	}
	defer f.Close()
	s, _ := f.Stat()
	if s.Size() == 0 {
		return "", false, 0, nil
	}
	buf := make([]byte, 8192)
	cur := s.Size()
	var offs []int64
	offs = append(offs, s.Size())
	limit := offset + count + 1
	for cur > 0 && len(offs) <= limit {
		sz := int64(8192)
		if cur < sz {
			sz = cur
		}
		cur -= sz
		_, _ = f.ReadAt(buf[:sz], cur)
		chunk := buf[:sz]
		for {
			idx := bytes.LastIndexByte(chunk, '\n')
			if idx == -1 {
				break
			}
			pos := cur + int64(idx)
			if pos != s.Size()-1 {
				offs = append(offs, pos)
				if len(offs) > limit {
					break
				}
			}
			chunk = chunk[:idx]
		}
	}
	if cur == 0 && (len(offs) == 1 || offs[len(offs)-1] != 0) {
		offs = append(offs, 0)
	}
	found := len(offs) - 1
	actual := offset
	if actual > found-count {
		actual = found - count
	}
	if actual < 0 {
		actual = 0
	}
	e, st := offs[actual], offs[Min(actual+count, found)]
	if st > 0 {
		st++
	}
	length := e - st
	const maxR = 2 * 1024 * 1024
	if length > maxR {
		st = e - maxR
		length = maxR
	}
	if length <= 0 {
		return MsgSessionConsoleEmpty, actual+count < found, actual, nil
	}
	res := make([]byte, length)
	_, _ = f.ReadAt(res, st)
	logs := strings.TrimSpace(string(res))
	if len(logs) > 1950 {
		cut := len(logs) - 1950
		if nl := strings.IndexByte(logs[cut:], '\n'); nl != -1 {
			logs = logs[cut+nl+1:]
		} else {
			logs = logs[cut:]
		}
	}
	return logs, actual+count < found, actual, nil
}

// ===========================
// Utilities
// ===========================

// Min returns the minimum of two integers.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the maximum of two integers.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Atoi converts a string to an integer, returning 0 on error.
func Atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}
