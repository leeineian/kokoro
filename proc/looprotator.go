package proc

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/leeineian/minder/sys"
)

// Constants
const (
	LoopWebhookName = "LoopHook"
	LoopBatchSize   = 25
)

func init() {
	sys.OnSessionReady(func(s *discordgo.Session) {
		sys.RegisterDaemon(sys.LogLoopRotator, func() { InitLoopRotator(s) })
	})
}

// WebhookData holds a webhook and its channel info
type WebhookData struct {
	Webhook     *discordgo.Webhook
	ChannelName string
}

// ChannelData holds the configuration and webhooks for a loop
type ChannelData struct {
	Config *sys.LoopConfig
	Hooks  []WebhookData
}

// LoopState tracks the running state of a loop
type LoopState struct {
	StopChan        chan struct{}
	RoundsTotal     int
	CurrentRound    int
	IntervalTimeout *time.Timer
}

// State maps
var (
	configuredChannels sync.Map // map[channelID]*ChannelData
	activeLoops        sync.Map // map[channelID]*LoopState
)

// InitLoopRotator initializes the loop rotator daemon
func InitLoopRotator(s *discordgo.Session) {
	configs, err := sys.GetAllLoopConfigs()
	if err != nil {
		sys.LogLoopRotator("Failed to load configs: %v", err)
		return
	}

	sys.LogLoopRotator("Loading %d configured channels from DB...", len(configs))

	for _, config := range configs {
		configuredChannels.Store(config.ChannelID, &ChannelData{
			Config: config,
			Hooks:  nil, // Lazy load
		})
	}

	sys.LogLoopRotator("Loaded configuration for %d channels (Lazy).", len(configs))

	// Auto-resume running loops
	resumeCount := 0
	for _, config := range configs {
		if config.IsRunning {
			resumeCount++
			go func(cfg *sys.LoopConfig) {
				data, ok := configuredChannels.Load(cfg.ChannelID)
				if !ok {
					return
				}
				channelData := data.(*ChannelData)

				// Load webhooks
				if err := loadWebhooksForChannel(s, channelData); err != nil {
					sys.LogLoopRotator("Failed to resume %s: %v", cfg.ChannelName, err)
					return
				}

				startLoopInternal(cfg.ChannelID, channelData, s)
			}(config)
		}
	}

	if resumeCount > 0 {
		sys.LogLoopRotator("Resuming %d active loops...", resumeCount)
	}
}

// parseInterval parses an interval string to duration
// Supports: "30s", "5m", "1min", "2h", "1hr", or just numbers as seconds
func parseInterval(interval string) (time.Duration, error) {
	if interval == "0" || interval == "" {
		return 0, nil
	}

	re := regexp.MustCompile(`^(\d+)(s|m|min|h|hr)?$`)
	match := re.FindStringSubmatch(strings.ToLower(interval))
	if match == nil {
		return 0, fmt.Errorf("invalid interval format: %s", interval)
	}

	value, _ := strconv.Atoi(match[1])
	unit := match[2]
	if unit == "" {
		unit = "s"
	}

	switch unit {
	case "s":
		return time.Duration(value) * time.Second, nil
	case "m", "min":
		return time.Duration(value) * time.Minute, nil
	case "h", "hr":
		return time.Duration(value) * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown interval unit: %s", unit)
	}
}

