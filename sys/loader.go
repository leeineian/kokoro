package sys

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

var AppContext context.Context
var RestartRequested bool

func SetAppContext(ctx context.Context) {
	AppContext = ctx
}

var commands = []discord.ApplicationCommandCreate{}
var commandHandlers = map[string]func(event *events.ApplicationCommandInteractionCreate){}
var autocompleteHandlers = map[string]func(event *events.AutocompleteInteractionCreate){}
var componentHandlers = map[string]func(event *events.ComponentInteractionCreate){}
var onClientReadyCallbacks []func(ctx context.Context, client *bot.Client)

// HttpClient is a shared thread-safe client for all external API calls.
var HttpClient = &http.Client{
	Timeout: 10 * time.Second,
}

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

func OnClientReady(cb func(ctx context.Context, client *bot.Client)) {
	onClientReadyCallbacks = append(onClientReadyCallbacks, cb)
}

func TriggerClientReady(ctx context.Context, client *bot.Client) {
	for _, cb := range onClientReadyCallbacks {
		cb(ctx, client)
	}
}

func RegisterCommands(client *bot.Client, guildIDStr string) error {
	LogInfo(MsgLoaderRegistering)

	// If a guild ID is provided, register to guild and then clear global sequentially
	if guildIDStr != "" {
		guildID, err := snowflake.Parse(guildIDStr)
		if err != nil {
			return err
		}

		// 1. Register to Guild
		LogInfo(MsgLoaderGuildRegister, guildIDStr)
		createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
		if err != nil {
			// If we hit a rate limit here, we should still try to proceed or return
			LogWarn("Guild command registration hit a bottleneck: %v", err)
		} else {
			for _, cmd := range createdCommands {
				LogInfo(MsgLoaderCommandRegistered, cmd.Name())
			}
		}

		// Brief pause to satisfy Discord rate limits during rapid restarts
		time.Sleep(1 * time.Second)

		// 2. Clear Global (Optional, only if needed or first time)
		LogInfo(MsgLoaderGlobalClear)
		_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
		if err != nil {
			// Don't let global clear failure (often rate limits) stop the bot
			LogWarn("Global command clear skipped or rate-limited: %v", err)
		} else {
			LogInfo(MsgLoaderGlobalCleared)
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
	}
}

// safeGo runs a function in a new goroutine with panic recovery
func safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				LogError("Panic recovered in handler: %v", r)
			}
		}()
		f()
	}()
}

func onReady(event *events.Ready) {
	client := event.Client()
	botUser := event.User

	// 1. Background Daemons
	TriggerClientReady(AppContext, client)
	StartDaemons(AppContext)

	// 2. Final Status
	LogInfo(MsgBotOnline, botUser.Username, botUser.ID.String(), os.Getpid())
}

// Daemon registry
type daemonEntry struct {
	starter func(ctx context.Context)
	logger  func(format string, v ...interface{})
}

var registeredDaemons []daemonEntry

// RegisterDaemon registers a background daemon with a logger and start function
func RegisterDaemon(logger func(format string, v ...interface{}), starter func(ctx context.Context)) {
	registeredDaemons = append(registeredDaemons, daemonEntry{starter: starter, logger: logger})
}

// StartDaemons starts all registered daemons with their individual colored logging
func StartDaemons(ctx context.Context) {
	for _, daemon := range registeredDaemons {
		daemon.logger("Starting...")
		daemon.starter(ctx)
	}
}
