package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
)

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

// --- Global State & Setup ---

var AppContext context.Context
var RestartRequested bool
var daemonsOnce sync.Once
var StartupTime = time.Now()

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
func CreateClient(ctx context.Context, cfg *Config, _ snowflake.ID) (*bot.Client, error) {
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
		bot.WithRestClientConfigOpts(
			rest.WithHTTPClient(&http.Client{
				Timeout: 60 * time.Second,
				Transport: &http.Transport{
					MaxIdleConns:        1000,
					MaxIdleConnsPerHost: 500,
					IdleConnTimeout:     90 * time.Second,
				},
			}),
		),
	)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// GetBotUsername fetches the bot's username and ID using the provided token, with caching
func GetBotUsername(ctx context.Context, token string) (string, snowflake.ID, error) {
	// 1. Check Cache First
	cachedName, _ := GetBotConfig(ctx, "cached_bot_name")
	cachedIDStr, _ := GetBotConfig(ctx, "cached_bot_id")

	var cachedID snowflake.ID
	if cachedIDStr != "" {
		if id, err := snowflake.Parse(cachedIDStr); err == nil {
			cachedID = id
		}
	}

	if cachedName != "" && cachedID != 0 {
		return cachedName, cachedID, nil
	}

	// 2. API Call
	req, err := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := HttpClient.Do(req)
	if err != nil {
		if cachedName != "" {
			return cachedName, cachedID, nil
		}
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == 429 {
			if cachedName != "" {
				return cachedName, cachedID, nil
			}
			return GetProjectName(), 0, nil
		}
		if cachedName != "" {
			return cachedName, cachedID, nil
		}
		return "", 0, fmt.Errorf(MsgBotAPIStatusError, resp.StatusCode)
	}

	var user struct {
		ID       snowflake.ID `json:"id"`
		Username string       `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		if cachedName != "" {
			return cachedName, cachedID, nil
		}
		return "", 0, err
	}

	// 3. Update Cache
	_ = SetBotConfig(ctx, "cached_bot_name", user.Username)
	_ = SetBotConfig(ctx, "cached_bot_id", user.ID.String())

	return user.Username, user.ID, nil
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

// calculateCommandHash generates a SHA256 hash of the commands slice
func calculateCommandHash(cmds []discord.ApplicationCommandCreate) string {
	data, err := json.Marshal(cmds)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func RegisterCommands(client *bot.Client, guildIDStr string, forceScan bool) error {
	ctx := context.Background()
	_, _ = GetBotConfig(ctx, "last_reg_mode")
	lastGuildID, _ := GetBotConfig(ctx, "last_guild_id")

	isProduction := guildIDStr == ""
	currentMode := "guild"
	if isProduction {
		currentMode = "global"
	}

	LogInfo(MsgLoaderSyncCommands, strings.ToUpper(currentMode))

	currentHash := calculateCommandHash(commands)
	lastHash, _ := GetBotConfig(ctx, "last_cmd_hash")
	lastMode, _ := GetBotConfig(ctx, "last_reg_mode")

	shouldRegister := true
	if currentHash != "" && currentHash == lastHash && currentMode == lastMode && !forceScan {
		shouldRegister = false
		LogInfo("[LOADER] Commands are up to date. (Hash: %s)", currentHash[:8])
	}

	// 1. Production Mode (Global)
	if isProduction {
		if shouldRegister {
			LogInfo(MsgLoaderProdStarting)
			createdCommands, err := client.Rest.SetGlobalCommands(client.ApplicationID, commands)
			if err != nil {
				return fmt.Errorf(MsgLoaderProdFail, err)
			}
			for _, cmd := range createdCommands {
				LogInfo(MsgLoaderProdRegistered, cmd.Name())
			}
		}

		shouldScan := forceScan || (lastMode != currentMode)
		if shouldScan {
			LogInfo(MsgLoaderScanStarting)
			if guilds, err := client.Rest.GetCurrentUserGuilds("", 0, 0, 100, false); err == nil {
				var wg sync.WaitGroup
				sem := make(chan struct{}, 5)

				for _, g := range guilds {
					wg.Add(1)
					go func(guild discord.OAuth2Guild) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, guild.ID, false); err == nil && len(cmds) > 0 {
							LogInfo(MsgLoaderScanCleared, guild.Name, guild.ID.String())
							_, _ = client.Rest.SetGuildCommands(client.ApplicationID, guild.ID, []discord.ApplicationCommandCreate{})
						}
					}(g)
				}
				wg.Wait()
			}
		}

		if lastGuildID != "" {
			if id, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, id, false); err == nil && len(cmds) > 0 {
					LogInfo(MsgLoaderCleanup, lastGuildID)
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

		if shouldRegister {
			LogInfo(MsgLoaderDevStarting, guildIDStr)
			createdCommands, err := client.Rest.SetGuildCommands(client.ApplicationID, guildID, commands)
			if err != nil {
				LogWarn(MsgLoaderDevFail, err)
			} else {
				for _, cmd := range createdCommands {
					LogInfo(MsgLoaderDevRegistered, cmd.Name())
				}
			}
		}

		if lastMode != currentMode || forceScan {
			if cmds, err := client.Rest.GetGlobalCommands(client.ApplicationID, false); err == nil && len(cmds) > 0 {
				LogInfo(MsgLoaderDevGlobalClear)
				_, err = client.Rest.SetGlobalCommands(client.ApplicationID, []discord.ApplicationCommandCreate{})
				if err != nil {
					LogWarn(MsgLoaderDevGlobalClearFail, err)
				}
			}
		}

		if lastGuildID != "" && lastGuildID != guildIDStr {
			if oldID, err := snowflake.Parse(lastGuildID); err == nil {
				if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, oldID, false); err == nil && len(cmds) > 0 {
					LogInfo(MsgLoaderCleanup, lastGuildID)
					_, _ = client.Rest.SetGuildCommands(client.ApplicationID, oldID, []discord.ApplicationCommandCreate{})
				}
			}
		}

		if forceScan {
			LogInfo(MsgLoaderScanStarting)
			if guilds, err := client.Rest.GetCurrentUserGuilds("", 0, 0, 100, false); err == nil {
				var wg sync.WaitGroup
				sem := make(chan struct{}, 5)

				for _, g := range guilds {
					if g.ID == guildID {
						continue
					}
					wg.Add(1)
					go func(guild discord.OAuth2Guild) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()

						if cmds, err := client.Rest.GetGuildCommands(client.ApplicationID, guild.ID, false); err == nil && len(cmds) > 0 {
							LogInfo(MsgLoaderScanCleared, guild.Name, guild.ID.String())
							_, _ = client.Rest.SetGuildCommands(client.ApplicationID, guild.ID, []discord.ApplicationCommandCreate{})
						}
					}(g)
				}
				wg.Wait()
			}
		}
	}

	// 3. Update State
	_ = SetBotConfig(ctx, "last_reg_mode", currentMode)
	_ = SetBotConfig(ctx, "last_guild_id", guildIDStr)
	if currentHash != "" {
		_ = SetBotConfig(ctx, "last_cmd_hash", currentHash)
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
	duration := time.Since(StartupTime)
	LogInfo(MsgBotReady, botUser.Username, botUser.ID.String(), os.Getpid(), duration.Milliseconds())

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
	starter func(ctx context.Context) (bool, func(), func())
	logger  func(format string, v ...any)
}

var registeredDaemons []daemonEntry
var activeShutdownHooks []func()
var activeShutdownMu sync.Mutex

// RegisterDaemon registers a background daemon with a logger and start function
func RegisterDaemon(logger func(format string, v ...any), starter func(ctx context.Context) (bool, func(), func())) {
	registeredDaemons = append(registeredDaemons, daemonEntry{starter: starter, logger: logger})
}

// StartDaemons starts all registered daemons with their individual colored logging
func StartDaemons(ctx context.Context) {
	daemonsOnce.Do(func() {
		type activeDaemon struct {
			entry daemonEntry
			run   func()
		}
		var active []activeDaemon

		// 1. Evaluate starters sequentially to determine active daemons
		for _, daemon := range registeredDaemons {
			if ok, run, shutdown := daemon.starter(ctx); ok && run != nil {
				if shutdown != nil {
					activeShutdownMu.Lock()
					activeShutdownHooks = append(activeShutdownHooks, shutdown)
					activeShutdownMu.Unlock()
				}
				active = append(active, activeDaemon{daemon, run})
			}
		}

		// 2. Log all "Starting..." messages sequentially
		for _, ad := range active {
			ad.entry.logger(MsgDaemonStarting)
		}

		// 3. Launch the actual daemon loops in parallel
		for _, ad := range active {
			go ad.run()
		}
	})
}

// ShutdownDaemons gracefully stops all active daemons
func ShutdownDaemons(ctx context.Context) {
	activeShutdownMu.Lock()
	defer activeShutdownMu.Unlock()

	var wg sync.WaitGroup
	for _, shutdown := range activeShutdownHooks {
		if shutdown != nil {
			wg.Add(1)
			go func(s func()) {
				defer wg.Done()
				s()
			}(shutdown)
		}
	}
	wg.Wait()
}