// FormatInterval formats a duration for display
func FormatInterval(d time.Duration) string {
	if d == 0 {
		return "∞ (Random)"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// IntervalMsToDuration converts milliseconds to duration
func IntervalMsToDuration(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// ParseIntervalString parses an interval string and returns a duration
func ParseIntervalString(interval string) (time.Duration, error) {
	return parseInterval(interval)
}

// loadWebhooksForChannel loads webhooks for a channel or category
func loadWebhooksForChannel(s *discordgo.Session, data *ChannelData) error {
	if data.Hooks != nil {
		return nil // Already loaded
	}

	channel, err := s.Channel(data.Config.ChannelID)
	if err != nil {
		return fmt.Errorf("failed to fetch channel: %w", err)
	}

	if data.Config.ChannelType == "category" {
		return prepareWebhooksForCategory(s, channel, data)
	}
	return prepareWebhooksForSingleChannel(s, channel, data)
}

// prepareWebhooksForSingleChannel prepares webhooks for a single channel
func prepareWebhooksForSingleChannel(s *discordgo.Session, channel *discordgo.Channel, data *ChannelData) error {
	webhooks, err := s.ChannelWebhooks(channel.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch webhooks: %w", err)
	}

	// Find existing webhook owned by bot
	var hook *discordgo.Webhook
	for _, wh := range webhooks {
		if wh.User != nil && wh.User.ID == s.State.User.ID && wh.Name == LoopWebhookName {
			hook = wh
			break
		}
	}

	// Create if not exists
	if hook == nil {
		if len(webhooks) >= 10 {
			sys.LogLoopRotator("Channel %s has 10 webhooks, skipping", channel.Name)
			return nil
		}
		hook, err = s.WebhookCreate(channel.ID, LoopWebhookName, s.State.User.AvatarURL("128"))
		if err != nil {
			return fmt.Errorf("failed to create webhook: %w", err)
		}
	}

	data.Hooks = []WebhookData{{Webhook: hook, ChannelName: channel.Name}}
	sys.LogLoopRotator("Prepared webhook for channel: %s", channel.Name)
	return nil
}

// prepareWebhooksForCategory prepares webhooks for all text channels in a category
func prepareWebhooksForCategory(s *discordgo.Session, category *discordgo.Channel, data *ChannelData) error {
	guild, err := s.State.Guild(category.GuildID)
	if err != nil {
		guild, err = s.Guild(category.GuildID)
		if err != nil {
			return fmt.Errorf("failed to fetch guild: %w", err)
		}
	}

	var hooks []WebhookData
	for _, ch := range guild.Channels {
		if ch.ParentID != category.ID || ch.Type != discordgo.ChannelTypeGuildText {
			continue
		}

		webhooks, err := s.ChannelWebhooks(ch.ID)
		if err != nil {
			sys.LogLoopRotator("Failed to fetch webhooks for %s: %v", ch.Name, err)
			continue
		}

		var hook *discordgo.Webhook
		for _, wh := range webhooks {
			if wh.User != nil && wh.User.ID == s.State.User.ID && wh.Name == LoopWebhookName {
				hook = wh
				break
			}
		}

		if hook == nil {
			if len(webhooks) >= 10 {
				sys.LogLoopRotator("Channel %s has 10 webhooks, skipping", ch.Name)
				continue
			}
			hook, err = s.WebhookCreate(ch.ID, LoopWebhookName, s.State.User.AvatarURL("128"))
			if err != nil {
				sys.LogLoopRotator("Failed to create webhook for %s: %v", ch.Name, err)
				continue
			}
		}

		hooks = append(hooks, WebhookData{Webhook: hook, ChannelName: ch.Name})
		sys.LogLoopRotator("Prepared webhook for channel: %s", ch.Name)
	}

	data.Hooks = hooks
	sys.LogLoopRotator("Prepared %d webhooks for category: %s", len(hooks), category.Name)
	return nil
}

// startLoopInternal starts a loop for a channel
func startLoopInternal(channelID string, data *ChannelData, s *discordgo.Session) {
	stopChan := make(chan struct{})
	state := &LoopState{
		StopChan:     stopChan,
		RoundsTotal:  0,
		CurrentRound: 0,
	}
	activeLoops.Store(channelID, state)
	sys.SetLoopState(channelID, true)

	isAlive := func() bool {
		_, ok := activeLoops.Load(channelID)
		return ok
	}

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			sys.SetLoopState(channelID, false)

			// Rename to inactive
			if data.Config.InactiveChannelName != "" {
				renameChannel(s, channelID, data.Config.InactiveChannelName)
			}
		}()

		// Rename to active
		if data.Config.ActiveChannelName != "" {
			renameChannel(s, channelID, data.Config.ActiveChannelName)
		}

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimedMode := interval > 0
		isRandomMode := interval == 0

		if isTimedMode {
			sys.LogLoopRotator("Starting timed loop for %s", FormatInterval(interval))
			state.IntervalTimeout = time.AfterFunc(interval, func() {
				sys.LogLoopRotator("Time limit reached for %s", data.Config.ChannelName)
				StopLoopInternal(channelID, s)
			})
		} else if isRandomMode {
			sys.LogLoopRotator("Starting infinite random mode for %s", data.Config.ChannelName)
		}

		for isAlive() {
			if isRandomMode {
				// Random mode: 1-100 rounds, 1-10 min delays
				randomRounds := rand.Intn(100) + 1
				randomDelay := time.Duration(rand.Intn(10)+1) * time.Minute

				state.RoundsTotal = randomRounds
				state.CurrentRound = 0

				sys.LogLoopRotator("[%s] Random: %d rounds, next delay: %s",
					data.Config.ChannelName, randomRounds, FormatInterval(randomDelay))

				for i := 0; i < randomRounds && isAlive(); i++ {
					state.CurrentRound = i + 1
					executeRound(data, isAlive, s)
				}

				if !isAlive() {
					break
				}

				// Wait before next iteration
				select {
				case <-time.After(randomDelay):
				case <-stopChan:
					return
				}
			} else if isTimedMode {
				state.CurrentRound++
				executeRound(data, isAlive, s)
			}
		}

		if state.IntervalTimeout != nil {
			state.IntervalTimeout.Stop()
		}
	}()
}

// executeRound sends messages to all webhooks
func executeRound(data *ChannelData, isAlive func() bool, s *discordgo.Session) {
	content := data.Config.Message
	if content == "" {
		content = "@everyone"
	}

	// Parse mentions for allowed_mentions
	var allowedMentions *discordgo.MessageAllowedMentions
	if strings.Contains(content, "@everyone") || strings.Contains(content, "@here") {
		allowedMentions = &discordgo.MessageAllowedMentions{
			Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeEveryone},
		}
	}

	webhookAuthor := data.Config.WebhookAuthor
	if webhookAuthor == "" {
		webhookAuthor = LoopWebhookName
	}

	webhookAvatar := data.Config.WebhookAvatar
	if webhookAvatar == "" && s.State.User != nil {
		webhookAvatar = s.State.User.AvatarURL("128")
	}

	for i := 0; i < len(data.Hooks); i += LoopBatchSize {
		if !isAlive() {
			break
		}

		end := i + LoopBatchSize
		if end > len(data.Hooks) {
			end = len(data.Hooks)
		}
		batch := data.Hooks[i:end]

		var wg sync.WaitGroup
		for _, hookData := range batch {
			if !isAlive() {
				break
			}

			wg.Add(1)
			go func(hd WebhookData) {
				defer wg.Done()

				_, err := s.WebhookExecute(hd.Webhook.ID, hd.Webhook.Token, false, &discordgo.WebhookParams{
					Content:         content,
					Username:        webhookAuthor,
					AvatarURL:       webhookAvatar,
					AllowedMentions: allowedMentions,
				})
				if err != nil {
					sys.LogLoopRotator("Failed to send to %s: %v", hd.ChannelName, err)
				}
			}(hookData)
		}
		wg.Wait()
	}
}

