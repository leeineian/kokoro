package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
	"github.com/fatih/color"
	kai "github.com/leeineian/kokoro/ai"
	kapp "github.com/leeineian/kokoro/app"
	kfun "github.com/leeineian/kokoro/fun"
	kloop "github.com/leeineian/kokoro/loop"
	kvoice "github.com/leeineian/kokoro/voice"
	_ "gosqlite.org"
)

const (
	MsgConfigFailedToLoad                             = "Failed to load config: %v"
	MsgConfigMissingToken                             = "DISCORD_TOKEN is not set in .env file"
	MsgDatabaseInitSuccess                            = "Database initialized successfully"
	MsgDatabaseTableError                             = "Failed to create table: %w"
	MsgDatabasePragmaError                            = "Failed to set pragma %s: %w"
	MsgDaemonStarting                                 = "Starting..."
	MsgAppStarting                                    = "Starting %s..."
	MsgAppReady                                       = "%s is ready! (ID: %s) (PID: %d) (Took: %dms)"
	MsgAppShutdown                                    = "Shutting down %s..."
	MsgAppKillingOld                                  = "Killing running instance... (PID: %d)"
	MsgAppKillFail                                    = "Failed to kill old instance: %v"
	MsgAppOldTerminated                               = "Old instance terminated."
	MsgAppPIDWriteFail                                = "Failed to write PID file: %v"
	MsgAppRegisterFail                                = "Command registration failed: %v"
	MsgAppAPIStatusError                              = "discord API returned status %d"
	MsgGenericError                                   = "%v"
	MsgInitializing                                   = "Initializing %s..."
	MsgDatabaseInitFail                               = "Failed to initialize database: %v"
	MsgPIDOpenFail                                    = "Failed to open PID file: %v"
	MsgPIDLockFail                                    = "Failed to lock PID file: %v"
	MsgAppStubbornOld                                 = "Old process %d is stubborn. Sending SIGKILL..."
	MsgAppKillResistant                               = "Process %d still exists after SIGKILL"
	MsgAppRestarting                                  = "Self-restarting process..."
	MsgAppStartPathFail                               = "Failed to resolve executable path: %v"
	MsgAppExecFail                                    = "Failed to re-execute: %v"
	MsgSignalDumpParams                               = "Received SIGUSR1, dumping goroutines to goroutines.txt"
	MsgSignalDumpCreateFail                           = "Failed to create goroutines.txt: %v"
	MsgSignalDumpSuccess                              = "Goroutines dumped"
	MsgAppClientCreateFail                            = "failed to create Discord client after %d attempts: %w"
	MsgAppClientRetry                                 = "Failed to create Discord client (attempt %d/5): %v. Retrying in 5s..."
	MsgAppGatewayFail                                 = "failed to open gateway: %w"
	MsgPanicFatal                                     = "\n[FATAL] %s\n"
	MsgComponentReady                                 = "Ready! (Took: %dms)"
	AppPIDFile                                        = ".app.pid"
	MsgLoaderSyncCommands                             = "Syncing %s commands..."
	MsgLoaderTransition                               = "Switching from %s to %s mode."
	MsgLoaderCleanup                                  = "Removing commands from previous dev guild: %s"
	MsgLoaderDevStarting                              = "Registering commands to guild: %s"
	MsgLoaderDevRegistered                            = "Registered: %s"
	MsgLoaderDevFail                                  = "Registration failed: %v"
	MsgLoaderDevGlobalClear                           = "Verifying global commands are cleared..."
	MsgLoaderDevGlobalClearFail                       = "Global clear skipped (likely rate limited): %v"
	MsgLoaderProdStarting                             = "Registering commands globally..."
	MsgLoaderProdRegistered                           = "Registered: %s"
	MsgLoaderProdFail                                 = "Global registration failed: %w"
	MsgLoaderScanStarting                             = "Checking all guilds for ghost commands..."
	MsgLoaderScanCleared                              = "Cleared ghost commands from: %s (%s)"
	MsgLoaderPanicRecovered                           = "Panic recovered in handler: %v"
	MsgLoaderUpToDate                                 = "Commands are up to date. (Hash: %s)"
	MsgLoaderInvalidGuildID                           = "invalid GUILD_ID: %w"
	MsgConfigInvalidGuildID                           = "invalid GUILD_ID: must be a valid Snowflake"
	MsgDBMigrationFail                                = "failed to migrate database: %w"
	MsgDBParseUserIDFail                              = "failed to parse user ID '%s' for reminder %d: %w"
	MsgDBParseChannelIDFail                           = "failed to parse channel ID '%s' for reminder %d: %w"
	MsgDBParseGuildIDFail                             = "failed to parse guild ID '%s' for reminder %d: %w"
	MsgDBParseClaimUserFail                           = "failed to parse user ID '%s' for claimed reminder %d: %w"
	MsgDBParseClaimChanFail                           = "failed to parse channel ID '%s' for claimed reminder %d: %w"
	MsgDBParseClaimGuildFail                          = "failed to parse guild ID '%s' for claimed reminder %d: %w"
	MsgDBParseDueUserFail                             = "failed to parse user ID '%s' for due reminder %d: %w"
	MsgDBParseDueChanFail                             = "failed to parse channel ID '%s' for due reminder %d: %w"
	MsgDBParseDueGuildFail                            = "failed to parse guild ID '%s' for due reminder %d: %w"
	MsgDBParseLoopChanIDFail                          = "failed to parse channel ID: %w"
	MsgDBScanLoopConfigFail                           = "failed to scan loop config: %w"
	MsgDBParseLoopConfigIDFail                        = "failed to parse channel ID '%s' for loop config: %w"
	MsgDBParseRoleIDFail                              = "failed to parse role ID: %w"
	MsgDBScanGuildConfigFail                          = "failed to scan guild config: %w"
	MsgDBParseGuildIDColorFail                        = "failed to parse guild ID '%s' in random colors: %w"
	MsgDBParseRoleIDColorFail                         = "failed to parse role ID '%s' in random colors: %w"
	EnvDiscordToken                                   = "DISCORD_TOKEN"
	EnvSilent                                         = "SILENT"
	EnvStreamingURL                                   = "STREAMING_URL"
	EnvOwnerIDs                                       = "OWNER_IDS"
	EnvGuildID                                        = "GUILD_ID"
	ComponentTypeSection        discord.ComponentType = 9
	ComponentTypeTextDisplay    discord.ComponentType = 10
	ComponentTypeThumbnail      discord.ComponentType = 11
	ComponentTypeMediaGallery   discord.ComponentType = 12
	ComponentTypeFile           discord.ComponentType = 13
	ComponentTypeSeparator      discord.ComponentType = 14
	ComponentTypeContainer      discord.ComponentType = 17
	MessageFlagsIsComponentsV2  discord.MessageFlags  = 1 << 15
	SeparatorSpacingSmall       SeparatorSpacing      = 0
	SeparatorSpacingMedium      SeparatorSpacing      = 1
	SeparatorSpacingLarge       SeparatorSpacing      = 2
)

