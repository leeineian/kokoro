package sys

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/snowflake/v2"
)

// --- Global State & Setup ---

var AppContext context.Context
var RestartRequested bool

var commands = []discord.ApplicationCommandCreate{}
var commandHandlers = map[string]func(event *events.ApplicationCommandInteractionCreate){}
var autocompleteHandlers = map[string]func(event *events.AutocompleteInteractionCreate){}
var componentHandlers = map[string]func(event *events.ComponentInteractionCreate){}
var onClientReadyCallbacks []func(ctx context.Context, client *bot.Client)

// HttpClient is a shared thread-safe client for all external API calls.
var HttpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func SetAppContext(ctx context.Context) {
	AppContext = ctx
}

// --- Bot Initialization ---

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
				gateway.WithPlayingActivity("Loading..."),
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
		bot.WithLogger(slog.Default()),
	)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// GetBotUsername fetches the bot's username using the provided token
func GetBotUsername(token string) (string, error) {
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := HttpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf(MsgBotAPIStatusError, resp.StatusCode)
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}
	return user.Username, nil
}

// --- Command & Handler Registration ---

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

// --- Command Syncing Logic ---

func RegisterCommands(client *bot.Client, guildIDStr string) error {
	ctx := context.Background()
	lastMode, _ := GetBotConfig(ctx, "last_reg_mode")
	lastGuildID, _ := GetBotConfig(ctx, "last_guild_id")

	isProduction := guildIDStr == ""
	currentMode := "guild"
	if isProduction {
		currentMode = "global"
	}

	LogInfo(MsgLoaderSyncCommands, strings.ToUpper(currentMode))

	// --- MANUAL CLEANUP OVERRIDE ---
	if manualID := os.Getenv("MANUAL_CLEANUP_GUILD"); manualID != "" {
		if id, err := snowflake.Parse(manualID); err == nil {
			LogInfo("🧹 [MANUAL CLEANUP] Clearing commands from guild: %s", manualID)
			_, _ = client.Rest.SetGuildCommands(client.ApplicationID, id, []discord.ApplicationCommandCreate{})
		}
	}

	// 1. Handle Transition: Mode changed (e.g. Guild -> Global or Global -> Guild)
	if lastMode != "" && lastMode != currentMode {
		LogInfo(MsgLoaderTransition, strings.ToUpper(lastMode), strings.ToUpper(currentMode))

		if lastMode == "guild" && lastGuildID != "" {
			// Clear old guild commands
			oldID, err := snowflake.Parse(lastGuildID)
			if err == nil {
				LogInfo(MsgLoaderCleanup, lastGuildID)
				_, _ = client.Rest.SetGuildCommands(client.ApplicationID, oldID, []discord.ApplicationCommandCreate{})
			}
		}
	}

	// 2. Deployment
	if !isProduction {
		// --- DEVELOPMENT MODE (GUILD) ---
		guildID, err := snowflake.Parse(guildIDStr)
		if err != nil {
			return fmt.Errorf("invalid GUILD_ID: %w", err)
		}

		LogInfo(MsgLoaderDevStarting, guildIDStr)
		createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
		if err != nil {
			LogWarn(MsgLoaderDevFail, err)
		} else {
			for _, cmd := range createdCommands {
				LogInfo(MsgLoaderDevRegistered, cmd.Name())
			}
		}

		if lastMode == "" || lastMode == "global" {
			LogInfo(MsgLoaderDevGlobalClear)
			_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
			if err != nil {
				LogWarn(MsgLoaderDevGlobalClearFail, err)
			}
		}
	} else {
		// --- PRODUCTION MODE (GLOBAL) ---
		LogInfo(MsgLoaderProdStarting)
		createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
		if err != nil {
			return fmt.Errorf(MsgLoaderProdFail, err)
		}
		for _, cmd := range createdCommands {
			LogInfo(MsgLoaderProdRegistered, cmd.Name())
		}
	}

	// 3. Update State
	_ = SetBotConfig(ctx, "last_reg_mode", currentMode)
	_ = SetBotConfig(ctx, "last_guild_id", guildIDStr)

	return nil
}

// --- Event Handlers ---

func onReady(event *events.Ready) {
	client := event.Client()
	botUser := event.User

	// 1. Final Status
	LogInfo(MsgBotReady, botUser.Username, botUser.ID.String(), os.Getpid())

	// 2. Background Daemons
	TriggerClientReady(AppContext, client)
	StartDaemons(AppContext)
}

func TriggerClientReady(ctx context.Context, client *bot.Client) {
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

// --- Daemon System ---

type daemonEntry struct {
	starter func(ctx context.Context) (bool, func())
	logger  func(format string, v ...interface{})
}

var registeredDaemons []daemonEntry

// RegisterDaemon registers a background daemon with a logger and start function
func RegisterDaemon(logger func(format string, v ...interface{}), starter func(ctx context.Context) (bool, func())) {
	registeredDaemons = append(registeredDaemons, daemonEntry{starter: starter, logger: logger})
}

// StartDaemons starts all registered daemons with their individual colored logging
func StartDaemons(ctx context.Context) {
	for _, daemon := range registeredDaemons {
		// Launch each daemon in parallel to avoid blocking on DB reads or setup
		go func(d daemonEntry) {
			if ok, run := d.starter(ctx); ok && run != nil {
				d.logger(MsgDaemonStarting)
				run()
			}
		}(daemon)
	}
}

// --- Utilities ---

// safeGo runs a function in a new goroutine with panic recovery
func safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				LogError(MsgLoaderPanicRecovered, r)
			}
		}()
		f()
	}()
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}
