package proc

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/leeineian/minder/sys"
)

// --- Types ---

type WebhookData struct {
	WebhookID    snowflake.ID
	WebhookToken string
	ChannelName  string
	ThreadIDs    []snowflake.ID
}

type ChannelData struct {
	Config *sys.LoopConfig
	Hooks  []WebhookData
}

type LoopState struct {
	StopChan        chan struct{}
	ResumeChan      chan struct{}
	IsPaused        bool
	VoteMessageID   snowflake.ID
	Votes           map[snowflake.ID]struct{}
	RoundsTotal     int
	CurrentRound    int
	NextRun         time.Time
	EndTime         time.Time
	DurationTimeout *time.Timer
	NeededVotes     int
}

type webhookCacheEntry struct {
	Webhooks map[snowflake.ID][]discord.Webhook
	Fetched  time.Time
}

// --- Globals & Constants ---

const (
	LoopWebhookName = "LoopHook"
	WebhookCacheTTL = 5 * time.Minute
)

var (
	// Configuration & State Maps
	configuredChannels sync.Map // map[snowflake.ID]*ChannelData
	activeLoops        sync.Map // map[snowflake.ID]*LoopState

	// Caching
	globalWebhookCache sync.Map // map[snowflake.ID]webhookCacheEntry
	globalWebhookMu    sync.Map // map[snowflake.ID]*sync.Mutex

	// System Flags
	isEmergencyStop int32

	// Semaphores
	// Limit webhook creation/deletion operations
	webhookOpSem = make(chan struct{}, 1)
	// Limit concurrent message sends (prevents connection exhaustion/503s)
	messageSendSem = make(chan struct{}, 200)

	// Serial Loop Queue
	loopQueue     []snowflake.ID
	shuffledQueue []snowflake.ID
	loopQueueMu   sync.Mutex
)

// --- Initialization ---

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		sys.RegisterDaemon(sys.LogLoopManager, func(ctx context.Context) (bool, func()) { return InitLoopManager(ctx, client) })
	})
}

func InitLoopManager(ctx context.Context, client *bot.Client) (bool, func()) {
	sys.RegisterComponentHandler("vote:", handleVoteButton)

	// Register rate limit failsafe
	sys.OnRateLimitExceeded(func() {
		StopAllLoops(ctx, client)
	})

	if err := sys.ResetAllLoopStates(ctx); err != nil {
		sys.LogLoopManager("⚠️ Failed to reset loop states: %v", err)
	}

	configs, err := sys.GetAllLoopConfigs(ctx)
	if err != nil {
		sys.LogLoopManager(sys.MsgLoopFailedToLoadConfigs, err)
		return false, nil
	}

	if len(configs) == 0 {
		return false, nil
	}

	for _, config := range configs {
		data := &ChannelData{
			Config: config,
			Hooks:  nil,
		}
		configuredChannels.Store(config.ChannelID, data)
	}

	if len(configs) > 0 {
		sys.LogLoopManager(sys.MsgLoopLoadedChannels, len(configs))
	}

	return true, nil
}

// --- Public Config API ---

func SetLoopConfig(ctx context.Context, client *bot.Client, channelID snowflake.ID, config *sys.LoopConfig) error {
	if err := sys.AddLoopConfig(ctx, channelID, config); err != nil {
		return err
	}
	configuredChannels.Store(channelID, &ChannelData{Config: config})
	sys.LogLoopManager(sys.MsgLoopConfigured, config.ChannelName)
	return nil
}

func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID, client *bot.Client) error {
	StopLoopInternal(ctx, channelID, client)
	configuredChannels.Delete(channelID)
	return sys.DeleteLoopConfig(ctx, channelID)
}

func GetActiveLoops() map[snowflake.ID]*LoopState {
	res := make(map[snowflake.ID]*LoopState)
	activeLoops.Range(func(k, v interface{}) bool {
		res[k.(snowflake.ID)] = v.(*LoopState)
		return true
	})
	return res
}

func GetConfiguredChannels() map[snowflake.ID]*ChannelData {
	res := make(map[snowflake.ID]*ChannelData)
	configuredChannels.Range(func(k, v interface{}) bool {
		res[k.(snowflake.ID)] = v.(*ChannelData)
		return true
	})
	return res
}

// --- Public Control API ---