var GlobalConfig *Config

var AppContext context.Context
var daemonsOnce sync.Once
var registeredDaemons []daemonEntry
var StartupTime = time.Now()
var activeShutdownHooks []func()
var activeShutdownMu sync.Mutex
var RestartRequested bool

var commands = []discord.ApplicationCommandCreate{}
var commandHandlers = map[string]func(event *events.ApplicationCommandInteractionCreate){}
var autocompleteHandlers = map[string]func(event *events.AutocompleteInteractionCreate){}
var componentHandlers = map[string]func(event *events.ComponentInteractionCreate){}
var voiceStateUpdateHandlers []func(event *events.GuildVoiceStateUpdate)
var onClientReadyCallbacks []func(ctx context.Context, client bot.Client)

var DB *sql.DB

var (
	infoColor  = color.New()
	warnColor  = color.New(color.FgYellow)
	errorColor = color.New(color.FgRed)
	fatalColor = color.New(color.FgRed, color.Bold)

	DefaultTimeFormat = "15:04:05"
	IsSilent          = false
	LogToFile         = false
	Logger            *slog.Logger

	logFile             *os.File
	logMu               sync.Mutex
	errorMapCache       map[string]string
	errorMapOnce        sync.Once
	onRateLimitExceeded func()
)

type daemonEntry struct {
	name    string
	logger  func(format string, v ...any)
	starter func(ctx context.Context) (bool, func(), func())
}

