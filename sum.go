package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"

	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/godave/golibdave"
	"github.com/disgoorg/snowflake/v2"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	kai "github.com/leeineian/kokoro/ai"
	kapp "github.com/leeineian/kokoro/app"
	kloop "github.com/leeineian/kokoro/loop"
)

func TruncateWithPreserve(text string, maxLen int, prefix, suffix string) string {
	rp, rs := []rune(prefix), []rune(suffix)
	fixedLen := len(rp) + len(rs)
	if fixedLen >= maxLen-10 {
		return TruncateCenter(prefix+text+suffix, maxLen)
	}
	return prefix + TruncateCenter(text, maxLen-fixedLen) + suffix
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Atoi(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func RandomIntRange(min, max int) int {
	if min > max {
		min, max = max, min
	}
	return rand.Intn(max-min+1) + min
}

func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func TruncateCenter(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	k := (maxLen - 3) / 2
	return string(r[:k]) + "..." + string(r[len(r)-k:])
}

func ContainsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		(len(s) > 0 && ContainsLower(s, substr)))
}

func ContainsLower(s, substr string) bool {
	s = strings.ToLower(s)
	substr = strings.ToLower(substr)
	return strings.Contains(s, substr)
}

func WrapText(text string, width int) []string {
	var lines []string
	words := strings.Fields(text)
	if len(words) == 0 {
		return lines
	}

	var sb strings.Builder
	currentLen := 0

	sb.WriteString(words[0])
	currentLen = len(words[0])

	for _, word := range words[1:] {
		wordLen := len(word)
		if currentLen+1+wordLen > width {
			lines = append(lines, sb.String())
			sb.Reset()
			sb.WriteString(word)
			currentLen = wordLen
		} else {
			sb.WriteString(" ")
			sb.WriteString(word)
			currentLen += 1 + wordLen
		}
	}
	lines = append(lines, sb.String())
	return lines
}

func ColorizeHex(colorInt int) string {
	hex := fmt.Sprintf("#%06X", colorInt)
	r := (colorInt >> 16) & 0xFF
	g := (colorInt >> 8) & 0xFF
	b := colorInt & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm⬤ %s\x1b[0m", r, g, b, hex)
}

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞"
	}
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func ParseDuration(duration string) (time.Duration, error) {
	if duration == "" || duration == "0" {
		return 0, nil
	}
	re := regexp.MustCompile(`^(\d+)(s|m|h)?$`)
	m := re.FindStringSubmatch(strings.ToLower(duration))
	if m == nil {
		return 0, fmt.Errorf("invalid format")
	}
	v, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "m":
		return time.Duration(v) * time.Minute, nil
	case "h":
		return time.Duration(v) * time.Hour, nil
	default:
		return time.Duration(v) * time.Second, nil
	}
}

func IntervalMsToDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