func StartLoop(ctx context.Context, client *bot.Client, channelID snowflake.ID, interval time.Duration) error {
	return BatchStartLoops(ctx, client, []snowflake.ID{channelID}, interval)
}

func BatchStartLoops(ctx context.Context, client *bot.Client, channelIDs []snowflake.ID, interval time.Duration) error {
	if atomic.LoadInt32(&isEmergencyStop) == 1 {
		return fmt.Errorf("cannot start loops: system is currently in emergency stop due to rate limits")
	}

	var toStart []*ChannelData

	// 1. Preparation Phase (Webhooks)
	for _, id := range channelIDs {
		dataVal, ok := configuredChannels.Load(id)
		if !ok {
			continue
		}
		data := dataVal.(*ChannelData)

		if _, running := activeLoops.Load(id); running {
			continue
		}

		// Prepare webhooks (detect existing or create new)
		if err := loadWebhooksForChannelWithCache(ctx, client, data); err != nil {
			sys.LogLoopManager("❌ Failed to prepare webhooks for %s: %v", id, err)
			continue
		}

		// Apply runtime interval if provided
		if interval > 0 {
			data.Config.Interval = int(interval.Milliseconds())
		}
		toStart = append(toStart, data)
	}

	if len(toStart) == 0 {
		return fmt.Errorf("no loops were able to start")
	}

	// 2. Queue Phase: Populate Serial Queue
	loopQueueMu.Lock()
	loopQueue = make([]snowflake.ID, 0, len(toStart))
	// Sort toStart by channel name for deterministic order
	sort.Slice(toStart, func(i, j int) bool {
		return toStart[i].Config.ChannelName < toStart[j].Config.ChannelName
	})

	var names []string
	for _, data := range toStart {
		loopQueue = append(loopQueue, data.Config.ChannelID)
		names = append(names, data.Config.ChannelName)
	}
	sys.LogLoopManager("Queuing %d loops: %s", len(toStart), strings.Join(names, ", "))
	shuffledQueue = nil // Reset shuffled queue to force rethink on start
	loopQueueMu.Unlock()

	// 3. Execution Phase: Start the first one
	startNextInQueue(ctx, client)

	return nil
}

func startNextInQueue(ctx context.Context, client *bot.Client) {
	loopQueueMu.Lock()
	if len(loopQueue) == 0 {
		shuffledQueue = nil
		loopQueueMu.Unlock()
		return
	}

	// Refill and shuffle if empty
	if len(shuffledQueue) == 0 {
		shuffledQueue = make([]snowflake.ID, len(loopQueue))
		copy(shuffledQueue, loopQueue)
		rand.Shuffle(len(shuffledQueue), func(i, j int) {
			shuffledQueue[i], shuffledQueue[j] = shuffledQueue[j], shuffledQueue[i]
		})
		sys.LogLoopManager("Shuffled execution set for %d categories.", len(shuffledQueue))
	}

	// Pick next
	nextID := shuffledQueue[0]
	// Remove from shuffled queue
	shuffledQueue = shuffledQueue[1:]
	loopQueueMu.Unlock()

	dataVal, ok := configuredChannels.Load(nextID)
	if !ok {
		// If config deleted mid-run, skip to next
		startNextInQueue(ctx, client)
		return
	}
	data := dataVal.(*ChannelData)

	sys.LogLoopManager("[%s] Starting serial loop...", data.Config.ChannelName)
	startLoopInternal(ctx, nextID, data, client)
}

// StopAllLoops stops all active loops immediately. Called by the rate-limit fail-safe.
func StopAllLoops(ctx context.Context, client *bot.Client) {
	if !atomic.CompareAndSwapInt32(&isEmergencyStop, 0, 1) {
		return
	}

	sys.LogLoopManager("🚨 FAILURE DETECTED: Rate limit threshold exceeded.")
	sys.LogLoopManager("🛑 Loop system fail-safe triggered. Stopping all active operations to protect the account.")

	count := 0
	activeLoops.Range(func(key, value interface{}) bool {
		channelID := key.(snowflake.ID)
		if StopLoopInternal(ctx, channelID, client) {
			count++
		}
		return true
	})

	// Clear the serial queue so nothing else starts automatically
	loopQueueMu.Lock()
	loopQueue = nil
	shuffledQueue = nil
	loopQueueMu.Unlock()

	sys.LogLoopManager("✅ Fail-safe complete. %d loops have been stopped.", count)

	// Reset emergency status immediately
	atomic.StoreInt32(&isEmergencyStop, 0)
}

