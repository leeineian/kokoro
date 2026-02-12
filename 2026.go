package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/mattn/go-sqlite3"
)

// ============================================================================
// Core
// ============================================================================

const (
	MsgConfigFailedToLoad   = "Failed to load config: %v"
	MsgConfigMissingToken   = "DISCORD_TOKEN is not set in .env file"
	MsgDatabaseInitSuccess  = "Database initialized successfully"
	MsgDatabaseTableError   = "Failed to create table: %w"
	MsgDatabasePragmaError  = "Failed to set pragma %s: %w"
	MsgDaemonStarting       = "Starting..."
	MsgBotStarting          = "Starting %s..."
	MsgBotReady             = "%s is ready! (ID: %s) (PID: %d) (Took: %dms)"
	MsgBotShutdown          = "Shutting down %s..."
	MsgBotKillingOld        = "Killing running instance... (PID: %d)"
	MsgBotKillFail          = "Failed to kill old instance: %v"
	MsgBotOldTerminated     = "Old instance terminated."
	MsgBotPIDWriteFail      = "Failed to write PID file: %v"
	MsgBotRegisterFail      = "Command registration failed: %v"
	MsgBotAPIStatusError    = "discord API returned status %d"
	MsgGenericError         = "%v"
	MsgInitializing         = "Initializing %s..."
	MsgDatabaseInitFail     = "Failed to initialize database: %v"
	MsgPIDOpenFail          = "Failed to open PID file: %v"
	MsgPIDLockFail          = "Failed to lock PID file: %v"
	MsgBotStubbornOld       = "Old process %d is stubborn. Sending SIGKILL..."
	MsgBotKillResistant     = "Process %d still exists after SIGKILL"
	MsgBotRestarting        = "Self-restarting process..."
	MsgBotStartPathFail     = "Failed to resolve executable path: %v"
	MsgBotExecFail          = "Failed to re-execute: %v"
	MsgSignalDumpParams     = "Received SIGUSR1, dumping goroutines to goroutines.txt"
	MsgSignalDumpCreateFail = "Failed to create goroutines.txt: %v"
	MsgSignalDumpSuccess    = "Goroutines dumped"
	MsgBotClientCreateFail  = "failed to create Discord client after %d attempts: %w"
	MsgBotClientRetry       = "Failed to create Discord client (attempt %d/5): %v. Retrying in 5s..."
	MsgBotSkipReg           = "Skipping command registration as requested."
	MsgBotGatewayFail       = "failed to open gateway: %w"
	MsgDaemonShutdown       = "Shutting down all daemons..."
	MsgPanicFatal           = "\n[FATAL] %s\n"
	BotPIDFile              = ".bot.pid"
)

