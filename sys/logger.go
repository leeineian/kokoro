package sys

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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

var (
	// Style definitions
	infoColor          = color.New(color.FgHiBlack)
	warnColor          = color.New(color.FgHiYellow)
	errorColor         = color.New(color.FgHiRed)
	fatalColor         = color.New(color.FgHiRed, color.Bold)
	databaseColor      = color.New(color.FgHiBlack)
	reminderColor      = color.New(color.FgHiMagenta)
	statusRotatorColor = color.New(color.FgHiMagenta)
	roleRotatorColor   = color.New(color.FgHiMagenta)
	loopManagerColor   = color.New(color.FgHiMagenta)
	catColor           = color.New(color.FgHiMagenta)
	undertextColor     = color.New(color.FgHiMagenta)

	IsSilent  = false
	LogToFile = false

	errorMapCache map[string]string
	errorMapOnce  sync.Once

	// Global default logger
	Logger *slog.Logger

	// Log file handling
	logFile *os.File
	logMu   sync.Mutex
)

func init() {
	// Initialize with a default handler immediately (Stdout only)
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

	// Clean up previous file if it exists (e.g. during reload)
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}

	var writer io.Writer = os.Stdout
	var err error

	// Open log file if requested
	if LogToFile {
		// Determine log file name from executable name
		exePath, exeErr := os.Executable()
		logName := "minder.log" // Fallback
		if exeErr == nil {
			logName = filepath.Base(exePath) + ".log"
		}

		// Open log file
		logFile, err = os.OpenFile(logName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open %s: %v\n", logName, err)
		} else {
			writer = io.MultiWriter(os.Stdout, logFile)
		}
	}

	// Force colors to be enabled even if writing to a file/pipe avoids detection
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

// --- Log Functions (Signatures preserved for compatibility) ---

func LogInfo(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...))
}

func LogWarn(format string, v ...interface{}) {
	slog.Warn(fmt.Sprintf(format, v...))
}

func LogError(format string, v ...interface{}) {
	slog.Error(fmt.Sprintf(format, v...))
}

func LogFatal(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	slog.Log(context.Background(), slog.LevelError+4, msg) // Custom Fatal level
	os.Exit(1)
}

func LogDatabase(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "database"))
}

func LogReminder(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "reminder"))
}

func LogStatusRotator(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "status_rotator"))
}

func ColorizeHex(colorInt int) string {
	hex := fmt.Sprintf("#%06X", colorInt)
	r := (colorInt >> 16) & 0xFF
	g := (colorInt >> 8) & 0xFF
	b := colorInt & 0xFF
	// 24-bit ANSI color: \x1b[38;2;R;G;Bm
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm⬤ %s\x1b[0m", r, g, b, hex)
}

func LogRoleColorRotator(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "role_color"))
}

func LogLoopManager(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "loop_manager"))
}

func LogCustom(tag string, tagColor *color.Color, format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", tag))
}

func LogCat(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "cat"))
}

func LogUndertext(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...), slog.String("component", "undertext"))
}

func LogDebug(format string, v ...interface{}) {
	slog.Debug(fmt.Sprintf(format, v...))
}

// --- Custom Slog Handler ---

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

	timeStr := time.Now().Format("15:04:05")
	var levelStr string
	var levelColor *color.Color

	switch {
	case r.Level >= slog.LevelError+4: // Fatal
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

	// Extract component if present
	component := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "component" {
			component = strings.ToUpper(a.Value.String())
			return false
		}
		return true
	})

	// Output: 15:04:05 [INFO] [COMPONENT] Message
	// Timestamp is always printed in default color.
	fmt.Fprintf(h.w, "%s", timeStr)

	if component != "" {
		// Component-specific logs: Level tag (if not INFO) is isolated, Message bleeds component color
		if levelStr != "INFO" {
			fmt.Fprintf(h.w, " %s", levelColor.Sprintf("[%s]", levelStr))
		}
		compColor := getComponentColor(component)
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(compColor, fmt.Sprintf("[%s] %s", component, r.Message)))
	} else {
		// General logs: Level tag color bleeds into the message
		fmt.Fprintf(h.w, " %s\n", colorizeWithResets(levelColor, fmt.Sprintf("[%s] %s", levelStr, r.Message)))
	}

	return nil
}