func StopLoopInternal(ctx context.Context, channelID snowflake.ID, client *bot.Client) bool {
	// Remove from serial queue to prevent it from coming back
	loopQueueMu.Lock()
	for i, id := range loopQueue {
		if id == channelID {
			// Remove element
			loopQueue = append(loopQueue[:i], loopQueue[i+1:]...)
			break
		}
	}
	for i, id := range shuffledQueue {
		if id == channelID {
			// Remove element
			shuffledQueue = append(shuffledQueue[:i], shuffledQueue[i+1:]...)
			break
		}
	}
	loopQueueMu.Unlock()

	if val, ok := activeLoops.LoadAndDelete(channelID); ok {
		state := val.(*LoopState)
		close(state.StopChan)
		if state.DurationTimeout != nil {
			state.DurationTimeout.Stop()
		}
		if dataVal, ok := configuredChannels.Load(channelID); ok {
			sys.LogLoopManager(sys.MsgLoopStopped, dataVal.(*ChannelData).Config.ChannelName)
		}
		return true
	}
	return false
}

func handleVoteButton(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	parts := strings.Split(customID, ":")
	if len(parts) < 2 {
		return
	}

	channelIDStr := parts[1]
	channelID, err := snowflake.Parse(channelIDStr)
	if err != nil {
		return
	}

	stateVal, ok := activeLoops.Load(channelID)
	if !ok {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ Loop is no longer active.").SetEphemeral(true).Build())
		return
	}
	state := stateVal.(*LoopState)

	dataVal, ok := configuredChannels.Load(channelID)
	if !ok {
		return
	}
	cfg := dataVal.(*ChannelData).Config

	if !state.IsPaused || state.VoteMessageID != event.Message.ID {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ This vote panel is no longer valid.").SetEphemeral(true).Build())
		return
	}

	// Verify Role
	hasRole := false
	requiredRoleID, _ := snowflake.Parse(cfg.VoteRole)
	for _, roleID := range event.Member().RoleIDs {
		if roleID == requiredRoleID {
			hasRole = true
			break
		}
	}

	if !hasRole {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ You do not have the required role to vote.").SetEphemeral(true).Build())
		return
	}

	// Check if already voted
	if state.Votes == nil {
		state.Votes = make(map[snowflake.ID]struct{})
	}

	userID := event.User().ID
	if _, voted := state.Votes[userID]; voted {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("⚠️ You have already voted!").SetEphemeral(true).Build())
		return
	}

	// Add vote
	state.Votes[userID] = struct{}{}

	// Use cached NeededVotes
	validVotes := len(state.Votes)
	neededVotes := state.NeededVotes
	if neededVotes == 0 {
		neededVotes = 1
	}

	// Update Button Label
	label := formatVoteLabel(validVotes, neededVotes)

	style := discord.ButtonStyleDanger
	if validVotes >= neededVotes {
		style = discord.ButtonStyleSuccess
	}

	panelContent := cfg.VoteMessage
	if panelContent == "" {
		panelContent = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", cfg.ChannelName)
	}

	// Update the message
	updateBuilder := discord.NewMessageUpdateBuilder().
		SetIsComponentsV2(true).
		SetComponents(
			discord.NewContainer(
				discord.NewSection(
					discord.NewTextDisplay(panelContent),
				).WithAccessory(
					discord.NewButton(style, label, customID, "", 0),
				),
			),
		)
	if err := event.UpdateMessage(updateBuilder.Build()); err != nil {
		sys.LogLoopManager("⚠️ Failed to update vote panel: %v", err)
	}

	sys.LogLoopManager("[%s] Vote: %d/%d (%d%% threshold)", cfg.ChannelName, validVotes, neededVotes, cfg.VoteThreshold)

	if validVotes >= neededVotes {
		sys.LogLoopManager("[%s] Vote Threshold Met! Resuming loop...", cfg.ChannelName)
		// Resume
		select {
		case state.ResumeChan <- struct{}{}:
		default:
		}
	}
}