func main() {
	// 0. Recover from panics (LogFatal uses panic to ensure defers run)
	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok {
				fmt.Fprintf(os.Stderr, MsgPanicFatal, msg)
				os.Exit(1)
			}
			panic(r)
		}
	}()

	// 1. Load configuration early
	cfg, err := LoadConfig()
	if err != nil {
		LogError(MsgConfigFailedToLoad, err)
	}

	silent := flag.Bool("silent", false, "Disable all log output")
	skipReg := flag.Bool("skip-reg", false, "Skip command registration")
	clearAll := flag.Bool("clear-all", false, "Force clear guild commands (scan all guilds)")
	flag.Parse()

	// 2. Initialize Logger (handle flags)
	logName := InitLogger(*silent, true)

	// 3. Try to detect bot name
	botName := GetProjectName()

	// 4. Log Starting Message
	LogInfo(MsgBotStarting, botName)

	// 5. Initialize Database & Logs
	LogInfo(MsgInitializing, filepath.Base(cfg.DatabasePath))
	if logName != "" {
		LogInfo(MsgInitializing, filepath.Base(logName))
	}

	if err := InitDatabase(context.Background(), cfg.DatabasePath); err != nil {
		LogFatal(MsgDatabaseInitFail, err)
	}
	defer CloseDatabase()

	// 6. Open or create the PID file
	f, err := os.OpenFile(BotPIDFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		LogFatal(MsgPIDOpenFail, err)
	}
	defer f.Close()

	// 7. Try to acquire an exclusive lock
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if err != syscall.EWOULDBLOCK {
			LogFatal(MsgPIDLockFail, err)
		}

		var oldPid int
		_, _ = f.Seek(0, 0)
		if _, scanErr := fmt.Fscanf(f, "%d", &oldPid); scanErr != nil {
			_ = f.Close()
			<-ticker.C
			f, _ = os.OpenFile(BotPIDFile, os.O_RDWR|os.O_CREATE, 0644)
			continue
		}

		if oldPid == os.Getpid() {
			break
		}

		process, procErr := os.FindProcess(oldPid)
		if procErr != nil {
			<-ticker.C
			continue
		}

		LogInfo(MsgBotKillingOld, oldPid)
		_ = process.Signal(syscall.SIGTERM)

		terminated := false
		timeout := time.After(5 * time.Second)
	waitLoop:
		for {
			select {
			case <-ticker.C:
				if err := process.Signal(syscall.Signal(0)); err != nil {
					terminated = true
					break waitLoop
				}
			case <-timeout:
				break waitLoop
			}
		}

		if !terminated {
			LogWarn(MsgBotStubbornOld, oldPid)
			_ = process.Signal(syscall.SIGKILL)

			killTimeout := time.After(2 * time.Second)
			killTicker := time.NewTicker(50 * time.Millisecond)
			defer killTicker.Stop()

		killWait:
			for {
				select {
				case <-killTicker.C:
					if err := process.Signal(syscall.Signal(0)); err != nil {
						break killWait
					}
				case <-killTimeout:
					LogWarn(MsgBotKillResistant, oldPid)
					break killWait
				}
			}
		}

		LogInfo(MsgBotOldTerminated)
	}

	// 8. We have the lock. Write our PID.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()

	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = os.Remove(BotPIDFile)
	}()

	// 9. Run bot (blocks until shutdown signal)
	if err := run(cfg, *silent, *skipReg, *clearAll); err != nil {
		LogFatal(MsgGenericError, err)
	}

	// 10. Handle Reboot
	if RestartRequested {
		LogInfo(MsgBotRestarting)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(BotPIDFile)

		args := os.Args
		hasSkipReg := slices.Contains(args, "-skip-reg")
		if !hasSkipReg {
			args = append(args, "-skip-reg")
		}

		exePath, err := os.Executable()
		if err != nil {
			LogFatal(MsgBotStartPathFail, err)
		}

		err = syscall.Exec(exePath, args, os.Environ())
		if err != nil {
			LogFatal(MsgBotExecFail, err)
		}
	}
}

func run(cfg *Config, silent bool, skipReg bool, clearAll bool) error {
	// 1. Setup global context that responds to shutdown signals
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)

	// Add SIGUSR1 handler for goroutine dumping
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

	// 2. Config is already loaded, but ensure it's valid
	if cfg == nil {
		var err error
		cfg, err = LoadConfig()
		if err != nil {
			return fmt.Errorf(MsgConfigFailedToLoad, err)
		}
	}

	// 3. Create disgo client with retries for network resilience
	var client bot.Client
	var err error
	for i := 1; i <= 5; i++ {
		client, err = CreateClient(ctx, cfg)
		if err == nil {
			break
		}
		if i == 5 {
			return fmt.Errorf(MsgBotClientCreateFail, i, err)
		}
		LogWarn(MsgBotClientRetry, i, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	defer client.Close(ctx)

	// 4. Command Registration
	if !skipReg {
		if err := RegisterCommands(client, cfg.GuildID, clearAll); err != nil {
			LogError(MsgBotRegisterFail, err)
		}
	} else {
		LogInfo(MsgBotSkipReg)
	}

	// 5. Connect to Gateway
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf(MsgBotGatewayFail, err)
	}

	<-ctx.Done()
	if !silent {
		fmt.Println()
	}

	LogInfo(MsgDaemonShutdown)
	ShutdownDaemons(context.Background())

	LogInfo(MsgBotShutdown, GetProjectName())

	return nil
}

// ============================================================================
// Loader
// ============================================================================