type AppLogHandlerOptions struct {
	Silent bool
	Level  slog.Leveler
}

type AppLogHandler struct {
	w    io.Writer
	opts *AppLogHandlerOptions
	mu   *sync.Mutex
}

type Config struct {
	Token        string
	GuildID      string
	DatabasePath string
	OwnerIDs     []string
	StreamingURL string
	Silent       bool
}

type GuildConfig struct {
	GuildID           string
	RandomColorRoleID string
	UpdatedAt         time.Time
}

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

type Thumbnail struct {
	CType       discord.ComponentType `json:"type"`
	Media       UnfurledMediaItem     `json:"media"`
	Description *string               `json:"description,omitempty"`
	Spoiler     bool                  `json:"spoiler,omitempty"`
}

type File struct {
	CType       discord.ComponentType `json:"type"`
	File        UnfurledMediaItem     `json:"file"`
	Description *string               `json:"description,omitempty"`
	Spoiler     bool                  `json:"spoiler,omitempty"`
	Filename    string                `json:"filename,omitempty"`
}

type Separator struct {
	CType   discord.ComponentType `json:"type"`
	Divider bool                  `json:"divider,omitempty"`
	Spacing SeparatorSpacing      `json:"spacing,omitempty"`
}

type TextDisplay struct {
	CType   discord.ComponentType `json:"type"`
	Content string                `json:"content"`
}

type Section struct {
	CType      discord.ComponentType `json:"type"`
	Components []any                 `json:"components"`
	Accessory  any                   `json:"accessory,omitempty"`
}

type Container struct {
	CType      discord.ComponentType `json:"type"`
	Components []any                 `json:"components"`
}

type SeparatorSpacing int

