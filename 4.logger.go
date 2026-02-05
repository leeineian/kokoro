package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
)

// --- Globals & Styles ---

var (
	// Level colors
	infoColor  = color.New()
	warnColor  = color.New(color.FgYellow)
	errorColor = color.New(color.FgRed)
	fatalColor = color.New(color.FgRed, color.Bold)

	// Component colors
	databaseColor      = color.New()
	reminderColor      = color.New(color.FgMagenta)
	statusRotatorColor = color.New(color.FgMagenta)
	roleRotatorColor   = color.New(color.FgMagenta)
	loopManagerColor   = color.New(color.FgMagenta)
	catColor           = color.New(color.FgMagenta)
	undertextColor     = color.New(color.FgMagenta)
	voiceColor         = color.New(color.FgMagenta)

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

// --- Initialization ---

func init() {
	InitLogger(false, false)
}

// InitLogger initializes the global structured logger
func InitLogger(silent bool, saveToFile bool) {
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

	if LogToFile {
		exePath, exeErr := os.Executable()
		logName := GetProjectName() + ".log"
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
}

func SetSilentMode(silent bool) {
	InitLogger(silent, LogToFile)
}

// --- Public Logging API ---

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

// Component Loggers

func LogDatabase(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "database"))
}

func LogReminder(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "reminder"))
}

func LogStatusRotator(format string, v ...any) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "session"))
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

// --- Log Handler Implementation ---

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
			go onRateLimitExceeded()
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

// --- Formatting Helpers ---

