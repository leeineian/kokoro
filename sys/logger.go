package sys

import (
	"fmt"
	"log"
	"os"

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
	debugColor         = color.New(color.FgHiGreen)
	IsSilent           = false
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

// LogDebug logs a debug message in hi-green
func LogDebug(format string, v ...interface{}) {
	if IsSilent {
		return
	}
	msg := fmt.Sprintf(format, v...)
	log.Println(debugColor.Sprintf("[DEBUG] %s", msg))
}

// Daemon registry
type daemonEntry struct {
	starter func()
	logger  func(format string, v ...interface{})
}

var registeredDaemons []daemonEntry

// RegisterDaemon registers a background daemon with a logger and start function
func RegisterDaemon(logger func(format string, v ...interface{}), starter func()) {
	registeredDaemons = append(registeredDaemons, daemonEntry{starter: starter, logger: logger})
}

// StartDaemons starts all registered daemons with their individual colored logging
func StartDaemons() {
	for _, daemon := range registeredDaemons {
		go func(d daemonEntry) {
			d.logger("Starting...")
			d.starter()
		}(daemon)
	}
}

// @config
const (
	// Error messages
	MsgConfigFailedToLoad = "Failed to load config: %v"
	MsgConfigMissingToken = "DISCORD_TOKEN is not set in .env file"
)

// @cat
const (
	// Fact command
	MsgCatFailedToFetchFact  = "Failed to fetch cat fact: %v"
	MsgCatFactAPIStatusError = "Cat fact API returned status %d"
	MsgCatFailedToDecodeFact = "Failed to decode cat fact: %v"

	// Image command
	MsgCatFailedToFetchImage  = "Failed to fetch cat image: %v"
	MsgCatImageAPIStatusError = "Cat image API returned status %d"
	MsgCatFailedToDecodeImage = "Failed to decode cat image response: %v"
	MsgCatImageAPIEmptyArray  = "Cat image API returned empty array"

	// Response errors
	MsgCatErrorEditingResponse      = "Error editing interaction response: %v"
	MsgCatCannotSendErrorFollowup   = "Cannot send error followup: nil session or interaction"
	MsgCatFailedToSendErrorFollowup = "Failed to send error followup message: %v"
	MsgCatCannotSendErrorResponse   = "Cannot send error response: nil session or interaction"
	MsgCatFailedToSendErrorResponse = "Failed to send error response: %v"

	// User-facing error messages
	ErrCatFailedToFetchFact       = "Failed to fetch cat fact"
	ErrCatFactServiceUnavailable  = "Cat fact service is unavailable"
	ErrCatFailedToDecodeFact      = "Failed to decode cat fact"
	ErrCatFailedToFetchImage      = "Failed to fetch cat image"
	ErrCatImageServiceUnavailable = "Cat image service is unavailable"
	ErrCatFailedToDecodeImage     = "Failed to decode cat image"
	ErrCatNoImagesAvailable       = "No cat images available"
)