func main() {
	defer func() {
		if r := recover(); r != nil {
			if msg, ok := r.(string); ok {
				fmt.Fprintf(os.Stderr, MsgPanicFatal, msg)
				os.Exit(1)
			}
			panic(r)
		}
	}()

	cfg, err := LoadConfig()
	if err != nil {
		LogError(MsgConfigFailedToLoad, err)
	}

	silent := flag.Bool("silent", false, "Disable all log output")
	refresh := flag.Bool("refresh", false, "Force refresh guild commands (scan all guilds)")
	flag.Parse()

	logName := InitLogger(*silent, true)

	botName := GetProjectName()

	LogInfo(MsgAppStarting, botName)

	LogInfo(MsgInitializing, filepath.Base(cfg.DatabasePath))
	if logName != "" {
		LogInfo(MsgInitializing, filepath.Base(logName))
	}

	if err := InitDatabase(context.Background(), cfg.DatabasePath); err != nil {
		LogFatal(MsgDatabaseInitFail, err)
	}

	kai.Init(kai.Hooks{
		DB:                          DB,
		SaveMessage:                 SaveAIMessage,
		GetRecentMessages:           GetRecentAIMessages,
		GetMemoryDump:               GetAIMemoryDump,
		ClearMessages:               ClearAIMessages,
		ClearAllMessages:            ClearAllAIMessages,
		GetAllVocab:                 GetAllAIVocab,
		ClearMessagesByHashes:       ClearAIMessagesByHashes,
		GetChannelsWithMemory:       GetChannelsWithAIMemory,
		FetchAIHistory:              FetchAIHistory,
		SplitAIMessage:              SplitAIMessage,
		Log:                         Log,
		LogError:                    LogError,
		LogInfo:                     LogInfo,
		LogApp:                      LogApp,
		RegisterCommand:             RegisterCommand,
		RegisterAutocompleteHandler: RegisterAutocompleteHandler,
		RegisterDaemon:              RegisterDaemon,
		OnClientReady:               OnClientReady,
		AdminPerm:                   discord.PermissionAdministrator,
	})

	kapp.Init(kapp.Hooks{
		LogApp:                        LogApp,
		LogInfo:                       LogInfo,
		LogError:                      LogError,
		LogWarn:                       LogWarn,
		LogDebug:                      LogDebug,
		RegisterCommand:               RegisterCommand,
		RegisterAutocompleteHandler:   RegisterAutocompleteHandler,
		RegisterComponentHandler:      RegisterComponentHandler,
		RespondInteractionV2:          RespondInteractionV2,
		EditInteractionV2:             EditInteractionV2,
		RegisterDaemon:                RegisterDaemon,
		OnClientReady:                 OnClientReady,
		AdminPerm:                     int64(discord.PermissionAdministrator),
		StartupTime:                   StartupTime,
		LogFile:                       logName,
		GetProjectName:                GetProjectName,
		AppContext:                    AppContext,
		RequestRestart:                func() { RestartRequested = true },
		GetAppConfig:                  GetAppConfig,
		SetAppConfig:                  SetAppConfig,
		GetRemindersCount:             GetRemindersCount,
		GetAllGuildRandomColorConfigs: GetAllGuildRandomColorConfigs,
		SetGuildRandomColorRole:       SetGuildRandomColorRole,
		GetGuildRandomColorRole:       GetGuildRandomColorRole,
		RandomIntRange:                RandomIntRange,
		DeleteAllRemindersForUser:     DeleteAllRemindersForUser,
		DeleteReminder:                DeleteReminder,
		GetRemindersForUser:           GetRemindersForUser,
		AddReminder:                   AddReminder,
		ClaimDueReminders:             ClaimDueReminders,
		StreamingURL:                  GlobalConfig.StreamingURL,
	})

	kfun.Init(kfun.Hooks{
		LogInfo:                     LogInfo,
		LogError:                    LogError,
		RegisterCommand:             RegisterCommand,
		RegisterAutocompleteHandler: RegisterAutocompleteHandler,
		RegisterComponentHandler:    RegisterComponentHandler,
		RespondInteractionV2:        RespondInteractionV2,
		RespondInteractionContainerV2: func(client bot.Client, interaction discord.Interaction, container any, ephemeral bool) error {
			return RespondInteractionContainerV2(client, interaction, container.(Container), ephemeral)
		},
		EditInteractionV2: EditInteractionV2,
		EditInteractionContainerV2: func(client bot.Client, interaction discord.Interaction, container any) error {
			return EditInteractionContainerV2(client, interaction, container.(Container))
		},
		UpdateInteractionContainerV2: func(client bot.Client, interaction discord.Interaction, container any) error {
			return UpdateInteractionContainerV2(client, interaction, container.(Container))
		},
		EditContainerV2: func(client bot.Client, channelID snowflake.ID, messageID snowflake.ID, container any, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error) {
			return EditContainerV2(client, channelID, messageID, container.(Container), stickers, embeds)
		},
		NewV2Container:  func(components ...interface{}) any { return NewV2Container(components...) },
		NewTextDisplay:  func(content string) any { return NewTextDisplay(content) },
		NewMediaGallery: func(urls ...string) any { return NewMediaGallery(urls...) },
		NewSeparator:    func(divider bool) any { return NewSeparator(divider) },
		AppContext:      AppContext,
		HttpClient:      HttpClient,
	})

	kloop.Init(kloop.Hooks{
		LogInfo:                     LogInfo,
		LogWarn:                     LogWarn,
		LogError:                    LogError,
		LogDebug:                    LogDebug,
		RegisterCommand:             RegisterCommand,
		RegisterAutocompleteHandler: RegisterAutocompleteHandler,
		RegisterComponentHandler:    RegisterComponentHandler,
		RegisterDaemon:              RegisterDaemon,
		RespondInteractionV2:        RespondInteractionV2,
		EditInteractionV2:           EditInteractionV2,
		SendComponentsV2:            SendComponentsV2,
		NewV2Container:              func(components ...any) any { return NewV2Container(components...) },
		NewTextDisplay:              func(text string) any { return NewTextDisplay(text) },
		AppContext:                  AppContext,
		FormatDuration:              FormatDuration,
		AdminPerm:                   int64(discord.PermissionAdministrator),
		GetAppConfig:                GetAppConfig,
		OnClientReady:               OnClientReady,
		DefaultTimeFormat:           DefaultTimeFormat,
		ResetAllLoopStates:          ResetAllLoopStates,
		IntervalMsToDuration:        IntervalMsToDuration,
		SetLoopState:                SetLoopState,
		OnRateLimitExceeded:         func(f func()) { onRateLimitExceeded = f },
		SetLoopConfigDB:             AddLoopConfig,
		DeleteLoopConfigDB:          DeleteLoopConfigDB,
		GetLoopConfigDB:             GetLoopConfig,
		GetAllLoopConfigsDB:         GetAllLoopConfigs,
	})

	kvoice.Init(kvoice.Hooks{
		LogInfo:                     LogInfo,
		LogWarn:                     LogWarn,
		LogError:                    LogError,
		LogDebug:                    LogDebug,
		RegisterCommand:             RegisterCommand,
		RegisterAutocompleteHandler: RegisterAutocompleteHandler,
		RegisterComponentHandler:    RegisterComponentHandler,
		RegisterDaemon:              RegisterDaemon,
		RespondInteractionV2:        RespondInteractionV2,
		EditInteractionV2:           EditInteractionV2,
		EditInteractionContainerV2: func(client bot.Client, interaction discord.Interaction, c any) error {
			return EditInteractionContainerV2(client, interaction, c.(Container))
		},
		EditInteractionContainerV2ByToken: func(client bot.Client, appID snowflake.ID, token string, c any) error {
			return EditInteractionContainerV2ByToken(client, appID, token, c.(Container))
		},
		SendComponentsV2:     SendComponentsV2,
		NewV2Container:       func(components ...any) any { return NewV2Container(components...) },
		NewTextDisplay:       func(text string) any { return NewTextDisplay(text) },
		NewSeparator:         func(b bool) any { return NewSeparator(b) },
		NewMediaGallery:      func(urls ...string) any { return NewMediaGallery(urls...) },
		AppContext:           AppContext,
		FormatDuration:       FormatDuration,
		Truncate:             Truncate,
		TruncateWithPreserve: TruncateWithPreserve,
		TruncateCenter:       TruncateCenter,
		AdminPerm:            int64(discord.PermissionAdministrator),
		GetAppConfig:         GetAppConfig,
		OnClientReady:        OnClientReady,
		OnVoiceStateUpdate:   RegisterVoiceStateUpdateHandler,
	})

	defer CloseDatabase()

	f, err := os.OpenFile(AppPIDFile, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		LogFatal(MsgPIDOpenFail, err)
	}
	defer f.Close()

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
			f, _ = os.OpenFile(AppPIDFile, os.O_RDWR|os.O_CREATE, 0644)
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

		LogInfo(MsgAppKillingOld, oldPid)
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
			LogWarn(MsgAppStubbornOld, oldPid)
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
					LogWarn(MsgAppKillResistant, oldPid)
					break killWait
				}
			}
		}

		LogInfo(MsgAppOldTerminated)
	}

	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Sync()

	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = os.Remove(AppPIDFile)
	}()

	if err := run(cfg, *silent, *refresh); err != nil {
		LogFatal(MsgGenericError, err)
	}
	if RestartRequested {
		LogInfo(MsgAppRestarting)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(AppPIDFile)

		args := os.Args
		hasSkipReg := slices.Contains(args, "-skip-reg")
		if !hasSkipReg {
			args = append(args, "-skip-reg")
		}

		exePath, err := os.Executable()
		if err != nil {
			LogFatal(MsgAppStartPathFail, err)
		}

		err = syscall.Exec(exePath, args, os.Environ())
		if err != nil {
			LogFatal(MsgAppExecFail, err)
		}
	}
}