const (
	MsgLoaderSyncCommands       = "Syncing %s commands..."
	MsgLoaderTransition         = "[TRANSITION] Switching from %s to %s mode."
	MsgLoaderCleanup            = "[CLEANUP] Removing commands from previous dev guild: %s"
	MsgLoaderDevStarting        = "[DEV] Registering commands to guild: %s"
	MsgLoaderDevRegistered      = "[DEV] Registered: %s"
	MsgLoaderDevFail            = "[DEV] Registration failed: %v"
	MsgLoaderDevGlobalClear     = "[DEV] Verifying global commands are cleared..."
	MsgLoaderDevGlobalClearFail = "[DEV] Global clear skipped (likely rate limited): %v"
	MsgLoaderProdStarting       = "[PROD] Registering commands globally..."
	MsgLoaderProdRegistered     = "[PROD] Registered: %s"
	MsgLoaderProdFail           = "[PROD] Global registration failed: %w"
	MsgLoaderScanStarting       = "[SCAN] Checking all guilds for ghost commands..."
	MsgLoaderScanCleared        = "[SCAN] Cleared ghost commands from: %s (%s)"
	MsgLoaderPanicRecovered     = "Panic recovered in handler: %v"
	MsgLoaderUpToDate           = "[LOADER] Commands are up to date. (Hash: %s)"
	MsgLoaderInvalidGuildID     = "invalid GUILD_ID: %w"
)

var AppContext context.Context
var RestartRequested bool
var daemonsOnce sync.Once
var StartupTime = time.Now()

var commands = []discord.ApplicationCommandCreate{}
var commandHandlers = map[string]func(event *events.ApplicationCommandInteractionCreate){}
var autocompleteHandlers = map[string]func(event *events.AutocompleteInteractionCreate){}
var componentHandlers = map[string]func(event *events.ComponentInteractionCreate){}
var voiceStateUpdateHandlers []func(event *events.GuildVoiceStateUpdate)
var onClientReadyCallbacks []func(ctx context.Context, client bot.Client)

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
		bot.WithEventListenerFunc(onMessageCreate),
		bot.WithEventListenerFunc(onMessageReactionAdd),
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
	_, _ = GetBotConfig(ctx, "last_reg_mode")
	lastGuildID, _ := GetBotConfig(ctx, "last_guild_id")

	isProduction := guildIDStr == ""
	currentMode := "guild"
	if isProduction {
		currentMode = "global"
	}

	LogInfo(MsgLoaderSyncCommands, strings.ToUpper(currentMode))

	currentHash := calculateCommandHash(commands)
	lastHash, _ := GetBotConfig(ctx, "last_cmd_hash")
	lastMode, _ := GetBotConfig(ctx, "last_reg_mode")

	shouldRegister := true
	if currentHash != "" && currentHash == lastHash && currentMode == lastMode && !forceScan {
		shouldRegister = false
		LogInfo(MsgLoaderUpToDate, currentHash[:8])
	}

	// 1. Production Mode (Global)
	if isProduction {
		if shouldRegister {
			LogInfo(MsgLoaderProdStarting)
			createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
			if err != nil {
				return fmt.Errorf(MsgLoaderProdFail, err)
			}
			for _, cmd := range createdCommands {
				LogInfo(MsgLoaderProdRegistered, cmd.Name())
			}
		}

		shouldScan := forceScan || (lastMode != currentMode)
		if shouldScan {
			LogInfo(MsgLoaderScanStarting)
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
								LogInfo(MsgLoaderScanCleared, guild.Name, guild.ID.String())
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
					LogInfo(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, id, []discord.ApplicationCommandCreate{})
				}
			}
		}
	} else {
		// 2. Development Mode (Guild)
		guildID, err := snowflake.Parse(guildIDStr)
		if err != nil {
			return fmt.Errorf(MsgLoaderInvalidGuildID, err)
		}

		if shouldRegister {
			LogInfo(MsgLoaderDevStarting, guildIDStr)
			createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
			if err != nil {
				LogWarn(MsgLoaderDevFail, err)
			} else {
				for _, cmd := range createdCommands {
					LogInfo(MsgLoaderDevRegistered, cmd.Name())
				}
			}
		}

		if lastMode != currentMode || forceScan {
			if cmds, err := client.Rest.GetGlobalCommands(client.ApplicationID, false); err == nil && len(cmds) > 0 {
				LogInfo(MsgLoaderDevGlobalClear)
				_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
				if err != nil {
					LogWarn(MsgLoaderDevGlobalClearFail, err)
				}
			}
		}

		if lastGuildID != "" && lastGuildID != guildIDStr {
			if oldID, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, oldID, false); err == nil && len(cmds) > 0 {
					LogInfo(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, oldID, []discord.ApplicationCommandCreate{})
				}
			}
		}

		if forceScan {
			LogInfo(MsgLoaderScanStarting)
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
								LogInfo(MsgLoaderScanCleared, guild.Name, guild.ID.String())
								_, _ = client.Rest.SetGuildCommands(client.ApplicationID, guild.ID, []discord.ApplicationCommandCreate{})
							}
						}(g)
					})
				}
				wg.Wait()
			}
		}
	}

	// 3. Update State
	_ = SetBotConfig(ctx, "last_reg_mode", currentMode)
	_ = SetBotConfig(ctx, "last_guild_id", guildIDStr)
	if currentHash != "" {
		_ = SetBotConfig(ctx, "last_cmd_hash", currentHash)
	}

	// 3. Update State
	_ = SetBotConfig(ctx, "last_reg_mode", currentMode)
	_ = SetBotConfig(ctx, "last_guild_id", guildIDStr)

	return nil
}

