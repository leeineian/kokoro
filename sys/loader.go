package sys

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

var commands = []*discordgo.ApplicationCommand{}
var commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var autocompleteHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
var onSessionReadyCallbacks []func(s *discordgo.Session)

// CreateSession creates and opens a Discord session with all required intents and handlers configured.
func CreateSession(token string, streamingURL string) (*discordgo.Session, error) {
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}

	s.AddHandler(InteractionHandler)

	s.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMembers |
		discordgo.IntentsGuildPresences |
		discordgo.IntentMessageContent |
		discordgo.IntentsGuildMessageReactions

	s.Identify.Presence = discordgo.GatewayStatusUpdate{
		Status: "online",
		Game: discordgo.Activity{
			Type: discordgo.ActivityTypeStreaming,
			URL:  streamingURL,
		},
	}

	if err := s.Open(); err != nil {
		return nil, err
	}

	return s, nil
}

func RegisterCommand(cmd *discordgo.ApplicationCommand, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	commands = append(commands, cmd)
	commandHandlers[cmd.Name] = handler
}

func RegisterAutocompleteHandler(cmdName string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	autocompleteHandlers[cmdName] = handler
}

func RegisterComponentHandler(customID string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	componentHandlers[customID] = handler
}

func OnSessionReady(cb func(s *discordgo.Session)) {
	onSessionReadyCallbacks = append(onSessionReadyCallbacks, cb)
}

func TriggerSessionReady(s *discordgo.Session) {
	for _, cb := range onSessionReadyCallbacks {
		cb(s)
	}
}

func RegisterCommands(s *discordgo.Session, guildID string) error {
	LogInfo(MsgLoaderRegistering)

	// If a guild ID is provided, register to guild and clear global simultaneously
	if guildID != "" {
		var errGuild, errGlobal error
		done := make(chan bool, 2)

		// 1. Register to Guild
		go func() {
			LogInfo(MsgLoaderGuildRegister, guildID)
			createdCommands, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, guildID, commands)
			if err != nil {
				errGuild = err
			} else {
				for _, cmd := range createdCommands {
					LogInfo(MsgLoaderCommandRegistered, cmd.Name)
				}
			}
			done <- true
		}()

		// 2. Clear Global
		go func() {
			LogInfo(MsgLoaderGlobalClear)
			_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", []*discordgo.ApplicationCommand{})
			if err != nil {
				errGlobal = err
			} else {
				LogInfo(MsgLoaderGlobalCleared)
			}
			done <- true
		}()

		// Wait for both
		<-done
		<-done

		if errGuild != nil {
			return fmt.Errorf("failed to register guild commands: %w", errGuild)
		}
		if errGlobal != nil {
			LogWarn(MsgLoaderGlobalClearFail, errGlobal)
		}

		return nil
	}

	// Otherwise, register commands globally
	LogInfo(MsgLoaderRegisteringGlobal)
	createdCommands, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", commands)
	// ... (the rest remains the same)
	if err != nil {
		return fmt.Errorf(MsgLoaderRegisterGlobalFail, err)
	}
	for _, cmd := range createdCommands {
		LogInfo(MsgLoaderGlobalRegistered, cmd.Name)
	}
	return nil
}

func InteractionHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			go h(s, i)
		}
	case discordgo.InteractionApplicationCommandAutocomplete:
		if h, ok := autocompleteHandlers[i.ApplicationCommandData().Name]; ok {
			go h(s, i)
		}
	case discordgo.InteractionMessageComponent:
		if h, ok := componentHandlers[i.MessageComponentData().CustomID]; ok {
			go h(s, i)
		}
	}
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
		daemon.logger("Starting...")
		daemon.starter()
	}
}