func run(cfg *Config, silent bool, refresh bool) error {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)

	safeGo(func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGUSR1)
		for range sigChan {
			LogInfo(MsgSignalDumpParams)
			f, err := os.Create("goroutines.txt")
			if err != nil {
				LogError(MsgSignalDumpCreateFail, err)
				continue
			}
			buf := make([]byte, 1<<20)
			length := runtime.Stack(buf, true)
			f.Write(buf[:length])
			f.Close()
			LogInfo(MsgSignalDumpSuccess)
		}
	})

	SetAppContext(ctx)

	if cfg == nil {
		var err error
		cfg, err = LoadConfig()
		if err != nil {
			return fmt.Errorf(MsgConfigFailedToLoad, err)
		}
	}

	var client bot.Client
	var err error
	for i := 1; i <= 5; i++ {
		client, err = CreateClient(ctx, cfg)
		if err == nil {
			break
		}
		if i == 5 {
			return fmt.Errorf(MsgAppClientCreateFail, i, err)
		}
		LogWarn(MsgAppClientRetry, i, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	defer client.Close(ctx)

	if err := RegisterCommands(client, cfg.GuildID, refresh); err != nil {
		LogError(MsgAppRegisterFail, err)
	}

	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf(MsgAppGatewayFail, err)
	}

	<-ctx.Done()
	if !silent {
		fmt.Println()
	}

	ShutdownDaemons(context.Background())

	LogInfo(MsgAppShutdown, GetProjectName())

	return nil
}
func CreateClient(ctx context.Context, cfg *Config) (bot.Client, error) {
	client, err := disgo.New(cfg.Token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds,
				gateway.IntentGuildMessages,
				gateway.IntentGuildMembers,
				gateway.IntentGuildPresences,
				gateway.IntentMessageContent,
				gateway.IntentGuildMessageReactions,
				gateway.IntentGuildVoiceStates,
			),
			gateway.WithPresenceOpts(
				gateway.WithPlayingActivity("Loading..."),
				gateway.WithOnlineStatus(discord.OnlineStatusOnline),
			),
		),
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagGuilds, cache.FlagMembers, cache.FlagRoles, cache.FlagChannels, cache.FlagVoiceStates),
		),
		bot.WithVoiceManagerConfigOpts(
			voice.WithDaveSessionCreateFunc(golibdave.NewSession),
		),
		bot.WithEventListenerFunc(onApplicationCommandInteraction),
		bot.WithEventListenerFunc(onAutocompleteInteraction),
		bot.WithEventListenerFunc(onComponentInteraction),
		bot.WithEventListenerFunc(onVoiceStateUpdate),
		bot.WithEventListenerFunc(onReady),
		bot.WithRestClientConfigOpts(
			rest.WithHTTPClient(&http.Client{
				Timeout: 60 * time.Second,
				Transport: &http.Transport{
					MaxIdleConns:        1000,
					MaxIdleConnsPerHost: 500,
					IdleConnTimeout:     90 * time.Second,
				},
			}),
		),
		bot.WithEventListenerFunc(kai.OnMessageCreate),
		bot.WithEventListenerFunc(kai.OnMessageReactionAdd),
	)
	if err != nil {
		return bot.Client{}, err
	}

	return *client, nil
}
func RegisterCommand(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate)) {
	commands = append(commands, cmd)
	switch c := cmd.(type) {
	case discord.SlashCommandCreate:
		commandHandlers[c.CommandName()] = handler
	case discord.UserCommandCreate:
		commandHandlers[c.CommandName()] = handler
	case discord.MessageCommandCreate:
		commandHandlers[c.CommandName()] = handler
	}
}
func RegisterAutocompleteHandler(cmdName string, handler func(event *events.AutocompleteInteractionCreate)) {
	autocompleteHandlers[cmdName] = handler
}
func RegisterComponentHandler(customID string, handler func(event *events.ComponentInteractionCreate)) {
	componentHandlers[customID] = handler
}
func RegisterVoiceStateUpdateHandler(handler func(event *events.GuildVoiceStateUpdate)) {
	voiceStateUpdateHandlers = append(voiceStateUpdateHandlers, handler)
}
func OnClientReady(cb func(ctx context.Context, client bot.Client)) {
	onClientReadyCallbacks = append(onClientReadyCallbacks, cb)
}
func calculateCommandHash(cmds []discord.ApplicationCommandCreate) string {
	data, err := json.Marshal(cmds)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func RegisterCommands(client bot.Client, guildIDStr string, forceScan bool) error {
	ctx := context.Background()

	_, _ = GetAppConfig(ctx, "last_reg_mode")
	lastGuildID, _ := GetAppConfig(ctx, "last_guild_id")

	isProduction := guildIDStr == ""
	currentMode := "guild"
	if isProduction {
		currentMode = "global"
	}

	LogLoader(MsgLoaderSyncCommands, strings.ToUpper(currentMode))

	currentHash := calculateCommandHash(commands)
	lastHash, _ := GetAppConfig(ctx, "last_cmd_hash")
	lastMode, _ := GetAppConfig(ctx, "last_reg_mode")

	shouldRegister := true
	if currentHash != "" && currentHash == lastHash && currentMode == lastMode && !forceScan {
		shouldRegister = false
		LogLoader(MsgLoaderUpToDate, currentHash[:8])
	}

	if isProduction {
		if shouldRegister {
			LogLoader(MsgLoaderProdStarting)
			createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
			if err != nil {
				return fmt.Errorf(MsgLoaderProdFail, err)
			}
			for _, cmd := range createdCommands {
				LogLoader(MsgLoaderProdRegistered, cmd.Name())
			}
		}

		shouldScan := forceScan || (lastMode != currentMode)
		if shouldScan {
			LogLoader(MsgLoaderScanStarting)
			if guilds, err := client.Rest.GetCurrentUserGuilds("", 0, 0, 100, false); err == nil {
				var wg sync.WaitGroup
				sem := make(chan struct{}, 5)

				for _, g := range guilds {
					wg.Add(1)
					safeGo(func() {
						func(guild discord.OAuth2Guild) {
							defer wg.Done()
							sem <- struct{}{}
							defer func() { <-sem }()

							if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, guild.ID, false); err == nil && len(cmds) > 0 {
								LogLoader(MsgLoaderScanCleared, guild.Name, guild.ID.String())
								_, _ = client.Rest.SetGuildCommands(client.ApplicationID, guild.ID, []discord.ApplicationCommandCreate{})
							}
						}(g)
					})
				}
				wg.Wait()
			}
		}

		if lastGuildID != "" {
			if id, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, id, false); err == nil && len(cmds) > 0 {
					LogLoader(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, id, []discord.ApplicationCommandCreate{})
				}
			}
		}
	} else {
		guildID, err := snowflake.Parse(guildIDStr)
		if err != nil {
			return fmt.Errorf(MsgLoaderInvalidGuildID, err)
		}

		if shouldRegister {
			LogLoader(MsgLoaderDevStarting, guildIDStr)
			createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
			if err != nil {
				LogWarn(MsgLoaderDevFail, err)
			} else {
				for _, cmd := range createdCommands {
					LogLoader(MsgLoaderDevRegistered, cmd.Name())
				}
			}
		}

		if lastMode != currentMode || forceScan {
			if cmds, err := client.Rest.GetGlobalCommands(client.ApplicationID, false); err == nil && len(cmds) > 0 {
				LogLoader(MsgLoaderDevGlobalClear)
				_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
				if err != nil {
					LogWarn(MsgLoaderDevGlobalClearFail, err)
				}
			}
		}

		if lastGuildID != "" && lastGuildID != guildIDStr {
			if oldID, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, oldID, false); err == nil && len(cmds) > 0 {
					LogLoader(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, oldID, []discord.ApplicationCommandCreate{})
				}
			}
		}

		if forceScan {
			LogLoader(MsgLoaderScanStarting)
			if guilds, err := client.Rest.GetCurrentUserGuilds("", 0, 0, 100, false); err == nil {
				var wg sync.WaitGroup
				sem := make(chan struct{}, 5)

				for _, g := range guilds {
					if g.ID == guildID {
						continue
					}
					wg.Add(1)
					safeGo(func() {
						func(guild discord.OAuth2Guild) {
							defer wg.Done()
							sem <- struct{}{}
							defer func() { <-sem }()

							if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, guild.ID, false); err == nil && len(cmds) > 0 {
								LogLoader(MsgLoaderScanCleared, guild.Name, guild.ID.String())
								_, _ = client.Rest.SetGuildCommands(client.ApplicationID, guild.ID, []discord.ApplicationCommandCreate{})
							}
						}(g)
					})
				}
				wg.Wait()
			}
		}
	}

	if currentHash != "" {
		_ = SetAppConfig(ctx, "last_cmd_hash", currentHash)
	}

	return nil
}
func onReady(event *events.Ready) {
	client := *event.Client()
	TriggerClientReady(AppContext, client)

	duration := time.Since(StartupTime)
	LogInfo(MsgAppReady, GetProjectName(), event.User.ID.String(), os.Getpid(), duration.Milliseconds())

	Log("Engine ready")

	_, runners := StartDaemons(AppContext)

	for _, run := range runners {
		if run != nil {
			safeGo(run)
		}
	}
}
func TriggerClientReady(ctx context.Context, client bot.Client) {
	for _, cb := range onClientReadyCallbacks {
		cb(ctx, client)
	}
}
func onApplicationCommandInteraction(event *events.ApplicationCommandInteractionCreate) {
	data := event.Data
	if h, ok := commandHandlers[data.CommandName()]; ok {
		safeGo(func() { h(event) })
	}
}
func onAutocompleteInteraction(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	if h, ok := autocompleteHandlers[data.CommandName]; ok {
		safeGo(func() { h(event) })
	}
}
func onComponentInteraction(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	if h, ok := componentHandlers[customID]; ok {
		safeGo(func() { h(event) })
		return
	}
	for prefix, h := range componentHandlers {
		if strings.HasSuffix(prefix, ":") && strings.HasPrefix(customID, prefix) {
			safeGo(func() { h(event) })
			return
		}
	}
}
func onVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	for _, h := range voiceStateUpdateHandlers {
		safeGo(func() { h(event) })
	}
}
func RegisterDaemon(name string, logger func(format string, v ...any), starter func(ctx context.Context) (bool, func(), func())) {
	registeredDaemons = append(registeredDaemons, daemonEntry{name: name, logger: logger, starter: starter})
}
func StartDaemons(ctx context.Context) (string, []func()) {
	var summary []string
	var runners []func()
	daemonsOnce.Do(func() {
		for _, entry := range registeredDaemons {
			start := time.Now()
			ok, run, shutdown := entry.starter(ctx)
			if ok {
				summarySub := fmt.Sprintf("%s (%dms)", entry.name, time.Since(start).Milliseconds())
				summary = append(summary, summarySub)
				if run != nil {
					runners = append(runners, run)
				}
				if shutdown != nil {
					activeShutdownMu.Lock()
					activeShutdownHooks = append(activeShutdownHooks, shutdown)
					activeShutdownMu.Unlock()
				}
			}
		}
	})
	return strings.Join(summary, " | "), runners
}
func ShutdownDaemons(ctx context.Context) {
	activeShutdownMu.Lock()
	defer activeShutdownMu.Unlock()

	var wg sync.WaitGroup
	for _, shutdown := range activeShutdownHooks {
		if shutdown != nil {
			wg.Add(1)
			safeGo(func() {
				func(s func()) {
					defer wg.Done()
					s()
				}(shutdown)
			})
		}
	}
	wg.Wait()
}
func init() {
	InitLogger(false, false)
}
func InitLogger(silent bool, saveToFile bool) string {
	logMu.Lock()
	defer logMu.Unlock()

	IsSilent = silent
	LogToFile = saveToFile
	level := slog.LevelInfo
	if strings.ToLower(os.Getenv("DEBUG")) == "true" {
		level = slog.LevelDebug
	}

	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}

	var writer io.Writer = os.Stdout
	var err error
	var logName string

	if LogToFile {
		exePath, exeErr := os.Executable()
		logName = GetProjectName() + ".log"
		if exeErr == nil {
			logName = filepath.Base(exePath) + ".log"
		}

		logFile, err = os.OpenFile(logName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open %s: %v\n", logName, err)
		} else {
			writer = io.MultiWriter(os.Stdout, NewStripANSIWriter(logFile))
		}
	}

	color.NoColor = false

	handler := NewAppLogHandler(writer, &AppLogHandlerOptions{
		Silent: IsSilent,
		Level:  level,
	})
	Logger = slog.New(handler)
	slog.SetDefault(Logger)

	return logName
}
func SetSilentMode(silent bool) {
	InitLogger(silent, LogToFile)
}
func LogInfo(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...))
}
func LogWarn(format string, v ...any) {
	slog.Warn(fmt.Sprintf(format, v...))
}
func LogError(format string, v ...any) {
	slog.Error(fmt.Sprintf(format, v...))
}
func LogFatal(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	slog.Log(context.Background(), slog.LevelError+4, msg)
	panic(msg)
}
func LogDebug(format string, v ...any) {
	slog.Debug(fmt.Sprintf(format, v...))
}
func LogDatabase(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "database"))
}
func LogReminder(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "reminder"))
}
func LogApp(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "bot"))
}
func LogRoleColorRotator(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "role"))
}
func LogLoopManager(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "loop"))
}
func LogCat(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "cat"))
}
func LogUndertext(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "undertext"))
}
func LogVoice(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "voice"))
}
func Log(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "ai"))
}
func LogLoader(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "loader"))
}
func LogCustom(tag string, tagColor *color.Color, format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", tag))
}
func NewAppLogHandler(w io.Writer, opts *AppLogHandlerOptions) *AppLogHandler {
	if opts == nil {
		opts = &AppLogHandlerOptions{Level: slog.LevelInfo}
	}
	return &AppLogHandler{
		w:    w,
		opts: opts,
		mu:   &sync.Mutex{},
	}
}
func (h *AppLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.opts.Silent {
		return false
	}
	return level >= h.opts.Level.Level()
}
func (h *AppLogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.opts.Silent {
		return nil
	}

	timeStr := time.Now().Format(DefaultTimeFormat)
	var levelStr string
	var levelColor *color.Color

	switch {
	case r.Level >= slog.LevelError+4:
		levelStr = "FATAL"
		levelColor = fatalColor
	case r.Level >= slog.LevelError:
		levelStr = "ERROR"
		levelColor = errorColor
	case r.Level >= slog.LevelWarn:
		levelStr = "WARN"
		levelColor = warnColor
	case r.Level >= slog.LevelInfo:
		levelStr = "INFO"
		levelColor = infoColor
	}

	if r.Level >= slog.LevelWarn && strings.Contains(strings.ToLower(r.Message), "rate limit exceeded") {
		if onRateLimitExceeded != nil {
			safeGo(onRateLimitExceeded)
		}

		if kloop.IsCleaningThreads() {
			return nil
		}
	}

	component := ""
	var extras string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" {
			component = strings.ToUpper(a.Value.String())
		} else {
			extras += "\n" + a.Key + ": " + a.Value.String()
		}
		return true
	})

	fmt.Fprintf(h.w, "%s", timeStr)

	if component != "" {
		if levelStr != "INFO" {
			fmt.Fprintf(h.w, " %s", levelColor.Sprintf("[%s]", levelStr))
		}
		compColor := getComponentColor(component)
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(compColor, fmt.Sprintf("[%s] %s%s", component, r.Message, extras)))
	} else {
		displayMsg := fmt.Sprintf("[%s] %s%s", levelStr, r.Message, extras)
		if levelStr == "INFO" && strings.HasPrefix(r.Message, "[") {
			if idx := strings.Index(r.Message, "]"); idx > 0 && idx < 20 {
				displayMsg = r.Message + extras
			}
		}
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(levelColor, displayMsg))
	}

	return nil
}
func (h *AppLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *AppLogHandler) WithGroup(name string) slog.Handler       { return h }
func LoadConfig() (*Config, error) {
	_ = godotenv.Load()

	token := os.Getenv(EnvDiscordToken)
	dbPath := filepath.Join(".", GetProjectName()+".db")

	silent, _ := strconv.ParseBool(os.Getenv(EnvSilent))
	streamingURL := os.Getenv(EnvStreamingURL)

	ownerIDsStr := os.Getenv(EnvOwnerIDs)
	var ownerIDs []string
	if ownerIDsStr != "" {
		ownerIDs = strings.Split(ownerIDsStr, ",")
		for i := range ownerIDs {
			ownerIDs[i] = strings.TrimSpace(ownerIDs[i])
		}
	}

	cfg := &Config{
		Token:        token,
		GuildID:      os.Getenv(EnvGuildID),
		DatabasePath: dbPath,
		OwnerIDs:     ownerIDs,
		StreamingURL: streamingURL,
		Silent:       silent,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if cfg.Silent {
		SetSilentMode(true)
	}

	GlobalConfig = cfg
	return cfg, nil
}
func (c *Config) Validate() error {
	if c.Token == "" {
		return fmt.Errorf(MsgConfigMissingToken)
	}
	if c.GuildID != "" && (len(c.GuildID) < 17 || len(c.GuildID) > 20) {
		return fmt.Errorf(MsgConfigInvalidGuildID)
	}
	return nil
}
func GetProjectName() string {
	exePath, err := os.Executable()
	projectName := "bot"
	if err == nil {
		projectName = filepath.Base(exePath)
		projectName = strings.TrimSuffix(projectName, ".exe")

		if projectName == "main" || strings.HasPrefix(projectName, "go_build_") {
			if modData, err := os.ReadFile("go.mod"); err == nil {
				lines := strings.Split(string(modData), "\n")
				if len(lines) > 0 && strings.HasPrefix(lines[0], "module ") {
					parts := strings.Split(lines[0], "/")
					projectName = strings.TrimSpace(parts[len(parts)-1])
				}
			}
		}
	}
	return projectName
}
func InitDatabase(ctx context.Context, dataSourceName string) error {
	var err error
	DB, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return err
	}

	DB.SetMaxOpenConns(5)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA cache_size=-2000;",
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for _, p := range pragmas {
		if _, err := DB.ExecContext(initCtx, p); err != nil {
			return fmt.Errorf(MsgDatabasePragmaError, p, err)
		}
	}

	tx, err := DB.BeginTx(initCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tableQueries := []string{
		`CREATE TABLE IF NOT EXISTS reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			channel_id TEXT NOT NULL,
			guild_id TEXT,
			message TEXT NOT NULL,
			remind_at DATETIME NOT NULL,
			send_to TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS guild_configs (
			guild_id TEXT PRIMARY KEY,
			random_color_role_id TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS bot_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS loop_channels (
			channel_id TEXT PRIMARY KEY,
			channel_name TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			rounds INTEGER DEFAULT 0,
			interval INTEGER DEFAULT 0,
			message TEXT DEFAULT '@everyone',
			webhook_author TEXT,
			webhook_avatar TEXT,
			use_thread INTEGER DEFAULT 0,
			thread_message TEXT,
			thread_count INTEGER DEFAULT 0,
			threads TEXT,
			is_running INTEGER DEFAULT 0,
			is_serial INTEGER DEFAULT 0,
			vote_panel TEXT,
			vote_role TEXT,
			vote_reaction TEXT,
			vote_message TEXT,
			vote_threshold INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS ai_messages (
			message_id TEXT PRIMARY KEY,
			guild_id TEXT,
			channel_id TEXT NOT NULL,
			content_hash TEXT,
			author_id TEXT,
			sticker_hash TEXT,
			reaction_hash TEXT,
			attachment_hash TEXT,
			created_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE TABLE IF NOT EXISTS ai_vocab (
			hash TEXT PRIMARY KEY,
			content TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS ai_tokens (
			id INTEGER PRIMARY KEY,
			token TEXT UNIQUE
		)`,
		`CREATE TABLE IF NOT EXISTS ai_transitions (
			channel_id TEXT NOT NULL,
			key_text TEXT NOT NULL,
			next_id INTEGER NOT NULL,
			weight INTEGER DEFAULT 1,
			PRIMARY KEY (channel_id, key_text, next_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_messages_channel_id ON ai_messages(channel_id)`,
	}

	for _, q := range tableQueries {
		if _, err := tx.ExecContext(initCtx, q); err != nil {
			return fmt.Errorf(MsgDatabaseTableError, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	migrations := []string{
		"ALTER TABLE loop_channels ADD COLUMN thread_count INTEGER DEFAULT 0",
		"ALTER TABLE loop_channels ADD COLUMN vote_panel TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_role TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_reaction TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_message TEXT",
		"ALTER TABLE loop_channels ADD COLUMN vote_threshold INTEGER DEFAULT 0",
		"ALTER TABLE loop_channels ADD COLUMN is_serial INTEGER DEFAULT 0",
		"ALTER TABLE ai_messages ADD COLUMN content_hash TEXT",
		"ALTER TABLE ai_messages ADD COLUMN sticker_hash TEXT",
		"ALTER TABLE ai_messages ADD COLUMN reaction_hash TEXT",
		"ALTER TABLE ai_messages ADD COLUMN attachment_hash TEXT",
		"CREATE INDEX IF NOT EXISTS idx_ai_messages_content_hash ON ai_messages(content_hash)",
		"CREATE INDEX IF NOT EXISTS idx_ai_messages_sticker_hash ON ai_messages(sticker_hash)",
		"CREATE INDEX IF NOT EXISTS idx_ai_messages_attachment_hash ON ai_messages(attachment_hash)",
		"CREATE INDEX IF NOT EXISTS idx_ai_messages_reaction_hash ON ai_messages(reaction_hash)",
	}

	legacyColumns := []string{"content", "sticker_id", "attachment_id", "attachment_url", "reactions"}
	for _, col := range legacyColumns {
		_, _ = tx.ExecContext(initCtx, "ALTER TABLE ai_messages DROP COLUMN "+col)
	}

	for _, m := range migrations {
		if _, err := DB.ExecContext(initCtx, m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf(MsgDBMigrationFail, err)
			}
		}
	}

	if rows, err := DB.QueryContext(initCtx, "SELECT message_id, content FROM ai_messages WHERE content IS NOT NULL AND content != '' AND (content_hash IS NULL OR content_hash = '')"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, content string
			if err := rows.Scan(&id, &content); err == nil {
				hash := sha256.Sum256([]byte(content))
				hashStr := hex.EncodeToString(hash[:])
				_ = DB.QueryRowContext(initCtx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", hashStr, content)
				_, _ = DB.ExecContext(initCtx, "UPDATE ai_messages SET content_hash = ? WHERE message_id = ?", hashStr, id)
			}
		}
	}

	if rows, err := DB.QueryContext(initCtx, "SELECT message_id, sticker_id FROM ai_messages WHERE sticker_id IS NOT NULL AND sticker_id != '' AND (sticker_hash IS NULL OR sticker_hash = '')"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, sID string
			if err := rows.Scan(&id, &sID); err == nil {
				content := "STICKER:" + sID
				hash := sha256.Sum256([]byte(content))
				hashStr := hex.EncodeToString(hash[:])
				_ = DB.QueryRowContext(initCtx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", hashStr, content)
				_, _ = DB.ExecContext(initCtx, "UPDATE ai_messages SET sticker_hash = ? WHERE message_id = ?", hashStr, id)
			}
		}
	}

	if rows, err := DB.QueryContext(initCtx, "SELECT message_id, attachment_url FROM ai_messages WHERE attachment_url IS NOT NULL AND attachment_url != '' AND (attachment_hash IS NULL OR attachment_hash = '')"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, url string
			if err := rows.Scan(&id, &url); err == nil {
				content := "ATTACHMENT:" + url
				hash := sha256.Sum256([]byte(content))
				hashStr := hex.EncodeToString(hash[:])
				_ = DB.QueryRowContext(initCtx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", hashStr, content)
				_, _ = DB.ExecContext(initCtx, "UPDATE ai_messages SET attachment_hash = ? WHERE message_id = ?", hashStr, id)
			}
		}
	}

	if rows, err := DB.QueryContext(initCtx, "SELECT message_id, reactions FROM ai_messages WHERE reactions IS NOT NULL AND reactions != '' AND (reaction_hash IS NULL OR reaction_hash = '')"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, reactions string
			if err := rows.Scan(&id, &reactions); err == nil {
				content := "REACTION:" + reactions
				hash := sha256.Sum256([]byte(content))
				hashStr := hex.EncodeToString(hash[:])
				_ = DB.QueryRowContext(initCtx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", hashStr, content)
				_, _ = DB.ExecContext(initCtx, "UPDATE ai_messages SET reaction_hash = ? WHERE message_id = ?", hashStr, id)
			}
		}
	}

	for _, m := range migrations {
		if _, err := DB.ExecContext(initCtx, m); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf(MsgDBMigrationFail, err)
			}
		}
	}

	return nil
}
func CloseDatabase() {
	if DB != nil {
		DB.Close()
	}
}
func GetAppConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := DB.QueryRowContext(ctx, "SELECT value FROM bot_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}
func SetAppConfig(ctx context.Context, key, value string) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO bot_config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}
func AddReminder(ctx context.Context, r *kapp.Reminder) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO reminders (user_id, channel_id, guild_id, message, remind_at, send_to)
		VALUES (?, ?, ?, ?, ?, ?)
	`, r.UserID.String(), r.ChannelID.String(), r.GuildID.String(), r.Message, r.RemindAt, r.SendTo)
	return err
}
func GetRemindersForUser(ctx context.Context, userID snowflake.ID) ([]*kapp.Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE user_id = ? ORDER BY remind_at ASC
	`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*kapp.Reminder
	for rows.Next() {
		r := &kapp.Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseUserIDFail, uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseChannelIDFail, cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			if gid != "" {
				return nil, fmt.Errorf(MsgDBParseGuildIDFail, gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}
func ClaimDueReminders(ctx context.Context) ([]*kapp.Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		DELETE FROM reminders 
		WHERE remind_at <= ? 
		RETURNING id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*kapp.Reminder
	for rows.Next() {
		r := &kapp.Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseClaimUserFail, uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseClaimChanFail, cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			if gid != "" {
				return nil, fmt.Errorf(MsgDBParseClaimGuildFail, gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}
func GetDueReminders(ctx context.Context) ([]*kapp.Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE remind_at <= ? ORDER BY remind_at ASC
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*kapp.Reminder
	for rows.Next() {
		r := &kapp.Reminder{}
		var uid, cid, gid string
		err := rows.Scan(&r.ID, &uid, &cid, &gid, &r.Message, &r.RemindAt, &r.SendTo, &r.CreatedAt)
		if err != nil {
			return nil, err
		}
		r.UserID, err = snowflake.Parse(uid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseDueUserFail, uid, r.ID, err)
		}
		r.ChannelID, err = snowflake.Parse(cid)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseDueChanFail, cid, r.ID, err)
		}
		r.GuildID, err = snowflake.Parse(gid)
		if err != nil {
			if gid != "" {
				return nil, fmt.Errorf(MsgDBParseDueGuildFail, gid, r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}
func DeleteReminder(ctx context.Context, id int64, userID snowflake.ID) (bool, error) {
	result, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE id = ? AND user_id = ?", id, userID.String())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}
func DeleteAllRemindersForUser(ctx context.Context, userID snowflake.ID) (int64, error) {
	result, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE user_id = ?", userID.String())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func DeleteReminderByID(ctx context.Context, id int64) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM reminders WHERE id = ?", id)
	return err
}
func GetRemindersCountForUser(ctx context.Context, userID snowflake.ID) (int, error) {
	var count int
	err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reminders WHERE user_id = ?", userID.String()).Scan(&count)
	return count, err
}
func GetRemindersCount(ctx context.Context) (int, error) {
	var count int
	err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM reminders").Scan(&count)
	return count, err
}
func AddLoopConfig(ctx context.Context, channelID snowflake.ID, config *kloop.LoopConfig) error {
	useThread := 0
	if config.UseThread {
		useThread = 1
	}

	_, err := DB.ExecContext(ctx, `
		INSERT INTO loop_channels (
			channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads,
			vote_panel, vote_role, vote_message, vote_threshold,
			is_running, is_serial
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT is_running FROM loop_channels WHERE channel_id = ?), 0), ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			channel_name = excluded.channel_name,
			channel_type = excluded.channel_type,
			rounds = excluded.rounds,
			interval = excluded.interval,
			message = excluded.message,
			webhook_author = excluded.webhook_author,
			webhook_avatar = excluded.webhook_avatar,
			use_thread = excluded.use_thread,
			thread_message = excluded.thread_message,
			thread_count = excluded.thread_count,
			threads = excluded.threads,
			vote_panel = excluded.vote_panel,
			vote_role = excluded.vote_role,
			vote_message = excluded.vote_message,
			vote_threshold = excluded.vote_threshold,
			is_serial = excluded.is_serial
	`, channelID.String(), config.ChannelName, config.ChannelType, config.Rounds, config.Interval,
		config.Message, config.WebhookAuthor, config.WebhookAvatar,
		useThread, config.ThreadMessage, config.ThreadCount, config.Threads,
		config.VoteChannelID, config.VoteRole, config.VoteMessage, config.VoteThreshold,
		channelID.String(), boolToInt(config.IsSerial))
	return err
}
func GetLoopConfig(ctx context.Context, channelID snowflake.ID) (*kloop.LoopConfig, error) {
	row := DB.QueryRowContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads, is_running,
			vote_panel, vote_role, vote_message, vote_threshold, is_serial
		FROM loop_channels WHERE channel_id = ?
	`, channelID.String())

	config := &kloop.LoopConfig{}
	var idStr string
	var message, author, avatar, threadMsg, threads sql.NullString
	var votePanel, voteRole, voteMessage sql.NullString
	var useThread, isRunning, isSerial int

	err := row.Scan(
		&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
		&message, &author, &avatar,
		&useThread, &threadMsg, &config.ThreadCount, &threads, &isRunning,
		&votePanel, &voteRole, &voteMessage, &config.VoteThreshold, &isSerial,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	config.ChannelID, err = snowflake.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf(MsgDBParseLoopChanIDFail, err)
	}
	config.Message = message.String
	if config.Message == "" {
		config.Message = "@everyone"
	}
	config.WebhookAuthor = author.String
	config.WebhookAvatar = avatar.String
	config.UseThread = useThread == 1
	config.ThreadMessage = threadMsg.String
	config.Threads = threads.String
	config.IsRunning = isRunning == 1
	config.VoteChannelID = votePanel.String
	config.VoteRole = voteRole.String
	config.VoteMessage = voteMessage.String
	config.IsSerial = isSerial == 1

	return config, nil
}
func SaveAIMessage(ctx context.Context, msgID snowflake.ID, guildID snowflake.ID, channelID snowflake.ID, content string, authorID snowflake.ID, stickerID string, reactions string, attachmentID string, attachmentURL string) error {
	hashStr := ""
	if content != "" {
		hash := sha256.Sum256([]byte(content))
		hashStr = hex.EncodeToString(hash[:])
		if _, err := DB.ExecContext(ctx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", hashStr, content); err != nil {
			return err
		}
	}

	stickerHashStr := ""
	if stickerID != "" {
		stickerContent := "STICKER:" + stickerID
		hash := sha256.Sum256([]byte(stickerContent))
		stickerHashStr = hex.EncodeToString(hash[:])
		if _, err := DB.ExecContext(ctx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", stickerHashStr, stickerContent); err != nil {
			return err
		}
	}

	attachmentHashStr := ""
	if attachmentURL != "" {
		attachmentContent := "ATTACHMENT:" + attachmentURL
		hash := sha256.Sum256([]byte(attachmentContent))
		attachmentHashStr = hex.EncodeToString(hash[:])
		if _, err := DB.ExecContext(ctx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", attachmentHashStr, attachmentContent); err != nil {
			return err
		}
	}

	reactionHashStr := ""
	if reactions != "" {
		reactionContent := "REACTION:" + reactions
		hash := sha256.Sum256([]byte(reactionContent))
		reactionHashStr = hex.EncodeToString(hash[:])
		if _, err := DB.ExecContext(ctx, "INSERT OR IGNORE INTO ai_vocab (hash, content) VALUES (?, ?)", reactionHashStr, reactionContent); err != nil {
			return err
		}
	}

	_, err := DB.ExecContext(ctx, `
		INSERT INTO ai_messages (message_id, guild_id, channel_id, content_hash, author_id, sticker_hash, reaction_hash, attachment_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			content_hash = excluded.content_hash,
			sticker_hash = excluded.sticker_hash,
			reaction_hash = CASE WHEN excluded.reaction_hash != '' THEN excluded.reaction_hash ELSE reaction_hash END,
			attachment_hash = excluded.attachment_hash
	`, msgID.String(), guildID.String(), channelID.String(), hashStr, authorID.String(), stickerHashStr, reactionHashStr, attachmentHashStr)
	return err
}
func GetRecentAIMessages(ctx context.Context, channelID snowflake.ID, limit int) ([]*kai.AIMessageData, error) {
	query := `
		SELECT 
			m.message_id, 
			COALESCE(v.content, ''), 
			COALESCE(vs.content, ''), 
			COALESCE(vr.content, ''), 
			COALESCE(va.content, ''), 
			m.author_id, 
			m.created_at 
		FROM ai_messages m
		LEFT JOIN ai_vocab v ON m.content_hash = v.hash
		LEFT JOIN ai_vocab vs ON m.sticker_hash = vs.hash
		LEFT JOIN ai_vocab vr ON m.reaction_hash = vr.hash
		LEFT JOIN ai_vocab va ON m.attachment_hash = va.hash
		WHERE m.channel_id = ? 
		ORDER BY m.created_at DESC`
	var args []any
	args = append(args, channelID.String())

	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []*kai.AIMessageData
	for rows.Next() {
		var messageIDStr, content, stickerContent, reactions, attachmentContent, authorIDStr string
		var createdAt time.Time
		if err := rows.Scan(&messageIDStr, &content, &stickerContent, &reactions, &attachmentContent, &authorIDStr, &createdAt); err != nil {
			return messages, err
		}

		msg := content
		if stickerContent != "" && stickerContent != "STICKER:" {
			if msg != "" {
				msg += " "
			}
			msg += stickerContent
		}
		if attachmentContent != "" && attachmentContent != "ATTACHMENT:" {
			if msg != "" {
				msg += " "
			}
			msg += attachmentContent
		}
		if reactions != "" {
			reacts := strings.SplitSeq(reactions, ",")
			for r := range reacts {
				if r != "" {
					msg += " REACTION:" + r
				}
			}
		}

		mID, _ := snowflake.Parse(messageIDStr)
		aID, _ := snowflake.Parse(authorIDStr)
		if msg != "" {
			messages = append(messages, &kai.AIMessageData{
				MessageID: mID,
				Content:   msg,
				AuthorID:  aID,
				CreatedAt: createdAt,
			})
		}
	}
	return messages, nil
}
func GetAIMemoryDump(ctx context.Context) (*kai.AIMemoryDump, error) {
	query := `
		SELECT 
			COALESCE(v.content, ''), 
			COALESCE(vs.content, ''), 
			COALESCE(vr.content, ''), 
			COALESCE(va.content, '')
		FROM ai_messages m
		LEFT JOIN ai_vocab v ON m.content_hash = v.hash
		LEFT JOIN ai_vocab vs ON m.sticker_hash = vs.hash
		LEFT JOIN ai_vocab vr ON m.reaction_hash = vr.hash
		LEFT JOIN ai_vocab va ON m.attachment_hash = va.hash
		ORDER BY m.created_at ASC`

	rows, err := DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dump := &kai.AIMemoryDump{}

	for rows.Next() {
		var content, stickerContent, reactions, attachmentContent sql.NullString
		if err := rows.Scan(&content, &stickerContent, &reactions, &attachmentContent); err != nil {
			return nil, err
		}

		if content.Valid && content.String != "" {
			dump.TextMessages = append(dump.TextMessages, content.String)
		}
		if stickerContent.Valid && stickerContent.String != "" && stickerContent.String != "STICKER:" {
			id := strings.TrimPrefix(stickerContent.String, "STICKER:")
			if id != "" {
				dump.StickerIDs = append(dump.StickerIDs, id)
			}
		}
		if reactions.Valid && reactions.String != "" && reactions.String != "REACTION:" {
			reacts := strings.Split(strings.TrimPrefix(reactions.String, "REACTION:"), ",")
			for _, r := range reacts {
				if r != "" {
					dump.ReactionEmojis = append(dump.ReactionEmojis, r)
				}
			}
		}
		if attachmentContent.Valid && attachmentContent.String != "" && attachmentContent.String != "ATTACHMENT:" {
			url := strings.TrimPrefix(attachmentContent.String, "ATTACHMENT:")
			if url != "" {
				dump.AttachmentURLs = append(dump.AttachmentURLs, url)
			}
		}
	}
	return dump, nil
}
func ClearAIMessages(ctx context.Context, channelID snowflake.ID) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM ai_messages WHERE channel_id = ?", channelID.String())
	return err
}
func ClearAllAIMessages(ctx context.Context) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM ai_messages")
	if err != nil {
		return err
	}
	_, err = DB.ExecContext(ctx, "DELETE FROM ai_vocab")
	return err
}
func GetAllAIVocab(ctx context.Context) (map[string]string, error) {
	rows, err := DB.QueryContext(ctx, "SELECT hash, content FROM ai_vocab")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vocab := make(map[string]string)
	for rows.Next() {
		var hash, content string
		if err := rows.Scan(&hash, &content); err != nil {
			return nil, err
		}
		vocab[hash] = content
	}
	return vocab, nil
}
func ClearAIMessagesByHashes(ctx context.Context, hashes []string) error {
	if len(hashes) == 0 {
		return nil
	}

	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, h := range hashes {
		_, err = tx.ExecContext(ctx, "DELETE FROM ai_messages WHERE content_hash = ? OR sticker_hash = ? OR reaction_hash = ? OR attachment_hash = ?", h, h, h, h)
		if err != nil {
			return err
		}
	}

	for _, h := range hashes {
		_, err = tx.ExecContext(ctx, "DELETE FROM ai_vocab WHERE hash = ?", h)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}
func GetChannelsWithAIMemory(ctx context.Context) ([]string, error) {
	rows, err := DB.QueryContext(ctx, "SELECT DISTINCT channel_id FROM ai_messages")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []string
	for rows.Next() {
		var chID string
		if err := rows.Scan(&chID); err != nil {
			return nil, err
		}
		channels = append(channels, chID)
	}
	return channels, nil
}
func GetAllLoopConfigs(ctx context.Context) (map[snowflake.ID]*kloop.LoopConfig, error) {
	rows, err := DB.QueryContext(ctx, "SELECT channel_id, channel_name, channel_type, rounds, interval, message, webhook_author, webhook_avatar, use_thread, thread_message, thread_count, threads, is_running, vote_panel, vote_role, vote_message, vote_threshold, is_serial FROM loop_channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make(map[snowflake.ID]*kloop.LoopConfig)
	for rows.Next() {
		var config kloop.LoopConfig
		var idStr, channelType, message, author, avatar, threadMsg, threads, votePanel, voteRole, voteMessage sql.NullString
		var useThread, isRunning, isSerial int
		err := rows.Scan(
			&idStr, &config.ChannelName, &channelType, &config.Rounds, &config.Interval,
			&message, &author, &avatar, &useThread, &threadMsg, &config.ThreadCount, &threads,
			&isRunning, &votePanel, &voteRole, &voteMessage, &config.VoteThreshold, &isSerial,
		)
		if err != nil {
			LogInfo("Failed to fetch configs: %v", err)
			continue
		}
		config.ChannelID, _ = snowflake.Parse(idStr.String)
		config.ChannelType = channelType.String
		config.Message = message.String
		if config.Message == "" {
			config.Message = "@everyone"
		}
		config.WebhookAuthor = author.String
		config.WebhookAvatar = avatar.String
		config.UseThread = useThread == 1
		config.ThreadMessage = threadMsg.String
		config.Threads = threads.String
		config.IsRunning = isRunning == 1
		config.VoteChannelID = votePanel.String
		config.VoteRole = voteRole.String
		config.VoteMessage = voteMessage.String
		config.IsSerial = isSerial == 1

		configs[config.ChannelID] = &config
	}
	return configs, nil
}
func DeleteLoopConfigDB(ctx context.Context, channelID snowflake.ID) error {
	_, err := DB.ExecContext(ctx, "DELETE FROM loop_channels WHERE channel_id = ?", channelID.String())
	return err
}
func SetLoopState(ctx context.Context, channelID snowflake.ID, running bool) error {
	val := 0
	if running {
		val = 1
	}
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET is_running = ? WHERE channel_id = ?", val, channelID.String())
	return err
}
func ResetAllLoopStates(ctx context.Context) error {
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET is_running = 0")
	return err
}
func UpdateLoopChannelName(ctx context.Context, channelID snowflake.ID, name string) error {
	_, err := DB.ExecContext(ctx, "UPDATE loop_channels SET channel_name = ? WHERE channel_id = ?", name, channelID.String())
	return err
}
func SetGuildRandomColorRole(ctx context.Context, guildID, roleID snowflake.ID) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO guild_configs (guild_id, random_color_role_id) VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET random_color_role_id = excluded.random_color_role_id, updated_at = CURRENT_TIMESTAMP
	`, guildID.String(), roleID.String())
	return err
}
func GetGuildRandomColorRole(ctx context.Context, guildID snowflake.ID) (snowflake.ID, error) {
	var roleIDStr sql.NullString
	err := DB.QueryRowContext(ctx, "SELECT random_color_role_id FROM guild_configs WHERE guild_id = ?", guildID.String()).Scan(&roleIDStr)
	if err == sql.ErrNoRows || !roleIDStr.Valid || roleIDStr.String == "" {
		return 0, nil
	}
	roleID, err := snowflake.Parse(roleIDStr.String)
	if err != nil {
		return 0, fmt.Errorf(MsgDBParseRoleIDFail, err)
	}
	return roleID, err
}
func GetAllGuildRandomColorConfigs(ctx context.Context) (map[snowflake.ID]snowflake.ID, error) {
	rows, err := DB.QueryContext(ctx, "SELECT guild_id, random_color_role_id FROM guild_configs WHERE random_color_role_id IS NOT NULL AND random_color_role_id != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make(map[snowflake.ID]snowflake.ID)
	for rows.Next() {
		var gStr, rStr string
		if err := rows.Scan(&gStr, &rStr); err != nil {
			return nil, fmt.Errorf(MsgDBScanGuildConfigFail, err)
		}
		gID, err := snowflake.Parse(gStr)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseGuildIDColorFail, gStr, err)
		}
		rID, err := snowflake.Parse(rStr)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseRoleIDColorFail, rStr, err)
		}
		configs[gID] = rID
	}
	return configs, nil
}
func (m MediaGallery) GetID() int {
	return 0
}
func (m MediaGallery) Type() discord.ComponentType {
	return ComponentTypeMediaGallery
}
func (t Thumbnail) GetID() int {
	return 0
}
func (t Thumbnail) Type() discord.ComponentType {
	return ComponentTypeThumbnail
}
func (f File) GetID() int {
	return 0
}
func (f File) Type() discord.ComponentType {
	return ComponentTypeFile
}
func (s Separator) GetID() int {
	return 0
}
func (s Separator) Type() discord.ComponentType {
	return ComponentTypeSeparator
}
func (t TextDisplay) GetID() int {
	return 0
}
func (t TextDisplay) Type() discord.ComponentType {
	return ComponentTypeTextDisplay
}
func (s Section) GetID() int {
	return 0
}
func (s Section) Type() discord.ComponentType {
	return ComponentTypeSection
}
func (c Container) GetID() int {
	return 0
}
func (c Container) Type() discord.ComponentType {
	return ComponentTypeContainer
}
func (c Container) ContainerComponent() {}
func NewV2Container(components ...interface{}) Container {
	return Container{
		CType:      ComponentTypeContainer,
		Components: components,
	}
}
func NewTextDisplay(content string) TextDisplay {
	return TextDisplay{
		CType:   ComponentTypeTextDisplay,
		Content: content,
	}
}
func NewMediaGallery(urls ...string) MediaGallery {
	items := make([]MediaGalleryItem, len(urls))
	for i, url := range urls {
		items[i] = MediaGalleryItem{
			Media: UnfurledMediaItem{
				URL: url,
			},
		}
	}
	return MediaGallery{
		CType: ComponentTypeMediaGallery,
		Items: items,
	}
}
func NewThumbnail(url string) Thumbnail {
	return Thumbnail{
		CType: ComponentTypeThumbnail,
		Media: UnfurledMediaItem{
			URL: url,
		},
	}
}
func NewFile(url string, filename string) File {
	return File{
		CType: ComponentTypeFile,
		File: UnfurledMediaItem{
			URL: url,
		},
		Filename: filename,
	}
}
func NewSeparator(divider bool) Separator {
	return Separator{
		CType:   ComponentTypeSeparator,
		Divider: divider,
	}
}
func NewSeparatorWithSpacing(divider bool, spacing SeparatorSpacing) Separator {
	return Separator{
		CType:   ComponentTypeSeparator,
		Divider: divider,
		Spacing: spacing,
	}
}
func NewSection(content string, accessory any) Section {
	s := Section{
		CType:      ComponentTypeSection,
		Components: []any{NewTextDisplay(content)},
	}
	if accessory != nil {
		s.Accessory = accessory
	}
	return s
}
func EditInteractionContainerV2(client bot.Client, interaction discord.Interaction, container Container) error {
	route := rest.NewEndpoint(http.MethodPatch, "/webhooks/{application.id}/{interaction.token}/messages/@original")

	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []any{container},
		Flags:      MessageFlagsIsComponentsV2,
	}

	compiledRoute := route.Compile(nil, client.ApplicationID.String(), interaction.Token())

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func EditInteractionV2(client bot.Client, interaction discord.Interaction, content string) error {
	route := rest.NewEndpoint(http.MethodPatch, "/webhooks/{application.id}/{interaction.token}/messages/@original")
	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []any{NewTextDisplay(content)},
		Flags:      MessageFlagsIsComponentsV2,
	}

	compiledRoute := route.Compile(nil, client.ApplicationID.String(), interaction.Token())

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func RespondInteractionContainerV2(client bot.Client, interaction discord.Interaction, container Container, ephemeral bool) error {
	route := rest.NewEndpoint(http.MethodPost, "/interactions/{interaction.id}/{interaction.token}/callback")

	var flags discord.MessageFlags
	if ephemeral {
		flags = discord.MessageFlagEphemeral | MessageFlagsIsComponentsV2
	} else {
		flags = MessageFlagsIsComponentsV2
	}

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		}{
			Components: []any{container},
			Flags:      flags,
		},
	}

	compiledRoute := route.Compile(nil, interaction.ID().String(), interaction.Token())

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func RespondInteractionFiles(client bot.Client, interaction discord.Interaction, content string, files []*discord.File, ephemeral bool) error {
	var flags discord.MessageFlags
	if ephemeral {
		flags = discord.MessageFlagEphemeral
	}

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Content     string               `json:"content,omitempty"`
			Flags       discord.MessageFlags `json:"flags"`
			Attachments []any                `json:"attachments,omitempty"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: struct {
			Content     string               `json:"content,omitempty"`
			Flags       discord.MessageFlags `json:"flags"`
			Attachments []any                `json:"attachments,omitempty"`
		}{
			Content: content,
			Flags:   flags,
		},
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormField("payload_json")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(part)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return err
	}

	for i, file := range files {
		part, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", i), file.Name)
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, file.Reader); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}

	url := "https://discord.com/api/v10/interactions/" + interaction.ID().String() + "/" + interaction.Token() + "/callback"
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Rest.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord api error: %s", string(b))
	}

	return nil
}
func RespondInteractionContainerV2Files(client bot.Client, interaction discord.Interaction, files []*discord.File, ephemeral bool, containers ...Container) error {
	var flags discord.MessageFlags
	if ephemeral {
		flags = discord.MessageFlagEphemeral | MessageFlagsIsComponentsV2
	} else {
		flags = MessageFlagsIsComponentsV2
	}

	anyContainers := make([]any, len(containers))
	for i, c := range containers {
		anyContainers[i] = c
	}

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components  []any                `json:"components"`
			Flags       discord.MessageFlags `json:"flags"`
			Attachments []any                `json:"attachments,omitempty"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: struct {
			Components  []any                `json:"components"`
			Flags       discord.MessageFlags `json:"flags"`
			Attachments []any                `json:"attachments,omitempty"`
		}{
			Components: anyContainers,
			Flags:      flags,
		},
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormField("payload_json")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(part)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return err
	}

	for i, file := range files {
		part, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", i), file.Name)
		if err != nil {
			return err
		}
		if _, err := io.Copy(part, file.Reader); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}

	url := "https://discord.com/api/v10/interactions/" + interaction.ID().String() + "/" + interaction.Token() + "/callback"
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Rest.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("interaction callback failed with status: %d", resp.StatusCode)
	}

	return nil
}
func RespondInteractionV2(client bot.Client, interaction discord.Interaction, content string, ephemeral bool) error {
	route := rest.NewEndpoint(http.MethodPost, "/interactions/{interaction.id}/{interaction.token}/callback")

	var flags discord.MessageFlags
	if ephemeral {
		flags = discord.MessageFlagEphemeral | MessageFlagsIsComponentsV2
	} else {
		flags = MessageFlagsIsComponentsV2
	}

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeCreateMessage,
		Data: struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		}{
			Components: []any{NewTextDisplay(content)},
			Flags:      flags,
		},
	}

	compiledRoute := route.Compile(nil, interaction.ID().String(), interaction.Token())

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func UpdateInteractionContainerV2(client bot.Client, interaction discord.Interaction, container Container) error {
	route := rest.NewEndpoint(http.MethodPost, "/interactions/{interaction.id}/{interaction.token}/callback")

	data := struct {
		Type discord.InteractionResponseType `json:"type"`
		Data struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		} `json:"data"`
	}{
		Type: discord.InteractionResponseTypeUpdateMessage,
		Data: struct {
			Components []any                `json:"components"`
			Flags      discord.MessageFlags `json:"flags"`
		}{
			Components: []any{container},
			Flags:      MessageFlagsIsComponentsV2,
		},
	}

	compiledRoute := route.Compile(nil, interaction.ID().String(), interaction.Token())

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func SendContainerV2(client bot.Client, channelID snowflake.ID, container Container, ref *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPost, "/channels/{channel.id}/messages")

	data := struct {
		Components       []any                     `json:"components"`
		Flags            discord.MessageFlags      `json:"flags"`
		MessageReference *discord.MessageReference `json:"message_reference,omitempty"`
		StickerIDs       []snowflake.ID            `json:"sticker_ids,omitempty"`
		Embeds           []discord.Embed           `json:"embeds,omitempty"`
	}{
		Components:       []any{container},
		Flags:            MessageFlagsIsComponentsV2,
		MessageReference: ref,
		StickerIDs:       stickers,
		Embeds:           embeds,
	}

	compiledRoute := route.Compile(nil, channelID.String())

	var msg discord.Message
	err := doRequestNoEscape(client, compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
func SendMessageV2(client bot.Client, channelID snowflake.ID, content string, ref *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPost, "/channels/{channel.id}/messages")

	var components []any
	if content != "" {
		components = append(components, NewTextDisplay(content))
	}

	data := struct {
		Components       []any                     `json:"components"`
		Flags            discord.MessageFlags      `json:"flags"`
		MessageReference *discord.MessageReference `json:"message_reference,omitempty"`
		StickerIDs       []snowflake.ID            `json:"sticker_ids,omitempty"`
		Embeds           []discord.Embed           `json:"embeds,omitempty"`
	}{
		Components:       components,
		Flags:            MessageFlagsIsComponentsV2,
		MessageReference: ref,
		StickerIDs:       stickers,
		Embeds:           embeds,
	}

	compiledRoute := route.Compile(nil, channelID.String())

	var msg discord.Message
	err := doRequestNoEscape(client, compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
func EditContainerV2(client bot.Client, channelID, messageID snowflake.ID, container Container, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPatch, "/channels/{channel.id}/messages/{message.id}")

	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
		StickerIDs []snowflake.ID       `json:"sticker_ids,omitempty"`
		Embeds     []discord.Embed      `json:"embeds,omitempty"`
	}{
		Components: []any{container},
		Flags:      MessageFlagsIsComponentsV2,
		StickerIDs: stickers,
		Embeds:     embeds,
	}

	compiledRoute := route.Compile(nil, channelID.String(), messageID.String())

	var msg discord.Message
	err := doRequestNoEscape(client, compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
func EditMessageV2(client bot.Client, channelID, messageID snowflake.ID, content string, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPatch, "/channels/{channel.id}/messages/{message.id}")

	var components []any
	if content != "" {
		components = append(components, NewTextDisplay(content))
	}

	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
		StickerIDs []snowflake.ID       `json:"sticker_ids,omitempty"`
		Embeds     []discord.Embed      `json:"embeds,omitempty"`
	}{
		Components: components,
		Flags:      MessageFlagsIsComponentsV2,
		StickerIDs: stickers,
		Embeds:     embeds,
	}

	compiledRoute := route.Compile(nil, channelID.String(), messageID.String())

	var msg discord.Message
	err := doRequestNoEscape(client, compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
func EditInteractionContainerV2ByToken(client bot.Client, appID snowflake.ID, token string, container Container) error {
	route := rest.NewEndpoint(http.MethodPatch, "/webhooks/{application.id}/{interaction.token}/messages/@original")
	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []any{container},
		Flags:      MessageFlagsIsComponentsV2,
	}
	compiledRoute := route.Compile(nil, appID.String(), token)

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func EditInteractionV2ByToken(client bot.Client, appID snowflake.ID, token string, content string) error {
	route := rest.NewEndpoint(http.MethodPatch, "/webhooks/{application.id}/{interaction.token}/messages/@original")
	data := struct {
		Components []any                `json:"components"`
		Flags      discord.MessageFlags `json:"flags"`
	}{
		Components: []any{NewTextDisplay(content)},
		Flags:      MessageFlagsIsComponentsV2,
	}

	compiledRoute := route.Compile(nil, appID.String(), token)

	return doRequestNoEscape(client, compiledRoute, data, nil)
}
func SendComponentsV2(client bot.Client, channelID snowflake.ID, components []any, ref *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
	route := rest.NewEndpoint(http.MethodPost, "/channels/{channel.id}/messages")

	data := struct {
		Components       []any                     `json:"components"`
		Flags            discord.MessageFlags      `json:"flags"`
		MessageReference *discord.MessageReference `json:"message_reference,omitempty"`
		StickerIDs       []snowflake.ID            `json:"sticker_ids,omitempty"`
		Embeds           []discord.Embed           `json:"embeds,omitempty"`
	}{
		Components:       components,
		Flags:            MessageFlagsIsComponentsV2,
		MessageReference: ref,
		StickerIDs:       stickers,
		Embeds:           embeds,
	}

	compiledRoute := route.Compile(nil, channelID.String())

	var msg discord.Message
	err := doRequestNoEscape(client, compiledRoute, data, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}
func doRequestNoEscape(client bot.Client, route *rest.CompiledEndpoint, body any, dst any) error {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return err
	}
	return client.Rest.Do(route, json.RawMessage(buf.Bytes()), dst)
}
func safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				LogError(MsgLoaderPanicRecovered, r)
				fmt.Printf("%s\n", debug.Stack())
			}
		}()
		f()
	}()
}
func NewStripANSIWriter(w io.Writer) *StripANSIWriter {
	return &StripANSIWriter{
		w:  w,
		re: regexp.MustCompile(`\x1b\[[0-9;]*m`),
	}
}
func (s *StripANSIWriter) Write(p []byte) (n int, err error) {
	clean := s.re.ReplaceAll(p, []byte(""))
	_, err = s.w.Write(clean)
	return len(p), err
}
func GetLogPath() string {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return ""
	}
	return logFile.Name()
}
func OnRateLimitExceeded(fn func()) {
	logMu.Lock()
	defer logMu.Unlock()
	onRateLimitExceeded = fn
}
func GetUserErrors() map[string]string {
	errorMapOnce.Do(func() {
		errorMapCache = make(map[string]string)

		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			return
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			return
		}

		ast.Inspect(node, func(n ast.Node) bool {
			genDecl, isGenDecl := n.(*ast.GenDecl)
			if isGenDecl && genDecl.Tok == token.CONST {
				for _, spec := range genDecl.Specs {
					valueSpec, isValueSpec := spec.(*ast.ValueSpec)
					if isValueSpec {
						for i, name := range valueSpec.Names {
							constName := name.Name
							if strings.HasPrefix(constName, "Err") || strings.HasPrefix(constName, "Msg") {
								if len(valueSpec.Values) > i {
									if basicLit, isBasicLit := valueSpec.Values[i].(*ast.BasicLit); isBasicLit && basicLit.Kind == token.STRING {
										constValue := strings.Trim(basicLit.Value, `"`)
										if !strings.Contains(constValue, "%") {
											errorMapCache[constName] = constValue
										}
									}
								}
							}
						}
					}
				}
			}
			return true
		})
	})

	return errorMapCache
}
func getComponentColor(name string) *color.Color {
	if name == "DATABASE" || name == "LOADER" {
		return color.New()
	}
	return color.New(color.FgMagenta)
}
func colorizeWithResets(c *color.Color, text string) string {
	if !strings.Contains(text, "\x1b[0m") {
		return c.Sprint(text)
	}

	marker := "@@@MSG@@@"
	wrapped := c.Sprint(marker)
	idx := strings.Index(wrapped, marker)
	if idx <= 0 {
		return text
	}
	startSeq := wrapped[:idx]

	modifiedText := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+startSeq)
	return c.Sprint(modifiedText)
}
func SetAppContext(ctx context.Context) {
	AppContext = ctx
}