func onReady(event *events.Ready) {
	client := *event.Client()
	botUser := event.User

	// 1. Final Status
	duration := time.Since(StartupTime)
	LogInfo(MsgBotReady, GetProjectName(), botUser.ID.String(), os.Getpid(), duration.Milliseconds())

	// 2. Background Daemons
	TriggerClientReady(AppContext, client)
	StartDaemons(AppContext)
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
	// 1. Try exact match
	if h, ok := componentHandlers[customID]; ok {
		safeGo(func() { h(event) })
		return
	}

	// 2. Try prefix match
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

type daemonEntry struct {
	starter func(ctx context.Context) (bool, func(), func())
	logger  func(format string, v ...any)
}

var registeredDaemons []daemonEntry
var activeShutdownHooks []func()
var activeShutdownMu sync.Mutex

func RegisterDaemon(logger func(format string, v ...any), starter func(ctx context.Context) (bool, func(), func())) {
	registeredDaemons = append(registeredDaemons, daemonEntry{starter: starter, logger: logger})
}

func StartDaemons(ctx context.Context) {
	daemonsOnce.Do(func() {
		type activeDaemon struct {
			entry daemonEntry
			run   func()
		}
		var active []activeDaemon

		// 1. Evaluate starters sequentially to determine active daemons
		for _, daemon := range registeredDaemons {
			if ok, run, shutdown := daemon.starter(ctx); ok && run != nil {
				if shutdown != nil {
					activeShutdownMu.Lock()
					activeShutdownHooks = append(activeShutdownHooks, shutdown)
					activeShutdownMu.Unlock()
				}
				active = append(active, activeDaemon{daemon, run})
			}
		}

		// 2. Log all "Starting..." messages sequentially
		for _, ad := range active {
			ad.entry.logger(MsgDaemonStarting)
		}

		// 3. Launch the actual daemon loops in parallel
		for _, ad := range active {
			safeGo(ad.run)
		}
	})
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

// ============================================================================
// Log
// ============================================================================

var (
	// Level colors
	infoColor  = color.New()
	warnColor  = color.New(color.FgYellow)
	errorColor = color.New(color.FgRed)
	fatalColor = color.New(color.FgRed, color.Bold)

	// Global state
	DefaultTimeFormat = "15:04:05"
	IsSilent          = false
	LogToFile         = false
	Logger            *slog.Logger

	// Internal state
	logFile             *os.File
	logMu               sync.Mutex
	errorMapCache       map[string]string
	errorMapOnce        sync.Once
	onRateLimitExceeded func()
)

func init() {
	InitLogger(false, false)
}

// InitLogger initializes the global structured logger and returns the log filename if one was created
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

	handler := NewBotLogHandler(writer, &BotLogHandlerOptions{
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

func LogBot(format string, v ...any) {
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

func LogCustom(tag string, tagColor *color.Color, format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", tag))
}

type BotLogHandlerOptions struct {
	Silent bool
	Level  slog.Leveler
}

type BotLogHandler struct {
	w    io.Writer
	opts *BotLogHandlerOptions
	mu   *sync.Mutex
}

func NewBotLogHandler(w io.Writer, opts *BotLogHandlerOptions) *BotLogHandler {
	if opts == nil {
		opts = &BotLogHandlerOptions{Level: slog.LevelInfo}
	}
	return &BotLogHandler{
		w:    w,
		opts: opts,
		mu:   &sync.Mutex{},
	}
}

func (h *BotLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.opts.Silent {
		return false
	}
	return level >= h.opts.Level.Level()
}

func (h *BotLogHandler) Handle(ctx context.Context, r slog.Record) error {
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

		if atomic.LoadInt32(&isCleaningThreads) > 0 {
			return nil
		}
	}

	component := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" {
			component = strings.ToUpper(a.Value.String())
			return false
		}
		return true
	})

	fmt.Fprintf(h.w, "%s", timeStr)

	if component != "" {
		if levelStr != "INFO" {
			fmt.Fprintf(h.w, " %s", levelColor.Sprintf("[%s]", levelStr))
		}
		compColor := getComponentColor(component)
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(compColor, fmt.Sprintf("[%s] %s", component, r.Message)))
	} else {
		displayMsg := fmt.Sprintf("[%s] %s", levelStr, r.Message)
		if levelStr == "INFO" && strings.HasPrefix(r.Message, "[") {
			if idx := strings.Index(r.Message, "]"); idx > 0 && idx < 20 {
				displayMsg = r.Message
			}
		}
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(levelColor, displayMsg))
	}

	return nil
}