func (h *BotLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *BotLogHandler) WithGroup(name string) slog.Handler       { return h }

func getComponentColor(name string) *color.Color {
	switch name {
	case "DATABASE":
		return databaseColor
	case "REMINDER":
		return reminderColor
	case "STATUS_ROTATOR":
		return statusRotatorColor
	case "ROLE_COLOR":
		return roleRotatorColor
	case "LOOP_MANAGER":
		return loopManagerColor
	case "CAT":
		return catColor
	case "UNDERTEXT":
		return undertextColor
	default:
		return color.New(color.FgCyan)
	}
}

// colorizeWithResets ensures that if the text contains ANSI reset codes,
// the starting color of the Color object is re-applied after each reset.
// This allows nested coloring (like hex codes) to work within component logs.
func colorizeWithResets(c *color.Color, text string) string {
	if !strings.Contains(text, "\x1b[0m") {
		return c.Sprint(text)
	}

	// Extract starting ANSI sequence
	marker := "@@@MSG@@@"
	wrapped := c.Sprint(marker)
	idx := strings.Index(wrapped, marker)
	if idx <= 0 {
		return text // No color applied or something went wrong
	}
	startSeq := wrapped[:idx]

	// Re-apply start sequences after each reset to maintain the outer color
	// Also re-apply it at the beginning of the string to be safe (Sprint handles it)
	modifiedText := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+startSeq)
	return c.Sprint(modifiedText)
}

// @src
const (
	// Configuration
	MsgConfigFailedToLoad = "Failed to load config: %v"
	MsgConfigMissingToken = "DISCORD_TOKEN is not set in .env file"

	// Data layer
	MsgDatabaseInitSuccess = "Database initialized successfully"
	MsgDatabaseTableError  = "Failed to create table: %w"
	MsgDatabasePragmaError = "Failed to set pragma %s: %w"

	// Command Registry
	MsgLoaderRegistering        = "Registering commands..."
	MsgLoaderGuildRegister      = "Registering commands to guild: %s"
	MsgLoaderGlobalClear        = "Clearing global commands..."
	MsgLoaderGlobalCleared      = "Global commands cleared."
	MsgLoaderGlobalClearFail    = "Failed to clear global commands: %v"
	MsgLoaderCommandRegistered  = "Registered guild command: %s"
	MsgGenericError             = "%v"
	MsgLoaderRegisteringGlobal  = "Registering commands globally..."
	MsgLoaderRegisterGlobalFail = "[ERROR] Failed to register global commands: %w"
	MsgLoaderGlobalRegistered   = "Registered global command: %s"

	// Bot Lifecycle
	MsgBotStarting      = "Starting %s..."
	MsgBotReady         = "%s is ready! (ID: %s) (PID: %d)"
	MsgBotShutdown      = "Shutting down %s..."
	MsgBotKillingOld    = "Killing running instance... (PID: %d)"
	MsgBotKillFail      = "Failed to kill old instance: %v"
	MsgBotOldTerminated = "Old instance terminated."
	MsgBotPIDWriteFail  = "Failed to write PID file: %v"
	MsgBotRegisterFail  = "Command registration failed: %v"
)

