package sys

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/fatih/color"
)

var (
	infoColor          = color.New(color.FgHiBlack)
	warnColor          = color.New(color.FgHiYellow)
	errorColor         = color.New(color.FgHiRed)
	fatalColor         = color.New(color.FgHiRed)
	databaseColor      = color.New(color.FgHiBlack)
	reminderColor      = color.New(color.FgMagenta)
	statusRotatorColor = color.New(color.FgGreen)
	roleRotatorColor   = color.New(color.FgYellow)
	loopRotatorColor   = color.New(color.FgBlue)
	catColor           = color.New(color.FgCyan)
	undertextColor     = color.New(color.FgRed)
	eightballColor     = color.New(color.FgHiBlue)
	debugColor         = color.New(color.FgHiGreen)
	IsSilent           = false

	errorMapCache map[string]string
	errorMapOnce  sync.Once
)

func SetSilentMode(silent bool) {
	IsSilent = silent
}

// LogInfo logs an informational message in green
func LogInfo(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(infoColor.Sprintf("[INFO] %s", msg))
}

// LogWarn logs a warning message in yellow
func LogWarn(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(warnColor.Sprintf("[WARN] %s", msg))
}

// LogError logs an error message in red
func LogError(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(errorColor.Sprintf("[ERROR] %s", msg))
}

// LogFatal logs a fatal error message in bold red and exits
func LogFatal(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	log.Println(fatalColor.Sprintf("[FATAL] %s", msg))
	os.Exit(1)
}

// LogDatabase logs a database-related message in cyan
func LogDatabase(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(databaseColor.Sprintf("[DATABASE] %s", msg))
}

// LogReminder logs a reminder scheduler message in magenta
func LogReminder(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(reminderColor.Sprintf("[REMINDER SCHEDULER] %s", msg))
}

// LogStatusRotator logs a status rotator message in hi-green
func LogStatusRotator(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(statusRotatorColor.Sprintf("[STATUS ROTATOR] %s", msg))
}

// LogRoleColorRotator logs a role color rotator message in hi-yellow
func LogRoleColorRotator(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(roleRotatorColor.Sprintf("[ROLE COLOR ROTATOR] %s", msg))
}

// LogLoopRotator logs a loop rotator message in hi-magenta
func LogLoopRotator(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(loopRotatorColor.Sprintf("[LOOP ROTATOR] %s", msg))
}

// LogCustom logs a message with a custom tag and color
func LogCustom(tag string, tagColor *color.Color, format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(tagColor.Sprintf("[%s] %s", tag, msg))
}

// LogCat logs a cat command message in hi-cyan
func LogCat(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(catColor.Sprintf("[CAT] %s", msg))
}

// LogUndertext logs an undertext command message in red
func LogUndertext(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(undertextColor.Sprintf("[UNDERTEXT] %s", msg))
}

// LogEightball logs an eightball command message in hi-blue
func LogEightball(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(eightballColor.Sprintf("[8BALL] %s", msg))
}

// LogDebug logs a debug message in hi-green
func LogDebug(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(debugColor.Sprintf("[DEBUG] %s", msg))
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
	MsgBotOnline        = "%s is online! (ID: %s) (PID: %d)"
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
	MsgCatErrorEditingResponse      = "Error editing interaction response: %v"
	MsgCatCannotSendErrorFollowup   = "Cannot send error followup: nil session or interaction"
	MsgCatFailedToSendErrorFollowup = "Failed to send error followup message: %v"
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
	MsgReminderFailedToScan          = "Failed to scan reminder: %v"
	MsgReminderFailedToCreateDM      = "Failed to create DM channel for user %s: %v"
	MsgReminderFailedToSend          = "Failed to send reminder %d: %v"
	MsgReminderFailedToDelete        = "Failed to delete sent reminder %d: %v"
	MsgReminderFailedToDeleteGeneral = "Failed to delete reminder: %v"
	MsgReminderSentAndDeleted        = "Sent and deleted reminder %d for user %s"
	MsgReminderFailedToSave          = "Failed to save reminder: %v"
	MsgReminderFailedToDeleteAll     = "Failed to delete all reminders: %v"
	MsgReminderFailedToQuery         = "Failed to query reminders: %v"
	MsgReminderAutocompleteFailed    = "Failed to query reminders for autocomplete: %v"
	MsgReminderEditResponseError     = "Error editing interaction response: %v"
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
	MsgStatusRotated           = "Status rotated to: %s (Next rotate in %v)"
	MsgStatusRotatedNoInterval = "Status rotated to: %s"
)

// @loop
const (
	MsgLoopFailedToLoadConfigs   = "Failed to load configs: %v"
	MsgLoopLoadingChannels       = "Loading %d configured channels from DB..."
	MsgLoopLoadedChannels        = "Loaded configuration for %d channels (Lazy)."
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
	MsgUndertextEditResponseError = "Error editing interaction response: %v"
	MsgUndertextRespondError      = "Failed to respond to interaction: %v"

	// User-facing messages
	ErrUndertextGenerateFailed = "Failed to generate text box."
)

// @eightball
const (
	// System logs
	MsgEightballFailedToFetchFortune  = "Failed to fetch fortune: %v"
	MsgEightballFortuneAPIStatusError = "Eightball API returned status %d"
	MsgEightballFailedToDecodeFortune = "Failed to decode fortune: %v"
	MsgEightballCannotSendError       = "Cannot send error response: nil session or interaction"
	MsgEightballFailedToSendError     = "Failed to send error response: %v"

	// User-facing messages
	ErrEightballFailedToFetchFortune = "Failed to fetch fortune"
	ErrEightballServiceUnavailable   = "Eightball service is unavailable"
	ErrEightballFailedToDecode       = "Failed to decode fortune"
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