// renameChannel renames a channel
func renameChannel(s *discordgo.Session, channelID, newName string) {
	_, err := s.ChannelEdit(channelID, &discordgo.ChannelEdit{Name: newName})
	if err != nil {
		sys.LogLoopRotator("Failed to rename channel: %v", err)
	}
}

// StopLoopInternal stops a loop and performs cleanup
func StopLoopInternal(channelID string, s *discordgo.Session) bool {
	stateVal, ok := activeLoops.Load(channelID)
	if !ok {
		return false
	}
	state := stateVal.(*LoopState)

	close(state.StopChan)
	if state.IntervalTimeout != nil {
		state.IntervalTimeout.Stop()
	}

	activeLoops.Delete(channelID)
	sys.SetLoopState(channelID, false)

	dataVal, ok := configuredChannels.Load(channelID)
	if ok {
		data := dataVal.(*ChannelData)
		if data.Config.InactiveChannelName != "" {
			renameChannel(s, channelID, data.Config.InactiveChannelName)
		}
		sys.LogLoopRotator("Stopped loop for: %s", data.Config.ChannelName)
	}

	return true
}

// GetActiveLoops returns a copy of active loop states for autocomplete
func GetActiveLoops() map[string]*LoopState {
	result := make(map[string]*LoopState)
	activeLoops.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(*LoopState)
		return true
	})
	return result
}

// GetConfiguredChannels returns a copy of configured channels for autocomplete
func GetConfiguredChannels() map[string]*ChannelData {
	result := make(map[string]*ChannelData)
	configuredChannels.Range(func(key, value interface{}) bool {
		result[key.(string)] = value.(*ChannelData)
		return true
	})
	return result
}

// SetLoopConfig configures a channel for looping (called from command handler)
func SetLoopConfig(s *discordgo.Session, channelID string, config *sys.LoopConfig) error {
	if err := sys.AddLoopConfig(channelID, config); err != nil {
		return err
	}

	data := &ChannelData{Config: config, Hooks: nil}
	configuredChannels.Store(channelID, data)

	sys.LogLoopRotator("Configured channel: %s", config.ChannelName)
	return nil
}

// StartLoop starts a loop for a channel ID
func StartLoop(s *discordgo.Session, channelID string, interval time.Duration) error {
	dataVal, ok := configuredChannels.Load(channelID)
	if !ok {
		return fmt.Errorf("channel not configured")
	}
	data := dataVal.(*ChannelData)

	// Check if already running
	if _, running := activeLoops.Load(channelID); running {
		return fmt.Errorf("loop already running")
	}

	// Load webhooks if needed
	if err := loadWebhooksForChannel(s, data); err != nil {
		return err
	}

	// Apply runtime interval
	data.Config.Interval = int(interval.Milliseconds())

	startLoopInternal(channelID, data, s)
	return nil
}

// DeleteLoopConfig removes a loop configuration
func DeleteLoopConfig(channelID string) error {
	StopLoopInternal(channelID, nil)
	configuredChannels.Delete(channelID)
	return sys.DeleteLoopConfig(channelID)
}