func getRoleMemberCount(client *bot.Client, guildID snowflake.ID, roleID snowflake.ID) int {
	var members []discord.Member
	chunk, err := client.Rest.GetMembers(guildID, 1000, 0)
	if err != nil {
		return 1 // Fail-safe
	}
	members = append(members, chunk...)
	if len(chunk) == 1000 {
		for {
			lastID := chunk[len(chunk)-1].User.ID
			chunk, err = client.Rest.GetMembers(guildID, 1000, lastID)
			if err != nil || len(chunk) == 0 {
				break
			}
			members = append(members, chunk...)
			if len(chunk) < 1000 {
				break
			}
		}
	}

	count := 0
	for _, m := range members {
		if m.User.Bot {
			continue
		}
		for _, rID := range m.RoleIDs {
			if rID == roleID {
				count++
				break
			}
		}
	}
	if count == 0 {
		return 1
	}
	return count
}

func formatVoteLabel(current, total int) string {
	return fmt.Sprintf("%d/%d Votes", current, total)
}

// --- Core Logic ---

func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client *bot.Client) {
	stopChan := make(chan struct{})
	resumeChan := make(chan struct{})
	state := &LoopState{
		StopChan:   stopChan,
		ResumeChan: resumeChan,
	}
	activeLoops.Store(channelID, state)
	sys.SetLoopState(ctx, channelID, true)

	go func() {
		defer func() {
			activeLoops.Delete(channelID)
			sys.SetLoopState(ctx, channelID, false)
		}()

		// Local RNG for this loop instance (avoids global lock convention)
		seed := time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		// Pre-allocated buffer for hooks to reduce GC pressure
		hookBuf := make([]WebhookData, len(data.Hooks))

		// Jitter: random 1 to 10-second delay before starting
		jitter := time.Duration(rng.Intn(10)+1) * time.Second
		sys.LogLoopManager("[%s] Applying %s startup jitter...", data.Config.ChannelName, jitter)
		select {
		case <-time.After(jitter):
		case <-stopChan:
			return
		}

		interval := time.Duration(data.Config.Interval) * time.Millisecond
		isTimed := interval > 0

		if isTimed {
			sys.LogLoopManager(sys.MsgLoopStartingTimed, FormatDuration(interval))
			state.EndTime = time.Now().UTC().Add(interval)
			state.DurationTimeout = time.AfterFunc(interval, func() {
				StopLoopInternal(ctx, channelID, client)
			})
		}

		content := data.Config.Message
		if content == "" {
			content = "@everyone"
		}
		author := data.Config.WebhookAuthor
		if author == "" {
			author = LoopWebhookName
		}
		avatar := data.Config.WebhookAvatar
		if avatar == "" {
			if ch, ok := client.Caches.Channel(channelID); ok {
				if guild, ok := client.Caches.Guild(ch.GuildID()); ok {
					if iconURL := guild.IconURL(); iconURL != nil {
						avatar = *iconURL
					}
				}
			}
		}

		threadContent := data.Config.ThreadMessage

		if isTimed {
			// --- TIMED MODE ---
			for {
				select {
				case <-stopChan:
					return
				default:
				}
				state.CurrentRound++
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			}
		} else {
			// Random Mode
			rounds := rng.Intn(100) + 1
			var delay time.Duration

			state.RoundsTotal = rounds
			state.CurrentRound = 0

			totalPings := rounds * len(data.Hooks) * (1 + data.Config.ThreadCount)
			if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
				sys.LogLoopManager("[%s] Random: %d rounds (%d pings)", data.Config.ChannelName, rounds, totalPings)
				// Delay remains 0, won't be used
			} else {
				delay = time.Duration(rng.Intn(1000)+1) * time.Second
				sys.LogLoopManager(sys.MsgLoopRandomStatus, data.Config.ChannelName, rounds, totalPings, FormatDuration(delay))
			}

			for i := 0; i < rounds; i++ {
				select {
				case <-stopChan:
					return
				default:
				}
				state.CurrentRound = i + 1
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			}

			state.EndTime = time.Time{}
			if delay > 0 {
				state.NextRun = time.Now().UTC().Add(delay)
			} else {
				state.NextRun = time.Time{} // Paused or instant
			}

			// Interaction / Pause Logic
			// Interaction / Pause Logic
			if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
				sys.LogLoopManager("[%s] Pausing for vote...", data.Config.ChannelName)
				state.IsPaused = true
				state.NextRun = time.Time{} // Clear next run as we are paused indefinitely

				// 1. Get or Create Webhook for the Vote Channel
				var voteChanID snowflake.ID

				// Handle potential legacy format (CHAN:MSG) or just CHAN
				rawID := data.Config.VoteChannelID
				if strings.Contains(rawID, ":") {
					parts := strings.Split(rawID, ":")
					if len(parts) > 0 {
						rawID = parts[0]
					}
				}

				if pid, err := snowflake.Parse(rawID); err == nil {
					voteChanID = pid
				} else {
					sys.LogLoopManager("⚠️ Invalid VoteChannelID '%s', cannot pause for vote.", data.Config.VoteChannelID)
					// Prevent infinite pause without interaction possibility?
					// If we can't post the vote panel, maybe we shouldn't pause?
					// For now, we just fail to post the message, but loop remains paused.
				}

				var hookID snowflake.ID
				var hookToken string

				// Fetch existing webhooks
				if hooks, err := client.Rest.GetWebhooks(voteChanID); err == nil {
					for _, h := range hooks {
						// Look for a token-based incoming webhook, preferably ours
						if incoming, ok := h.(discord.IncomingWebhook); ok {
							hookID = incoming.ID()
							hookToken = incoming.Token
							if incoming.Name() == LoopWebhookName {
								break // Found our preferred one
							}
						}
					}
				}

				if hookID == 0 {
					wh, err := client.Rest.CreateWebhook(voteChanID, discord.WebhookCreate{
						Name: LoopWebhookName,
					})
					if err == nil {
						hookID = wh.ID()
						hookToken = wh.Token
					} else {
						sys.LogLoopManager("⚠️ Failed to create webhook for vote channel %s: %v", voteChanID, err)
					}
				}

				// 2. Send "Panel Message" using the webhook
				if hookID != 0 {
					// Delete old notification if exists (cleanup)
					if state.VoteMessageID != 0 {
						_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
					}

					panelContent := data.Config.VoteMessage
					if panelContent == "" {
						panelContent = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", data.Config.ChannelName)
					}

					// Initialize votes
					state.Votes = make(map[snowflake.ID]struct{})
					requiredRoleID, _ := snowflake.Parse(data.Config.VoteRole)
					guildID := snowflake.ID(0)
					if ch, ok := client.Caches.Channel(channelID); ok {
						guildID = ch.GuildID()
					}

					totalRoleMembers := getRoleMemberCount(client, guildID, requiredRoleID)
					state.NeededVotes = int(math.Ceil(float64(totalRoleMembers) * float64(data.Config.VoteThreshold) / 100.0))
					if state.NeededVotes == 0 {
						state.NeededVotes = 1
					}

					label := formatVoteLabel(0, state.NeededVotes)
					voteCustomID := fmt.Sprintf("vote:%s", channelID)

					builder := discord.NewWebhookMessageCreateBuilder().
						SetIsComponentsV2(true).
						AddComponents(
							discord.NewContainer(
								discord.NewSection(
									discord.NewTextDisplay(panelContent),
								).WithAccessory(
									discord.NewButton(discord.ButtonStyleDanger, label, voteCustomID, "", 0),
								),
							),
						).
						SetAllowedMentions(&discord.AllowedMentions{
							Parse: []discord.AllowedMentionType{
								discord.AllowedMentionTypeEveryone,
								discord.AllowedMentionTypeRoles,
								discord.AllowedMentionTypeUsers,
							},
						}).
						SetUsername(data.Config.ChannelName).
						SetAvatarURL(data.Config.WebhookAvatar)

					msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, builder.Build(), rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

					if err == nil {
						state.VoteMessageID = msg.ID
					} else {
						sys.LogLoopManager("⚠️ Failed to send vote panel message: %v", err)
					}
				}

				// Wait for resume signal
				select {
				case <-resumeChan:
					// Resumed!
					sys.LogLoopManager("[%s] Loop resumed by vote!", data.Config.ChannelName)
					state.IsPaused = false

					// Give a small delay so users see the final vote count and green button
					time.Sleep(3 * time.Second)

					if state.VoteMessageID != 0 {
						// Clean up notification
						_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
						state.VoteMessageID = 0
					}
				case <-stopChan:
					return
				}
			} else {
				// Standard delay
				select {
				case <-time.After(delay):
					state.NextRun = time.Time{}
				case <-stopChan:
					return
				}
			}
		}

		// Natural finish of the batch
		sys.LogLoopManager("[%s] Batch finished. Moving to next in queue...", data.Config.ChannelName)
		// Trigger next loop in the queue
		go startNextInQueue(ctx, client)
	}()
}