func getComponentColor(name string) *color.Color {
	switch name {
	case "DATABASE":
		return databaseColor
	case "REMINDER":
		return reminderColor
	case "SESSION":
		return statusRotatorColor
	case "ROLE":
		return roleRotatorColor
	case "LOOP":
		return loopManagerColor
	case "CAT":
		return catColor
	case "UNDERTEXT":
		return undertextColor
	case "VOICE":
		return voiceColor
	default:
		return color.New(color.FgCyan)
	}
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

// --- Utilities & State ---

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

// --- ANSI Stripper ---

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

// --- Message Constants ---

const (
	// --- Infrastructure & Lifecycle ---
	MsgConfigFailedToLoad  = "Failed to load config: %v"
	MsgConfigMissingToken  = "DISCORD_TOKEN is not set in .env file"
	MsgDatabaseInitSuccess = "Database initialized successfully"
	MsgDatabaseTableError  = "Failed to create table: %w"
	MsgDatabasePragmaError = "Failed to set pragma %s: %w"
	MsgDaemonStarting      = "Starting..."
	MsgBotStarting         = "Starting %s..."
	MsgBotReady            = "%s is ready! (ID: %s) (PID: %d) (Took: %dms)"
	MsgBotShutdown         = "Shutting down %s..."
	MsgBotKillingOld       = "Killing running instance... (PID: %d)"
	MsgBotKillFail         = "Failed to kill old instance: %v"
	MsgBotOldTerminated    = "Old instance terminated."
	MsgBotPIDWriteFail     = "Failed to write PID file: %v"
	MsgBotRegisterFail     = "Command registration failed: %v"
	MsgBotAPIStatusError   = "discord API returned status %d"
	MsgGenericError        = "%v"

	// --- Command Loader & Registry ---
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

	// --- Cat System ---
	MsgCatFailedToFetchFact         = "Failed to fetch cat fact: %v"
	MsgCatFactAPIStatusError        = "Cat fact API returned status %d"
	MsgCatFailedToDecodeFact        = "Failed to decode cat fact: %v"
	MsgCatFailedToFetchImage        = "Failed to fetch cat image: %v"
	MsgCatImageAPIStatusError       = "Cat image API returned status %d"
	MsgCatFailedToDecodeImage       = "Failed to decode cat image response: %v"
	MsgCatImageAPIEmptyArray        = "Cat image API returned empty array"
	MsgCatCannotSendErrorResponse   = "Cannot send error response: nil session or interaction"
	MsgCatFailedToSendErrorResponse = "Failed to send error response: %v"
	MsgCatFactAPIUnreachable        = "**API Unreachable**: The cat fact service is currently offline or timing out.\n> _%v_"
	MsgCatImageAPIUnreachable       = "**API Unreachable**: The cat image service is currently offline or timing out.\n> _%v_"
	MsgCatSystemStatus              = "**Cat System Status**\n\n" +
		"**External APIs:**\n" +
		"> • Cat Facts: `https://catfact.ninja/fact`\n" +
		"> • Cat Images: `https://api.thecatapi.com/v1`\n" +
		"> • ASCII Engine: `Kokoro Internal ANSI v1`\n\n" +
		"**Usage Tip:** Use `/cat say` with `catcolor` and `expression` for custom ASCII art!"
	MsgCatAPIStatusErrorDisp      = "**Service Error**: The API returned an unexpected status code: **%d %s**"
	MsgCatDataError               = "**Data Error**: Failed to read the response body from the API."
	MsgCatFormatError             = "**Format Error**: The API returned data in an invalid format."
	MsgCatFormatErrorExt          = "**Format Error**: The API returned data in an invalid format.\n> _%v_"
	MsgCatImageEmptyResult        = "**Empty Result**: The API returned an empty list of images."
	ErrCatFailedToFetchFact       = "Failed to fetch cat fact"
	ErrCatFactServiceUnavailable  = "Cat fact service is unavailable"
	ErrCatFailedToDecodeFact      = "Failed to decode cat fact"
	ErrCatFailedToFetchImage      = "Failed to fetch cat image"
	ErrCatImageServiceUnavailable = "Cat image service is unavailable"
	ErrCatFailedToDecodeImage     = "Failed to decode cat image"
	ErrCatNoImagesAvailable       = "No cat images available"

	// --- Reminder System ---
	MsgReminderFailedToQueryDue      = "Failed to query due reminders: %v"
	MsgReminderFailedToCreateDM      = "Failed to create DM channel for user %s: %v"
	MsgReminderFailedToSend          = "Failed to send reminder %d: %v"
	MsgReminderFailedToDelete        = "Failed to delete sent reminder %d: %v"
	MsgReminderFailedToDeleteGeneral = "Failed to delete reminder: %v"
	MsgReminderSentAndDeleted        = "Sent and deleted reminder %d for user %s"
	MsgReminderFailedToSave          = "Failed to save reminder: %v"
	MsgReminderFailedToDeleteAll     = "Failed to delete all reminders: %v"
	MsgReminderFailedToQuery         = "Failed to query reminders: %v"
	MsgReminderAutocompleteFailed    = "Failed to query reminders for autocomplete: %v"
	MsgReminderRespondError          = "Failed to respond to interaction: %v"
	MsgReminderNaturalTimeInitFail   = "Failed to initialize naturaltime parser: %v"
	ErrReminderParseFailed           = "Failed to parse the date/time. Try formats like 'tomorrow', 'in 2 hours', 'next friday at 3pm'."
	ErrReminderPastTime              = "The reminder time must be in the future!"
	ErrReminderSaveFailed            = "Failed to save reminder. Please try again."
	ErrReminderFetchFailed           = "Failed to retrieve your reminders."
	ErrReminderDismissFailed         = "Failed to dismiss reminder."
	ErrReminderDismissAllFail        = "Failed to dismiss all reminders."
	MsgReminderSetSuccess            = "Reminder set for %s\n\n %s"
	MsgReminderDismissedBatch        = "Dismissed **%d** reminder(s)!"
	MsgReminderNoActive              = "You have no active reminders. Set one with `/reminder set`!"
	MsgReminderDismissed             = "Reminder dismissed!"
	MsgReminderListHeader            = "**Your Reminders** (%d active)\n\n"
	MsgReminderListItem              = "%d. **%s** - %s\n"
	MsgReminderChoiceAll             = "Dismiss All (%d reminders)"
	MsgReminderStatsHeader           = "**Your Active Reminders (%d)**\n\n"
	MsgReminderStatsMore             = "> ...and %d more."
	MsgReminderStatsDue              = "> Due %s (`%s`)\n"
	MsgReminderStatsDM               = "> Delivery: Direct Message\n"
	MsgReminderRelLessMinute         = "in less than a minute"
	MsgReminderRelMinute             = "in 1 minute"
	MsgReminderRelMinutes            = "in %d minutes"
	MsgReminderRelHour               = "in 1 hour"
	MsgReminderRelHours              = "in %d hours"
	MsgReminderRelDay                = "in 1 day"
	MsgReminderRelDays               = "in %d days"
	MsgReminderRelWeek               = "in 1 week"
	MsgReminderRelWeeks              = "in %d weeks"
	MsgReminderRelMonth              = "in 1 month"
	MsgReminderRelMonths             = "in %d months"
	MsgReminderRelYear               = "in 1 year"
	MsgReminderRelYears              = "in %d years"

	// --- Loop System ---
	MsgLoopFailedToLoadConfigs   = "Failed to load configs: %v"
	MsgLoopLoadedChannels        = "Loaded configuration for %d categories."
	MsgLoopFailedToResume        = "Failed to resume %s: %v"
	MsgLoopResuming              = "Resuming %d active loops..."
	MsgLoopWebhookLimitReached   = "Channel %s has 10 webhooks, skipping"
	MsgLoopPreparedWebhook       = "Prepared webhook for channel: %s"
	MsgLoopFailedToFetchWebhooks = "Failed to fetch webhooks for %s: %v"
	MsgLoopFailedToCreateWebhook = "Failed to create webhook for %s: %v"
	MsgLoopPreparedCategoryHooks = "Prepared %d webhooks for category: %s"
	MsgLoopStartingTimed         = "Starting timed loop for %s"
	MsgLoopTimeLimitReached      = "Time limit reached for %s"
	MsgLoopStartingRandom        = "Starting infinite random mode for %s"
	MsgLoopRandomStatus          = "[%s] Random: %d rounds (%d pings), next delay: %s"
	MsgLoopRateLimited           = "[%s] Rate limited. Retrying in %v (Attempt %d/3)"
	MsgLoopSendFail              = "Failed to send to %s: %v"
	MsgLoopRenameFail            = "Failed to rename channel: %v"
	MsgLoopStopped               = "Stopped loop for: %s"
	MsgLoopConfigured            = "Configured channel: %s"
	MsgLoopEraseNoConfigs        = "No configurations were found to erase."
	MsgLoopErasedBatch           = "Erased **%d** configuration(s)."
	MsgLoopErrInvalidSelection   = "Invalid selection."
	MsgLoopErrConfigNotFound     = "Configuration not found."
	MsgLoopDeleteFail            = "Failed to delete configuration for **%s**: %v"
	MsgLoopDeleted               = "Deleted configuration for **%s**."
	MsgLoopErrInvalidChannel     = "Invalid channel selection."
	MsgLoopErrChannelFetchFail   = "Failed to fetch channel."
	MsgLoopErrOnlyCategories     = "Only **categories** are supported. Please select a category channel."
	MsgLoopSaveFail              = "Failed to save configuration: %v"
	MsgLoopConfiguredDisp        = "**Category Configured**\n> **%s**\n> Duration: ∞\n> Run `/loop start` to begin."
	MsgLoopErrInvalidDuration    = "Invalid duration: %v"
	MsgLoopErrNoChannels         = "No channels configured!"
	MsgLoopErrNoneStarted        = "No loops were started."
	MsgLoopStartedBatch          = "Started **%d** loop(s) for: **%s**"
	MsgLoopStarted               = "Started loop for: **%s**"
	MsgLoopStartFail             = "Failed to start **%s**: %v"
	MsgLoopNoRunning             = "No loops are currently running."
	MsgLoopStoppedBatch          = "Stopped **%d** loop(s)."
	MsgLoopStoppedDisp           = "Stopped the selected loop."
	MsgLoopErrStopFail           = "Could not find or stop the loop."
	MsgLoopErrGuildOnly          = "This command can only be used in a server."
	MsgLoopErrRetrieveFail       = "Failed to retrieve loop configurations."
	MsgLoopErrNoGuildConfigs     = "No loops are currently configured for this server."
	MsgLoopStatsHeader           = "**Current Loop Configurations**\n\n"
	MsgLoopStatsInterval         = "> • Interval: `%s`\n"
	MsgLoopStatsStatus           = "> • Status: %s\n"
	MsgLoopStatsThreads          = "> • Threads: `Enabled` (%d per channel)\n"
	MsgLoopStatsMessage          = "> • Message: `%s`\n"
	MsgLoopStatsAuthor           = "> • Author: `%s`\n"
	MsgLoopStatsAvatar           = "> • Avatar: [Link](<%s>)\n"
	MsgLoopStatsThreadMsg        = "> • Thread Message: `%s`\n"
	MsgLoopStatsVoteChan         = "> • Vote Channel: <#%s>\n"
	MsgLoopStatsVoteRole         = "> • Vote Role: <@&%s>\n"
	MsgLoopStatsVoteReaction     = "> • Vote Reaction: %s\n"
	MsgLoopStatsVoteThreshold    = "> • Vote Threshold: `%d%%`\n"
	MsgLoopStatsVoteMsg          = "> • Vote Message: `%s`\n"
	MsgLoopStatsQueue            = "> • Queue: `%s`\n"
	MsgLoopStatusStopped         = "🔴"
	MsgLoopStatusRunning         = "🟢"
	MsgLoopStatusRound           = " (Round %d)"
	MsgLoopStatusRoundBatch      = " (Round %d/%d)"
	MsgLoopStatusNextRun         = " (Next: %s)"
	MsgLoopStatusEnds            = " (Ends: %s)"
	MsgLoopStatusFinishing       = " (Finishing...)"
	MsgLoopChoiceStartAll        = "Start All Configured Loops"
	MsgLoopChoiceStart           = "Start Loop: %s %s%s (Duration: %s)"
	MsgLoopChoiceCategory        = "%s"
	MsgLoopChoiceEraseAll        = "Erase All Configured Loops"
	MsgLoopChoiceErase           = "Erase Loop: %s %s%s (Duration: %s)"
	MsgLoopChoiceStopAll         = "Stop All Running Loops"
	MsgLoopChoiceStop            = "Stop Loop: %s %s%s (Duration: %s)"
	MsgLoopSearchStartAll        = "start all configured loops"
	MsgLoopSearchEraseAll        = "erase all configured loops"
	MsgLoopSearchStopAll         = "stop all running loops"

	// --- Role Color System ---
	MsgRoleColorFailedToFetchConfigs = "Failed to fetch configs: %v"
	MsgRoleColorNextUpdate           = "Guild %s next update in %d minutes"
	MsgRoleColorUpdateFail           = "Failed to update role %s in guild %s: %v"
	MsgRoleColorUpdated              = "Updated role %s in guild %s to %s"
	MsgRoleColorErrGuildOnly         = "This command can only be used in a server."
	MsgRoleColorErrSetFail           = "Failed to set role color configuration."
	MsgRoleColorErrResetFail         = "Failed to reset role color configuration."
	MsgRoleColorErrNoRole            = "No role is configured for color rotation."
	MsgRoleColorErrRefreshFail       = "Failed to refresh role color."
	MsgRoleColorErrNoRoleStats       = "No random color role is currently configured for this server. Use `/rolecolor set` to start!"
	MsgRoleColorSetSuccess           = "Role <@&%s> will now have random colors!"
	MsgRoleColorResetSuccess         = "Role color rotation has been disabled."
	MsgRoleColorRefreshSuccess       = "Role color has been refreshed!"
	MsgRoleColorStatsHeader          = "**Random Role Color Status**"
	MsgRoleColorStatsContent         = "**Current Role:** <@&%s>\n" +
		"**Status:** `Active`\n\n" +
		"The bot will periodically change the color of this role to a random vibrant hue."

	// --- Session System ---
	MsgSessionRebootCommanded   = "Reboot commanded by user %s (%s)"
	MsgSessionShutdownCommanded = "Shutdown commanded by user %s (%s)"
	MsgSessionLogReadFail       = "Failed to read log file: %v"
	MsgSessionRebooting         = "**Rebooting...**"
	MsgSessionShuttingDown      = "**Shutting down...**"
	MsgSessionStatsLoading      = "Loading stats..."
	MsgSessionStatusUpdated     = "Status visibility updated!"
	MsgSessionStatusEnabled     = "Status rotation enabled!"
	MsgSessionStatusDisabled    = "Status rotation disabled!"
	MsgSessionConsoleDisabled   = "Logging to file is disabled."
	MsgSessionConsoleEmpty      = "No logs available."
	MsgSessionStatsSendFail     = "Failed to send initial stats: %v"
	MsgSessionConsoleBtnOldest  = "[Oldest]"
	MsgSessionConsoleBtnOlder   = "[Older]"
	MsgSessionConsoleBtnRefresh = "[Refresh]"
	MsgSessionConsoleBtnNewer   = "[Newer]"
	MsgSessionConsoleBtnLatest  = "[Latest]"

	MsgSessionRebootBuilding     = "**Building...**"
	MsgSessionRebootBuildFail    = "❌ **Build Failed**\n```\n%s\n```"
	MsgSessionRebootBuildSuccess = "✅ **Build Successful**"

	// --- Status & Activity ---
	MsgStatusUpdateFail        = "Update failed: %v"
	MsgStatusRotated           = "Status rotated to: \"%s\" (Next rotate in %v)"
	MsgStatusRotatedNoInterval = "Status rotated to: \"%s\""

	// --- Debug & Miscellaneous ---
	MsgDebugRoleColorUpdateFail  = "Failed to update guild config: %v"
	MsgDebugRoleColorResetFail   = "Failed to reset guild config: %v"
	MsgDebugRoleColorRefreshFail = "Failed to refresh role color: %v"
	MsgDebugStatusCmdFail        = "Failed to respond to status command: %v"
	MsgDebugTestErrorSendFail    = "Failed to send error preview: %v"
	MsgUndertextRespondError     = "Failed to respond to interaction: %v"
)