func (h *BotLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *BotLogHandler) WithGroup(name string) slog.Handler       { return h }

// ============================================================================
// Database Constants
// ============================================================================

const (
	MsgConfigInvalidGuildID    = "invalid GUILD_ID: must be a valid Snowflake"
	MsgDBMigrationFail         = "failed to migrate database: %w"
	MsgDBParseUserIDFail       = "failed to parse user ID '%s' for reminder %d: %w"
	MsgDBParseChannelIDFail    = "failed to parse channel ID '%s' for reminder %d: %w"
	MsgDBParseGuildIDFail      = "failed to parse guild ID '%s' for reminder %d: %w"
	MsgDBParseClaimUserFail    = "failed to parse user ID '%s' for claimed reminder %d: %w"
	MsgDBParseClaimChanFail    = "failed to parse channel ID '%s' for claimed reminder %d: %w"
	MsgDBParseClaimGuildFail   = "failed to parse guild ID '%s' for claimed reminder %d: %w"
	MsgDBParseDueUserFail      = "failed to parse user ID '%s' for due reminder %d: %w"
	MsgDBParseDueChanFail      = "failed to parse channel ID '%s' for due reminder %d: %w"
	MsgDBParseDueGuildFail     = "failed to parse guild ID '%s' for due reminder %d: %w"
	MsgDBParseLoopChanIDFail   = "failed to parse channel ID: %w"
	MsgDBScanLoopConfigFail    = "failed to scan loop config: %w"
	MsgDBParseLoopConfigIDFail = "failed to parse channel ID '%s' for loop config: %w"
	MsgDBParseRoleIDFail       = "failed to parse role ID: %w"
	MsgDBScanGuildConfigFail   = "failed to scan guild config: %w"
	MsgDBParseGuildIDColorFail = "failed to parse guild ID '%s' in random colors: %w"
	MsgDBParseRoleIDColorFail  = "failed to parse role ID '%s' in random colors: %w"

	// Environment Variables
	EnvDiscordToken = "DISCORD_TOKEN"
	EnvSilent       = "SILENT"
	EnvStreamingURL = "STREAMING_URL"
	EnvOwnerIDs     = "OWNER_IDS"
	EnvGuildID      = "GUILD_ID"
	EnvAIMaxLen     = "AI_MAX_LENGTH"
	EnvAIKeySize    = "AI_MAX_KEY_SIZE"
	EnvAITry        = "AI_ATTEMPTS"
	EnvAITempMin    = "AI_TEMPERATURE_MIN"
	EnvAITempMax    = "AI_TEMPERATURE_MAX"
	EnvAISeedChance = "AI_SEED_PREFIX_CHANCE"
	EnvAIRandChance = "AI_RANDOM_RESPONSE_CHANCE"
)

// --- Phase 1: Configuration & Environment ---

type Config struct {
	Token                  string
	GuildID                string
	DatabasePath           string
	OwnerIDs               []string
	StreamingURL           string
	Silent                 bool
	AIMaxLength            int
	AIMaxKeySize           int
	AIAttempts             int
	AITemperatureMin       float64
	AITemperatureMax       float64
	AISeedPrefixChance     float64
	AIRandomResponseChance float64
}