// @cat
const (
	// System logs
	MsgCatFailedToFetchFact         = "Failed to fetch cat fact: %v"
	MsgCatFactAPIStatusError        = "Cat fact API returned status %d"
	MsgCatFailedToDecodeFact        = "Failed to decode cat fact: %v"
	MsgCatFailedToFetchImage        = "Failed to fetch cat image: %v"
	MsgCatImageAPIStatusError       = "Cat image API returned status %d"
	MsgCatFailedToDecodeImage       = "Failed to decode cat image response: %v"
	MsgCatImageAPIEmptyArray        = "Cat image API returned empty array"
	MsgCatCannotSendErrorResponse   = "Cannot send error response: nil session or interaction"
	MsgCatFailedToSendErrorResponse = "Failed to send error response: %v"

	// User-facing messages
	ErrCatFailedToFetchFact       = "Failed to fetch cat fact"
	ErrCatFactServiceUnavailable  = "Cat fact service is unavailable"
	ErrCatFailedToDecodeFact      = "Failed to decode cat fact"
	ErrCatFailedToFetchImage      = "Failed to fetch cat image"
	ErrCatImageServiceUnavailable = "Cat image service is unavailable"
	ErrCatFailedToDecodeImage     = "Failed to decode cat image"
	ErrCatNoImagesAvailable       = "No cat images available"
)

// @reminder
const (
	// System logs
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

	// User-facing messages
	ErrReminderParseFailed    = "Failed to parse the date/time. Try formats like 'tomorrow', 'in 2 hours', 'next friday at 3pm'."
	ErrReminderPastTime       = "The reminder time must be in the future!"
	ErrReminderSaveFailed     = "Failed to save reminder. Please try again."
	ErrReminderFetchFailed    = "Failed to fetch reminders."
	ErrReminderDismissFailed  = "Failed to dismiss reminder."
	ErrReminderDismissAllFail = "Failed to dismiss all reminders."
	MsgReminderNoActive       = "You have no active reminders."
	MsgReminderDismissed      = "Reminder dismissed!"
)

// @rolecolor
const (
	MsgRoleColorFailedToFetchConfigs = "Failed to fetch configs: %v"
	MsgRoleColorNextUpdate           = "Guild %s next update in %d minutes"
	MsgRoleColorUpdateFail           = "Failed to update role %s in guild %s: %v"
	MsgRoleColorUpdated              = "Updated role %s in guild %s to %s"
)

// @status
const (
	MsgStatusUpdateFail        = "Update failed: %v"
	MsgStatusRotated           = "Status rotated to: \"%s\" (Next rotate in %v)"
	MsgStatusRotatedNoInterval = "Status rotated to: \"%s\""
)

// @loop
const (
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
	MsgLoopRandomStatus          = "[%s] Random: %d rounds, next delay: %s"
	MsgLoopRateLimited           = "[%s] Rate limited. Retrying in %v (Attempt %d/3)"
	MsgLoopSendFail              = "Failed to send to %s: %v"
	MsgLoopRenameFail            = "Failed to rename channel: %v"
	MsgLoopStopped               = "Stopped loop for: %s"
	MsgLoopConfigured            = "Configured channel: %s"
)

// @debug
const (
	MsgDebugEchoFail            = "Failed to respond to echo: %v"
	MsgDebugRoleColorUpdateFail = "Failed to update guild config: %v"
	MsgDebugRoleColorResetFail  = "Failed to reset guild config: %v"
	MsgDebugStatusCmdFail       = "Failed to respond to status command: %v"
	MsgDebugTestErrorSendFail   = "Failed to send error preview: %v"
)

// @undertext
const (
	// System logs
	MsgUndertextRespondError = "Failed to respond to interaction: %v"

	// User-facing messages
)

// GetUserErrors dynamically parses the source file to discover all 'Err' and 'Msg' constants.
func GetUserErrors() map[string]string {
	errorMapOnce.Do(func() {
		errorMapCache = make(map[string]string)

		// Get the current file path
		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			return
		}

		fset := token.NewFileSet()
		// Parse the current file to discover constants
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
							// Only include Err... or Msg... constants (heuristics for user-facing)
							if strings.HasPrefix(constName, "Err") || strings.HasPrefix(constName, "Msg") {
								if len(valueSpec.Values) > i {
									if basicLit, isBasicLit := valueSpec.Values[i].(*ast.BasicLit); isBasicLit && basicLit.Kind == token.STRING {
										constValue := strings.Trim(basicLit.Value, `"`)
										// Filter out constants with formatting placeholders (system logs)
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
