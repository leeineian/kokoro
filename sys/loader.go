package sys

import (
	"context"
	"log/slog"
	"os"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

var commands = []discord.ApplicationCommandCreate{}
var commandHandlers = map[string]func(event *events.ApplicationCommandInteractionCreate){}
var autocompleteHandlers = map[string]func(event *events.AutocompleteInteractionCreate){}
var componentHandlers = map[string]func(event *events.ComponentInteractionCreate){}
var onClientReadyCallbacks []func(client *bot.Client)

// CreateClient creates and configures a disgo client
func CreateClient(ctx context.Context, cfg *Config) (*bot.Client, error) {
	client, err := disgo.New(cfg.Token,
		bot.WithGatewayConfigOpts(
			gateway.WithIntents(
				gateway.IntentGuilds,
				gateway.IntentGuildMessages,
				gateway.IntentGuildMembers,
				gateway.IntentGuildPresences,
				gateway.IntentMessageContent,
				gateway.IntentGuildMessageReactions,
			),
			gateway.WithPresenceOpts(
				gateway.WithPlayingActivity("Starting..."),
				gateway.WithOnlineStatus(discord.OnlineStatusOnline),
			),
		),
		bot.WithCacheConfigOpts(
			cache.WithCaches(cache.FlagGuilds, cache.FlagMembers, cache.FlagRoles, cache.FlagChannels),
		),
		bot.WithEventListenerFunc(onApplicationCommandInteraction),
		bot.WithEventListenerFunc(onAutocompleteInteraction),
		bot.WithEventListenerFunc(onComponentInteraction),
		bot.WithEventListenerFunc(onReady),
		// Disable slog output if silent
		bot.WithLogger(slog.Default()),
	)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func RegisterCommand(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate)) {
	commands = append(commands, cmd)
	// Extract name based on command type
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

func OnClientReady(cb func(client *bot.Client)) {
	onClientReadyCallbacks = append(onClientReadyCallbacks, cb)
}

func TriggerClientReady(client *bot.Client) {
	for _, cb := range onClientReadyCallbacks {
		cb(client)
	}
}

func RegisterCommands(client *bot.Client, guildIDStr string) error {
	LogInfo(MsgLoaderRegistering)

	// If a guild ID is provided, register to guild and clear global simultaneously
	if guildIDStr != "" {
		guildID, err := snowflake.Parse(guildIDStr)
		if err != nil {
			return err
		}

		var errGuild, errGlobal error
		done := make(chan bool, 2)

		// 1. Register to Guild
		go func() {
			LogInfo(MsgLoaderGuildRegister, guildIDStr)
			createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
			if err != nil {
				errGuild = err
			} else {
				for _, cmd := range createdCommands {
					LogInfo(MsgLoaderCommandRegistered, cmd.Name())
				}
			}
			done <- true
		}()

		// 2. Clear Global
		go func() {
			LogInfo(MsgLoaderGlobalClear)
			_, err := client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
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
			return errGuild
		}
		if errGlobal != nil {
			LogWarn(MsgLoaderGlobalClearFail, errGlobal)
		}

		return nil
	}

	// Otherwise, register commands globally
	LogInfo(MsgLoaderRegisteringGlobal)
	createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
	if err != nil {
		return err
	}
	for _, cmd := range createdCommands {
		LogInfo(MsgLoaderGlobalRegistered, cmd.Name())
	}
	return nil
}

func onApplicationCommandInteraction(event *events.ApplicationCommandInteractionCreate) {
	data := event.Data
	if h, ok := commandHandlers[data.CommandName()]; ok {
		go h(event)
	}
}

func onAutocompleteInteraction(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	if h, ok := autocompleteHandlers[data.CommandName]; ok {
		go h(event)
	}
}

func onComponentInteraction(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	if h, ok := componentHandlers[customID]; ok {
		go h(event)
	}
}

func onReady(event *events.Ready) {
	client := event.Client()
	botUser := event.User

	// 1. Background Daemons
	TriggerClientReady(client)
	StartDaemons()

	// 2. Final Status
	LogInfo(MsgBotOnline, botUser.Username, botUser.ID.String(), os.Getpid())
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