func executeRound(ctx context.Context, data *ChannelData, client *bot.Client, stopChan chan struct{}, content, threadContent, author, avatar string, rng *rand.Rand, hookBuf []WebhookData) {
	// Copy hooks to reusable buffer
	if len(hookBuf) != len(data.Hooks) {
		// Should not happen given logic, but safety check
		hookBuf = make([]WebhookData, len(data.Hooks))
	}
	copy(hookBuf, data.Hooks)

	// Shuffle the hooks for true randomness
	rng.Shuffle(len(hookBuf), func(i, j int) {
		hookBuf[i], hookBuf[j] = hookBuf[j], hookBuf[i]
	})

	// Randomize rate for this round: 1-50 messages per second
	rate := rng.Intn(50) + 1
	delay := time.Second / time.Duration(rate)

	var wg sync.WaitGroup
	for _, h := range hookBuf {
		select {
		case <-stopChan:
			goto Wait
		default:
		}

		// Calculate jitter for this worker using the single-threaded RNG
		workerJitter := time.Duration(rng.Intn(50)) * time.Millisecond

		wg.Add(1)
		go func(hd WebhookData, startJitter time.Duration, stepDelay time.Duration) {
			defer wg.Done()

			// Minor jitter to prevent simultaneous network spikes
			select {
			case <-time.After(startJitter):
			case <-stopChan:
				return
			}

			// Helper for sending with retries
			sendWithRetry := func(threadID snowflake.ID, msgContent string) {
				// Acquire semaphore to limit concurrent connections
				select {
				case messageSendSem <- struct{}{}:
					defer func() { <-messageSendSem }()
				case <-stopChan:
					return
				case <-ctx.Done():
					return
				}

				backoffs := []time.Duration{2 * time.Second, 4 * time.Second}
				for attempt := 0; attempt <= len(backoffs); attempt++ {
					// Check stop before each attempt
					select {
					case <-stopChan:
						return
					default:
					}

					params := rest.CreateWebhookMessageParams{Wait: false}
					if threadID != 0 {
						params.ThreadID = threadID
					}

					_, err := client.Rest.CreateWebhookMessage(hd.WebhookID, hd.WebhookToken, discord.WebhookMessageCreate{
						Content:   msgContent,
						Username:  author,
						AvatarURL: avatar,
						AllowedMentions: &discord.AllowedMentions{
							Parse: []discord.AllowedMentionType{
								discord.AllowedMentionTypeEveryone,
								discord.AllowedMentionTypeRoles,
								discord.AllowedMentionTypeUsers,
							},
						},
						Flags: discord.MessageFlagSuppressNotifications,
					}, params, rest.WithCtx(ctx))
					if err == nil {
						return
					}

					if attempt < len(backoffs) {
						select {
						case <-time.After(backoffs[attempt]):
						case <-stopChan:
							return
						case <-ctx.Done():
							return
						}
					}
				}
			}

			// 1. Send to main channel (Synchronous - Priority)
			if content != "" {
				sendWithRetry(0, content)
			}

			// 2. Send to threads (Parallel but Paced)
			var shardWg sync.WaitGroup
			if threadContent != "" && len(hd.ThreadIDs) > 0 {
				for i, tid := range hd.ThreadIDs {
					// Apply rate limit delay before launching next thread
					if i > 0 || content != "" {
						select {
						case <-time.After(stepDelay):
						case <-stopChan:
							return
						}
					}

					shardWg.Add(1)
					go func(tID snowflake.ID) {
						defer shardWg.Done()
						sendWithRetry(tID, threadContent)
					}(tid)
				}
			}

			// Wait for threads to finish
			shardWg.Wait()
		}(h, workerJitter, delay)

		// Variable delay based on randomized rate
		select {
		case <-time.After(delay):
		case <-stopChan:
			goto Wait
		}
	}

Wait:
	wg.Wait()
}