var GlobalConfig *Config

// LoadConfig initializes the configuration from environment variables.
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

	cfg.AIMaxLength, _ = strconv.Atoi(os.Getenv(EnvAIMaxLen))
	if cfg.AIMaxLength == 0 {
		cfg.AIMaxLength = 15
	}
	cfg.AIMaxKeySize, _ = strconv.Atoi(os.Getenv(EnvAIKeySize))
	if cfg.AIMaxKeySize == 0 {
		cfg.AIMaxKeySize = 1 // Default
	}
	cfg.AIAttempts, _ = strconv.Atoi(os.Getenv(EnvAITry))
	if cfg.AIAttempts == 0 {
		cfg.AIAttempts = 100
	}
	cfg.AITemperatureMin, _ = strconv.ParseFloat(os.Getenv(EnvAITempMin), 64)
	if cfg.AITemperatureMin == 0 {
		cfg.AITemperatureMin = 1.0
	}
	cfg.AITemperatureMax, _ = strconv.ParseFloat(os.Getenv(EnvAITempMax), 64)
	if cfg.AITemperatureMax == 0 {
		cfg.AITemperatureMax = 1.5
	}
	cfg.AISeedPrefixChance, _ = strconv.ParseFloat(os.Getenv(EnvAISeedChance), 64)
	if cfg.AISeedPrefixChance == 0 {
		cfg.AISeedPrefixChance = 0.1
	}
	cfg.AIRandomResponseChance, _ = strconv.ParseFloat(os.Getenv(EnvAIRandChance), 64)
	// No default for AIRandomResponseChance as 0.0 is a valid and desired default.

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

// --- Phase 2: Database Connection & Lifecycle ---

var DB *sql.DB