type StripANSIWriter struct {
	w  io.Writer
	re *regexp.Regexp
}

var HttpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func FetchAIHistory(ctx context.Context, client bot.Client, channelID snowflake.ID) ([]string, error) {
	var (
		allMessages    []*kai.AIMessageData
		seenIDs        = make(map[snowflake.ID]bool)
		humanCount     = 0
		scannedCount   = 0
		beforeID       snowflake.ID
		guildID        snowflake.ID
		groupedSamples []string
		reacts         []string
		content        string
		stickerID      string
		msgStr         string
		attachmentID   string
		attachmentURL  string
		reactions      string
		reactionList   []string
		msgGuildID     snowflake.ID
		currentGroup   string
		lastAuthor     snowflake.ID
		lastTime       time.Time
		msg            discord.Message
		msgData        *kai.AIMessageData
		messages       []discord.Message
		err            error
		r              discord.MessageReaction
		s              string
		dbMessages     []*kai.AIMessageData
		i              int
		ch             any
		ok             bool
		gID            interface{ GuildID() snowflake.ID }
		rStr           string
	)

	if ch, ok = client.Caches.Channel(channelID); ok {
		if gID, ok = ch.(interface{ GuildID() snowflake.ID }); ok {
			guildID = gID.GuildID()
		}
	}

	for humanCount < kai.TargetHumanMessages && scannedCount < kai.MaxScanDepth {
		messages, err = client.Rest.GetMessages(channelID, 0, beforeID, 0, kai.ChunkSize)
		if err != nil {
			LogApp(kai.LogAIHistoryFetchFail, scannedCount, err)
			break
		}

		if len(messages) == 0 {
			break
		}

		for _, msg = range messages {
			scannedCount++
			if !msg.Author.Bot && len(msg.Content) > 0 {
				content = msg.Content
				stickerID = ""
				if len(msg.StickerItems) > 0 {
					stickerID = msg.StickerItems[0].ID.String()
				}

				if strings.HasPrefix(content, "/") || strings.HasPrefix(content, "!") {
					continue
				}

				msgStr = content
				if stickerID != "" {
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "STICKER:" + stickerID
				}

				attachmentID = ""
				attachmentURL = ""
				if len(msg.Attachments) > 0 {
					attachmentID = msg.Attachments[0].ID.String()
					attachmentURL = msg.Attachments[0].URL
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "ATTACHMENT:" + attachmentURL
				}

				if msgStr != "" {
					reactions = ""
					if len(msg.Reactions) > 0 {
						reacts = nil
						for _, r = range msg.Reactions {
							s = r.Emoji.Name
							if r.Emoji.ID != 0 {
								s = fmt.Sprintf("%s:%s", r.Emoji.Name, r.Emoji.ID.String())
							}
							reacts = append(reacts, s)
						}
						reactions = strings.Join(reacts, ",")
					}
					if reactions != "" {
						reactionList = strings.Split(reactions, ",")
						for _, rStr = range reactionList {
							if rStr != "" {
								msgStr += " REACTION:" + rStr
							}
						}
					}

					if !seenIDs[msg.ID] {
						allMessages = append(allMessages, &kai.AIMessageData{
							MessageID: msg.ID,
							Content:   msgStr,
							AuthorID:  msg.Author.ID,
							CreatedAt: msg.CreatedAt,
						})
						seenIDs[msg.ID] = true
						humanCount++
					}

					msgGuildID = guildID
					if msg.GuildID != nil {
						msgGuildID = *msg.GuildID
					}

					if msg.ReferencedMessage != nil {
						msgStr = "REPLY: " + msgStr
					}

					if strings.Contains(msg.Content, fmt.Sprintf("<@%s>", client.ID())) || strings.Contains(msg.Content, fmt.Sprintf("<@!%s>", client.ID())) {
						if !strings.HasPrefix(msgStr, "REPLY:") {
							msgStr = "MENTION: " + msgStr
						}
					}

					err = SaveAIMessage(ctx, msg.ID, msgGuildID, msg.ChannelID, content, msg.Author.ID, stickerID, reactions, attachmentID, attachmentURL)
					if err != nil {
						LogError(kai.LogAISaveHistoryFail, err)
					}
				}
			}
		}

		beforeID = messages[len(messages)-1].ID

		if len(messages) < kai.ChunkSize {
			break
		}
	}

	dbMessages, err = GetRecentAIMessages(ctx, channelID, kai.HistoryDBLimit)
	if err == nil {
		for _, msgData = range dbMessages {
			if !seenIDs[msgData.MessageID] {
				allMessages = append(allMessages, msgData)
				seenIDs[msgData.MessageID] = true
			}
		}
	}

	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].CreatedAt.Before(allMessages[j].CreatedAt)
	})

	if len(allMessages) > 0 {
		currentGroup = allMessages[0].Content
		lastAuthor = allMessages[0].AuthorID
		lastTime = allMessages[0].CreatedAt
		for i = 1; i < len(allMessages); i++ {
			msgData = allMessages[i]
			if msgData.AuthorID == lastAuthor && msgData.CreatedAt.Sub(lastTime) < kai.GroupingWindow {
				currentGroup += " " + msgData.Content
			} else {
				groupedSamples = append(groupedSamples, currentGroup)
				currentGroup = msgData.Content
			}
			lastAuthor = msgData.AuthorID
			lastTime = msgData.CreatedAt
		}
		groupedSamples = append(groupedSamples, currentGroup)
	}

	return groupedSamples, nil
}

func SplitAIMessage(content string, limit int) []string {
	if len(content) <= limit {
		return []string{content}
	}
	var chunks []string
	for len(content) > limit {
		splitAt := -1
		if val := strings.LastIndex(content[:limit], "\n"); val != -1 {
			splitAt = val
		} else if val := strings.LastIndex(content[:limit], " "); val != -1 {
			splitAt = val
		} else {
			splitAt = limit
		}

		chunk := content[:splitAt]
		chunks = append(chunks, chunk)
		content = content[splitAt:]
		content = strings.TrimPrefix(content, "\n")
		content = strings.TrimPrefix(content, " ")
	}
	if len(content) > 0 {
		chunks = append(chunks, content)
	}
	return chunks
}
