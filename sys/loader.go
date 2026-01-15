package sys

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
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
var voiceStateUpdateHandlers []func(event *events.GuildVoiceStateUpdate)
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
		bot.WithEventListenerFunc(onApplicationCommandInteraction),
		bot.WithEventListenerFunc(onAutocompleteInteraction),
		bot.WithEventListenerFunc(onComponentInteraction),
		bot.WithEventListenerFunc(onVoiceStateUpdate),
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

func RegisterVoiceStateUpdateHandler(handler func(event *events.GuildVoiceStateUpdate)) {
	voiceStateUpdateHandlers = append(voiceStateUpdateHandlers, handler)
}

func OnClientReady(cb func(ctx context.Context, client *bot.Client)) {
	onClientReadyCallbacks = append(onClientReadyCallbacks, cb)
}

// --- Command Syncing Logic ---

func RegisterCommands(client *bot.Client, guildIDStr string) error {
	ctx := context.Background()
	_, _ = GetBotConfig(ctx, "last_reg_mode")
	lastGuildID, _ := GetBotConfig(ctx, "last_guild_id")

	isProduction := guildIDStr == ""
	currentMode := "guild"
	if isProduction {
		currentMode = "global"
	}

	LogInfo(MsgLoaderSyncCommands, strings.ToUpper(currentMode))

	// 1. Production Mode (Global)
	if isProduction {
		LogInfo(MsgLoaderProdStarting)
		createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
		if err != nil {
			return fmt.Errorf(MsgLoaderProdFail, err)
		}
		for _, cmd := range createdCommands {
			LogInfo(MsgLoaderProdRegistered, cmd.Name())
		}

		// Cleanup: If we transitioned from a Guild or have a stale Guild in history, check it
		targetClear := lastGuildID
		if targetClear != "" {
			if id, err := snowflake.Parse(targetClear); err == nil {
				// Only send DELETE if there is something to delete (saves rate limits)
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, id, false); err == nil && len(cmds) > 0 {
					LogInfo(MsgLoaderCleanup, targetClear)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, id, []discord.ApplicationCommandCreate{})
				}
			}
		}
	} else {
		// 2. Development Mode (Guild)
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

		// Reconcile Global: Ensure no global commands compete with our dev guild
		if cmds, err := client.Rest.GetGlobalCommands(client.ApplicationID, false); err == nil && len(cmds) > 0 {
			LogInfo(MsgLoaderDevGlobalClear)
			_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
			if err != nil {
				LogWarn(MsgLoaderDevGlobalClearFail, err)
			}
		}

		// Reconcile Transitions: Clear previous dev guild if it changed
		if lastGuildID != "" && lastGuildID != guildIDStr {
			if oldID, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, oldID, false); err == nil && len(cmds) > 0 {
					LogInfo(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, oldID, []discord.ApplicationCommandCreate{})
				}
			}
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

func onVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	for _, h := range voiceStateUpdateHandlers {
		safeGo(func() { h(event) })
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
				fmt.Printf("%s\n", debug.Stack())
			}
		}()
		f()
	}()
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}