// --- Preparation Logic (Webhooks/Threads) ---

func loadWebhooksForChannelWithCache(ctx context.Context, client *bot.Client, data *ChannelData) error {
	var channel discord.GuildChannel
	var ok bool

	// Wait up to 30 seconds for the channel to appear in cache (it might be missing immediately after READY)
	for i := 0; i < 60; i++ {
		if ch, found := client.Caches.Channel(data.Config.ChannelID); found {
			channel = ch
			ok = true
			break
		}
		if i == 10 { // Only log if still missing after 5 seconds
			sys.LogLoopManager("Still waiting for category %s to appear in cache...", data.Config.ChannelID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if !ok {
		return fmt.Errorf("channel %s not in cache", data.Config.ChannelID)
	}

	guildID := channel.GuildID()

	// Atomic fetch-and-fill to avoid parallel API calls for the same guild
	var webhookMap map[snowflake.ID][]discord.Webhook
	if val, ok := globalWebhookCache.Load(guildID); ok {
		entry := val.(webhookCacheEntry)
		if time.Since(entry.Fetched) < WebhookCacheTTL {
			webhookMap = entry.Webhooks
		}
	}

	if webhookMap == nil {
		// Get or create a mutex for this guild
		muVal, _ := globalWebhookMu.LoadOrStore(guildID, &sync.Mutex{})
		mu := muVal.(*sync.Mutex)

		mu.Lock()
		// Double check after lock
		if val, ok := globalWebhookCache.Load(guildID); ok {
			entry := val.(webhookCacheEntry)
			if time.Since(entry.Fetched) < WebhookCacheTTL {
				webhookMap = entry.Webhooks
				mu.Unlock()
			}
		}

		if webhookMap == nil {
			defer mu.Unlock()
			// 350+ webhooks can be slow to fetch; use a dedicated longer timeout for this heavy operation.
			fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			hooks, err := client.Rest.GetAllWebhooks(guildID, rest.WithCtx(fetchCtx))
			cancel()

			if err != nil {
				return fmt.Errorf("failed to fetch webhooks: %w", err)
			}

			webhookMap = make(map[snowflake.ID][]discord.Webhook)
			for _, wh := range hooks {
				var chID snowflake.ID
				switch w := wh.(type) {
				case discord.IncomingWebhook:
					chID = w.ChannelID
				case discord.ChannelFollowerWebhook:
					chID = w.ChannelID
				}
				if chID != 0 {
					webhookMap[chID] = append(webhookMap[chID], wh)
				}
			}
			globalWebhookCache.Store(guildID, webhookCacheEntry{
				Webhooks: webhookMap,
				Fetched:  time.Now(),
			})
			sys.LogLoopManager("Cached %d webhooks for guild %s", len(hooks), guildID)
		}
	}

	// Always prepare as category (loop system is category-only)
	return prepareWebhooksForCategory(ctx, client, channel, data, webhookMap)
}

func prepareWebhooksForCategory(ctx context.Context, client *bot.Client, category discord.GuildChannel, data *ChannelData, webhookMap map[snowflake.ID][]discord.Webhook) error {
	var targetChannels []discord.GuildMessageChannel
	guildID := category.GuildID()
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if textCh, ok := ch.(discord.GuildMessageChannel); ok {
				if textCh.ParentID() != nil && *textCh.ParentID() == category.ID() {
					targetChannels = append(targetChannels, textCh)
				}
			}
		}
	}

	self, _ := client.Caches.SelfUser()
	var hooks []WebhookData

	// Find threads in cache for this guild
	var activeThreads []discord.GuildThread
	for ch := range client.Caches.Channels() {
		if ch.GuildID() == guildID {
			if thread, ok := ch.(discord.GuildThread); ok {
				activeThreads = append(activeThreads, thread)
			}
		}
	}

	for _, tc := range targetChannels {
		webhooks := webhookMap[tc.ID()]
		var hook *discord.IncomingWebhook
		for _, wh := range webhooks {
			if incoming, ok := wh.(discord.IncomingWebhook); ok {
				if incoming.User.ID == self.ID && incoming.Name() == LoopWebhookName {
					hook = &incoming
					break
				}
			}
		}

		if hook == nil {
			newHook, err := createWebhookWithRetry(ctx, client, tc.ID())
			if err != nil {
				sys.LogLoopManager(sys.MsgLoopFailedToCreateWebhook, tc.Name(), err)
				continue
			}
			hook = newHook
		}

		var threadIDs []snowflake.ID
		if data.Config.UseThread && data.Config.ThreadCount > 0 {
			// Find existing bot threads
			expectedName := "🧵" + tc.Name()
			for _, thread := range activeThreads {
				if thread.ParentID() != nil && *thread.ParentID() == tc.ID() &&
					thread.Name() == expectedName && thread.OwnerID == self.ID {
					threadIDs = append(threadIDs, thread.ID())
					if len(threadIDs) >= data.Config.ThreadCount {
						break
					}
				}
			}

			// Check for archived threads
			if len(threadIDs) < data.Config.ThreadCount {
				archived, err := client.Rest.GetPublicArchivedThreads(tc.ID(), time.Time{}, 0, rest.WithCtx(ctx))
				if err == nil {
					for _, thread := range archived.Threads {
						if thread.Name() == expectedName && thread.OwnerID == self.ID {
							reopened, err := unarchiveThreadWithRetry(ctx, client, thread.ID())
							if err == nil {
								threadIDs = append(threadIDs, reopened.ID())
								if len(threadIDs) >= data.Config.ThreadCount {
									break
								}
							}
						}
					}
				}
			}

			// Create missing threads
			for len(threadIDs) < data.Config.ThreadCount {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				newThread, err := createThreadWithRetry(ctx, client, tc.ID(), expectedName)
				if err != nil {
					sys.LogLoopManager("❌ Failed to create thread for %s: %v", tc.Name(), err)
					break
				}
				threadIDs = append(threadIDs, newThread.ID())
			}
		}

		hooks = append(hooks, WebhookData{
			WebhookID:    hook.ID(),
			WebhookToken: hook.Token,
			ChannelName:  tc.Name(),
			ThreadIDs:    threadIDs,
		})
	}

	data.Hooks = hooks
	sys.LogLoopManager("[%s] Preparing %d webhooks (%d channels + %d threads)...", category.Name(), len(hooks), len(hooks), data.Config.ThreadCount*len(hooks))
	return nil
}

func createWebhookWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID) (*discord.IncomingWebhook, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	sys.LogLoopManager("🔨 [WEBHOOK CREATE] Attempting for channel %s", channelID)
	hook, err := client.Rest.CreateWebhook(channelID, discord.WebhookCreate{Name: LoopWebhookName}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [WEBHOOK CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	sys.LogLoopManager("✅ [WEBHOOK CREATE] Success for channel %s", channelID)
	return hook, nil
}

func createThreadWithRetry(ctx context.Context, client *bot.Client, channelID snowflake.ID, name string) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	sys.LogLoopManager("🔨 [THREAD CREATE] Attempting for channel %s", channelID)
	thread, err := client.Rest.CreateThread(channelID, discord.GuildPublicThreadCreate{
		Name: name,
	}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [THREAD CREATE] Failed for channel %s: %v", channelID, err)
		return nil, err
	}
	sys.LogLoopManager("✅ [THREAD CREATE] Success for channel %s", channelID)
	return thread, nil
}

func unarchiveThreadWithRetry(ctx context.Context, client *bot.Client, threadID snowflake.ID) (*discord.GuildThread, error) {
	webhookOpSem <- struct{}{}
	defer func() {
		// Mandatory pacing to avoid rate limit warnings
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
		<-webhookOpSem
	}()

	sys.LogLoopManager("🔓 [THREAD UNARCHIVE] Attempting for thread %s", threadID)
	channel, err := client.Rest.UpdateChannel(threadID, discord.GuildThreadUpdate{
		Archived: sys.Ptr(false),
	}, rest.WithCtx(ctx))
	if err != nil {
		sys.LogLoopManager("❌ [THREAD UNARCHIVE] Failed for thread %s: %v", threadID, err)
		return nil, err
	}

	thread, ok := channel.(discord.GuildThread)
	if !ok {
		return nil, fmt.Errorf("updated channel %s is not a thread", threadID)
	}

	sys.LogLoopManager("✅ [THREAD UNARCHIVE] Success for thread %s", threadID)
	return &thread, nil
}

// --- Utilities ---

func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "∞"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func ParseDuration(duration string) (time.Duration, error) {
	if duration == "" || duration == "0" {
		return 0, nil
	}
	re := regexp.MustCompile(`^(\d+)(s|m|h)?$`)
	m := re.FindStringSubmatch(strings.ToLower(duration))
	if m == nil {
		return 0, fmt.Errorf("invalid format")
	}
	v, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "m":
		return time.Duration(v) * time.Minute, nil
	case "h":
		return time.Duration(v) * time.Hour, nil
	default:
		return time.Duration(v) * time.Second, nil
	}
}

func IntervalMsToDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