func InitDatabase(ctx context.Context, dataSourceName string) error {
	_ = sqlite3.SQLiteDriver{}

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

	// Data Migration: Normalize Content, Stickers, and Attachments
	// 1. Content
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

	// 2. Stickers
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

	// 3. Attachments
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

	// 4. Reactions
	if rows, err := DB.QueryContext(initCtx, "SELECT message_id, reactions FROM ai_messages WHERE reactions IS NOT NULL AND reactions != '' AND (reaction_hash IS NULL OR reaction_hash = '')"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, reactions string
			if err := rows.Scan(&id, &reactions); err == nil {
				// Legacy reactions were Comma separated, but for the Markov chain we treat them as individual tokens
				// For the database, we'll just hash the whole string (which might be single emoji or comma list)
				// though SaveAIMessage now handles them one by one or as a single REACTION: token?
				// Wait, SaveAIMessage takes a reactions string.
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

// --- Phase 3: Infrastructure & Bot Persistence ---

// BotConfig helpers are used by the loader for mode tracking and state.
func GetBotConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := DB.QueryRowContext(ctx, "SELECT value FROM bot_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetBotConfig(ctx context.Context, key, value string) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO bot_config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}

// --- Phase 4: Application Logic (Reminders) ---

type Reminder struct {
	ID        int64
	UserID    snowflake.ID
	ChannelID snowflake.ID
	GuildID   snowflake.ID
	Message   string
	RemindAt  time.Time
	SendTo    string
	CreatedAt time.Time
}

func AddReminder(ctx context.Context, r *Reminder) error {
	_, err := DB.ExecContext(ctx, `
		INSERT INTO reminders (user_id, channel_id, guild_id, message, remind_at, send_to)
		VALUES (?, ?, ?, ?, ?, ?)
	`, r.UserID.String(), r.ChannelID.String(), r.GuildID.String(), r.Message, r.RemindAt, r.SendTo)
	return err
}

func GetRemindersForUser(ctx context.Context, userID snowflake.ID) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE user_id = ? ORDER BY remind_at ASC
	`, userID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
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

func ClaimDueReminders(ctx context.Context) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		DELETE FROM reminders 
		WHERE remind_at <= ? 
		RETURNING id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
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

func GetDueReminders(ctx context.Context) ([]*Reminder, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT id, user_id, channel_id, guild_id, message, remind_at, send_to, created_at
		FROM reminders WHERE remind_at <= ? ORDER BY remind_at ASC
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []*Reminder
	for rows.Next() {
		r := &Reminder{}
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

// --- Phase 5: Application Logic (Loop Channels) ---

type LoopConfig struct {
	ChannelID     snowflake.ID
	ChannelName   string
	ChannelType   string
	Rounds        int
	Interval      int
	Message       string
	WebhookAuthor string
	WebhookAvatar string
	UseThread     bool
	ThreadMessage string
	ThreadCount   int
	Threads       string
	IsRunning     bool
	VoteChannelID string
	VoteRole      string
	VoteMessage   string
	VoteThreshold int
	IsSerial      bool
}

func AddLoopConfig(ctx context.Context, channelID snowflake.ID, config *LoopConfig) error {
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

func GetLoopConfig(ctx context.Context, channelID snowflake.ID) (*LoopConfig, error) {
	row := DB.QueryRowContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads, is_running,
			vote_panel, vote_role, vote_message, vote_threshold, is_serial
		FROM loop_channels WHERE channel_id = ?
	`, channelID.String())

	config := &LoopConfig{}
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

// --- AI Database ---

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

type AIMessageData struct {
	MessageID snowflake.ID
	Content   string
	AuthorID  snowflake.ID
	CreatedAt time.Time
}

func GetRecentAIMessages(ctx context.Context, channelID snowflake.ID, limit int) ([]*AIMessageData, error) {
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

	var messages []*AIMessageData
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
			messages = append(messages, &AIMessageData{
				MessageID: mID,
				Content:   msg,
				AuthorID:  aID,
				CreatedAt: createdAt,
			})
		}
	}
	return messages, nil
}

type AIMemoryDump struct {
	TextMessages   []string
	StickerIDs     []string
	ReactionEmojis []string
	AttachmentURLs []string
}

func GetAIMemoryDump(ctx context.Context) (*AIMemoryDump, error) {
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

	dump := &AIMemoryDump{}

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

	// 1. Delete matching messages
	for _, h := range hashes {
		_, err = tx.ExecContext(ctx, "DELETE FROM ai_messages WHERE content_hash = ? OR sticker_hash = ? OR reaction_hash = ? OR attachment_hash = ?", h, h, h, h)
		if err != nil {
			return err
		}
	}

	// 2. Delete from vocab
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

func GetAllLoopConfigs(ctx context.Context) ([]*LoopConfig, error) {
	rows, err := DB.QueryContext(ctx, `
		SELECT channel_id, channel_name, channel_type, rounds, interval,
			message, webhook_author, webhook_avatar,
			use_thread, thread_message, thread_count, threads, is_running,
			vote_panel, vote_role, vote_message, vote_threshold, is_serial
		FROM loop_channels
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []*LoopConfig
	for rows.Next() {
		config := &LoopConfig{}
		var idStr string
		var message, author, avatar, threadMsg, threads sql.NullString
		var votePanel, voteRole, voteMessage sql.NullString
		var useThread, isRunning, isSerial int

		err := rows.Scan(
			&idStr, &config.ChannelName, &config.ChannelType, &config.Rounds, &config.Interval,
			&message, &author, &avatar,
			&useThread, &threadMsg, &config.ThreadCount, &threads, &isRunning,
			&votePanel, &voteRole, &voteMessage, &config.VoteThreshold, &isSerial,
		)
		if err != nil {
			return nil, fmt.Errorf(MsgDBScanLoopConfigFail, err)
		}

		config.ChannelID, err = snowflake.Parse(idStr)
		if err != nil {
			return nil, fmt.Errorf(MsgDBParseLoopConfigIDFail, idStr, err)
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

		configs = append(configs, config)
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

// --- Phase 6: Application Logic (Guild Configs) ---

type GuildConfig struct {
	GuildID           string
	RandomColorRoleID string
	UpdatedAt         time.Time
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

// ============================================================================
// V2 Components
// ============================================================================

const (
	ComponentTypeSection      discord.ComponentType = 9
	ComponentTypeTextDisplay  discord.ComponentType = 10
	ComponentTypeThumbnail    discord.ComponentType = 11
	ComponentTypeMediaGallery discord.ComponentType = 12
	ComponentTypeFile         discord.ComponentType = 13
	ComponentTypeSeparator    discord.ComponentType = 14
	ComponentTypeContainer    discord.ComponentType = 17

	MessageFlagsIsComponentsV2 discord.MessageFlags = 1 << 15
)

type UnfurledMediaItem struct {
	URL string `json:"url"`
}

type MediaGalleryItem struct {
	Media       UnfurledMediaItem `json:"media"`
	Description *string           `json:"description,omitempty"`
	Spoiler     bool              `json:"spoiler,omitempty"`
}

type MediaGallery struct {
	CType discord.ComponentType `json:"type"`
	ID    int                   `json:"id,omitempty"`
	Items []MediaGalleryItem    `json:"items"`
}

func (m MediaGallery) GetID() int {
	return 0
}

func (m MediaGallery) Type() discord.ComponentType {
	return ComponentTypeMediaGallery
}

type Thumbnail struct {
	CType       discord.ComponentType `json:"type"`
	Media       UnfurledMediaItem     `json:"media"`
	Description *string               `json:"description,omitempty"`
	Spoiler     bool                  `json:"spoiler,omitempty"`
}

func (t Thumbnail) GetID() int {
	return 0
}

func (t Thumbnail) Type() discord.ComponentType {
	return ComponentTypeThumbnail
}

type File struct {
	CType       discord.ComponentType `json:"type"`
	File        UnfurledMediaItem     `json:"file"`
	Description *string               `json:"description,omitempty"`
	Spoiler     bool                  `json:"spoiler,omitempty"`
	Filename    string                `json:"filename,omitempty"`
}

func (f File) GetID() int {
	return 0
}

func (f File) Type() discord.ComponentType {
	return ComponentTypeFile
}

type Separator struct {
	CType   discord.ComponentType `json:"type"`
	Divider bool                  `json:"divider,omitempty"`
	Spacing SeparatorSpacing      `json:"spacing,omitempty"`
}

func (s Separator) GetID() int {
	return 0
}

func (s Separator) Type() discord.ComponentType {
	return ComponentTypeSeparator
}

type TextDisplay struct {
	CType   discord.ComponentType `json:"type"`
	Content string                `json:"content"`
}

func (t TextDisplay) GetID() int {
	return 0
}

func (t TextDisplay) Type() discord.ComponentType {
	return ComponentTypeTextDisplay
}

type Section struct {
	CType      discord.ComponentType `json:"type"`
	Components []any                 `json:"components"`
	Accessory  any                   `json:"accessory,omitempty"`
}

func (s Section) GetID() int {
	return 0
}

func (s Section) Type() discord.ComponentType {
	return ComponentTypeSection
}

type Container struct {
	CType      discord.ComponentType `json:"type"`
	Components []any                 `json:"components"`
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

type SeparatorSpacing int

const (
	SeparatorSpacingSmall  SeparatorSpacing = 0
	SeparatorSpacingMedium SeparatorSpacing = 1
	SeparatorSpacingLarge  SeparatorSpacing = 2
)

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

func RespondInteractionContainerV2Files(client bot.Client, interaction discord.Interaction, container Container, files []*discord.File, ephemeral bool) error {
	var flags discord.MessageFlags
	if ephemeral {
		flags = discord.MessageFlagEphemeral | MessageFlagsIsComponentsV2
	} else {
		flags = MessageFlagsIsComponentsV2
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
			Components: []any{container},
			Flags:      flags,
		},
	}

	// Manual multipart construction
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 1. Payload JSON
	part, err := writer.CreateFormField("payload_json")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(part)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(data); err != nil {
		return err
	}

	// 2. Files
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

	// 3. Request
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

// SendMessageV2 sends a message using the components v2 flag
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

// ============================================================================
// Helper Functions
// ============================================================================
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

func TruncateWithPreserve(text string, maxLen int, prefix, suffix string) string {
	rp, rs := []rune(prefix), []rune(suffix)
	fixedLen := len(rp) + len(rs)
	if fixedLen >= maxLen-10 {
		return TruncateCenter(prefix+text+suffix, maxLen)
	}
	return prefix + TruncateCenter(text, maxLen-fixedLen) + suffix
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

type StripANSIWriter struct {
	w  io.Writer
	re *regexp.Regexp
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
	if name == "DATABASE" {
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

var HttpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func SetAppContext(ctx context.Context) {
	AppContext = ctx
}
