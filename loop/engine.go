package loop

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/rest"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"
)

func StartLoop(ctx context.Context, client bot.Client, channelID snowflake.ID, rounds int) error {
	return BatchStartLoops(ctx, client, []snowflake.ID{channelID}, rounds)
}

func BatchStartLoops(ctx context.Context, client bot.Client, channelIDs []snowflake.ID, rounds int) error {
	if atomic.LoadInt32(&isEmergencyStop) == 1 {
		return fmt.Errorf("cannot start loops: system is currently in emergency stop due to rate limits")
	}

	var toStart []*ChannelData
	for _, id := range channelIDs {
		dataVal, ok := configuredChannels.Load(id)
		if !ok {
			continue
		}
		data := dataVal.(*ChannelData)
		if _, running := activeLoops.Load(id); running {
			continue
		}
		if err := loadWebhooksForChannelWithCache(ctx, client, data); err != nil {
			loopSys.LogInfo("❌ Failed to prepare webhooks for %s: %v", id, err)
			continue
		}
		toStart = append(toStart, data)
	}

	if len(toStart) == 0 {
		return fmt.Errorf("no loops were able to start")
	}

	var serialToStart []*ChannelData
	parallelCount := 0

	for _, data := range toStart {
		if data.Config.IsSerial {
			serialToStart = append(serialToStart, data)
		} else {
			parallelCount++
			safeGo(func() { startLoopInternal(ctx, data.Config.ChannelID, data, client, rounds) })
		}
	}

	if len(serialToStart) > 0 {
		loopQueueMu.Lock()
		loopQueue = make([]snowflake.ID, 0, len(serialToStart))
		sort.Slice(serialToStart, func(i, j int) bool { return serialToStart[i].Config.ChannelName < serialToStart[j].Config.ChannelName })
		for _, data := range serialToStart {
			loopQueue = append(loopQueue, data.Config.ChannelID)
		}
		shuffledQueue = nil
		loopQueueMu.Unlock()

		if atomic.LoadInt32(&serialActive) == 0 {
			safeGo(func() { startNextInQueue(ctx, client) })
		}
	}

	return nil
}

func StopAllLoops(ctx context.Context, client bot.Client) {
	if !atomic.CompareAndSwapInt32(&isEmergencyStop, 0, 1) {
		return
	}
	activeLoops.Range(func(key, value any) bool {
		StopLoopInternal(ctx, key.(snowflake.ID), client)
		return true
	})
	loopQueueMu.Lock()
	loopQueue = nil
	shuffledQueue = nil
	loopQueueMu.Unlock()
	atomic.StoreInt32(&isEmergencyStop, 0)
}

func StopLoopInternal(ctx context.Context, channelID snowflake.ID, client bot.Client) bool {
	loopQueueMu.Lock()
	loopQueue = removeFromSlice(loopQueue, channelID)
	shuffledQueue = removeFromSlice(shuffledQueue, channelID)
	loopQueueMu.Unlock()

	if val, ok := activeLoops.LoadAndDelete(channelID); ok {
		state := val.(*LoopState)
		close(state.StopChan)
		if dataVal, ok := configuredChannels.Load(channelID); ok {
			loopSys.LogInfo(MsgLoopStopped, dataVal.(*ChannelData).Config.ChannelName)
		}
		return true
	}
	return false
}

func startLoopInternal(ctx context.Context, channelID snowflake.ID, data *ChannelData, client bot.Client, rounds int) {
	stopChan := make(chan struct{})
	resumeChan := make(chan struct{})
	state := &LoopState{
		StopChan:   stopChan,
		ResumeChan: resumeChan,
	}
	activeLoops.Store(channelID, state)
	loopSys.SetLoopState(ctx, channelID, true)

	safeGo(func() {
		defer func() {
			activeLoops.Delete(channelID)
			loopSys.SetLoopState(ctx, channelID, false)
			if data.Config.IsSerial {
				atomic.StoreInt32(&serialActive, 0)
				safeGo(func() { startNextInQueue(ctx, client) })
			}
		}()

		seed := time.Now().UnixNano()
		rng := rand.New(rand.NewSource(seed))

		hookBuf := make([]WebhookData, len(data.Hooks))

		isFixedRounds := rounds > 0

		content := data.Config.Message
		if content == "" {
			content = "@everyone"
		}
		author, avatar := resolveWebhookIdentity(client, data.Config)
		threadContent := data.Config.ThreadMessage

		if isFixedRounds {
			// --- FIXED ROUNDS MODE ---
			loopSys.LogInfo("[%s] Starting loop for %d rounds", data.Config.ChannelName, rounds)
			state.RoundsTotal = rounds
			for i := range rounds {
				select {
				case <-stopChan:
					return
				default:
				}
				state.CurrentRound = i + 1
				executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
			}
		} else {
			// --- RANDOM/INFINITE MODE ---
			for {
				select {
				case <-stopChan:
					return
				default:
				}

				cycleRounds := rng.Intn(100) + 1
				var delay time.Duration

				state.RoundsTotal = cycleRounds
				state.CurrentRound = 0

				for i := range cycleRounds {
					select {
					case <-stopChan:
						return
					default:
					}
					state.CurrentRound = i + 1
					executeRound(ctx, data, client, stopChan, content, threadContent, author, avatar, rng, hookBuf)
				}

				if data.Config.IsSerial {
					return
				}

				if data.Config.VoteChannelID != "" && data.Config.VoteRole != "" {
					loopSys.LogInfo("[%s] Cycle finished (%d rounds). Pausing for vote...", data.Config.ChannelName, cycleRounds)
					state.IsPaused = true
					state.NextRun = time.Time{}

					var voteChanID snowflake.ID
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
						loopSys.LogInfo("⚠️ Invalid VoteChannelID '%s', cannot pause for vote.", data.Config.VoteChannelID)
					}

					var hookID snowflake.ID
					var hookToken string

					select {
					case webhookOpSem <- struct{}{}:
						hooks, err := client.Rest.GetWebhooks(voteChanID)
						if err == nil {
							for _, h := range hooks {
								if incoming, ok := h.(discord.IncomingWebhook); ok && incoming.Token != "" {
									if incoming.Name() == LoopWebhookName {
										hookID = incoming.ID()
										hookToken = incoming.Token
										break
									}
									if hookID == 0 {
										hookID = incoming.ID()
										hookToken = incoming.Token
									}
								}
							}
						}
						select {
						case <-time.After(1 * time.Second):
						case <-ctx.Done():
							return
						}
						<-webhookOpSem
					case <-ctx.Done():
						return
					}

					if hookID == 0 {
						select {
						case webhookOpSem <- struct{}{}:
							wh, err := client.Rest.CreateWebhook(voteChanID, discord.WebhookCreate{Name: LoopWebhookName})
							if err == nil {
								hookID = wh.ID()
								hookToken = wh.Token
								if hookToken == "" {
									loopSys.LogInfo("⚠️ Created webhook for vote channel %s but could not retrieve token.", voteChanID)
								}
							} else {
								loopSys.LogInfo("⚠️ Failed to create webhook for vote channel %s: %v", voteChanID, err)
							}
							select {
							case <-time.After(2 * time.Second):
							case <-ctx.Done():
								return
							}
							<-webhookOpSem
						case <-ctx.Done():
							return
						}
					}

					if hookID != 0 {
						if state.VoteMessageID != 0 {
							_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
						}

						panelContent := data.Config.VoteMessage
						if panelContent == "" {
							panelContent = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", data.Config.ChannelName)
						}

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
						_, voteAvatar := resolveWebhookIdentity(client, data.Config)

						builder := discord.NewWebhookMessageCreate().
							WithIsComponentsV2(true).
							AddComponents(
								discord.NewContainer(
									discord.NewSection(
										discord.NewTextDisplay(panelContent),
									).WithAccessory(
										discord.NewButton(discord.ButtonStyleDanger, label, voteCustomID, "", 0),
									),
								),
							).
							WithAllowedMentions(&discord.AllowedMentions{
								Parse: []discord.AllowedMentionType{
									discord.AllowedMentionTypeEveryone,
									discord.AllowedMentionTypeRoles,
									discord.AllowedMentionTypeUsers,
								},
							}).
							WithUsername(fmt.Sprintf("Loop Finished: %s (%d rounds)", data.Config.ChannelName, cycleRounds)).
							WithAvatarURL(voteAvatar)

						if voteCh, ok := client.Caches.Channel(voteChanID); ok {
							if voteCh.Type() == discord.ChannelTypeGuildForum || voteCh.Type() == discord.ChannelTypeGuildMedia {
								builder = builder.WithThreadName(fmt.Sprintf("Loop Vote: %s", data.Config.ChannelName))
							}
						}

						msg, err := client.Rest.CreateWebhookMessage(hookID, hookToken, builder, rest.CreateWebhookMessageParams{Wait: true}, rest.WithCtx(ctx))

						if err == nil {
							state.VoteMessageID = msg.ID
						} else {
							loopSys.LogInfo("⚠️ Failed to send vote panel message to channel %s (Hook: %s): %v", voteChanID, hookID, err)
						}
					}

					select {
					case <-resumeChan:
						loopSys.LogInfo("[%s] Loop resumed by vote!", data.Config.ChannelName)
						state.IsPaused = false
						select {
						case <-time.After(3 * time.Second):
						case <-ctx.Done():
							return
						}
						if state.VoteMessageID != 0 {
							_ = client.Rest.DeleteMessage(voteChanID, state.VoteMessageID)
							state.VoteMessageID = 0
						}
					case <-stopChan:
						return
					}
				} else {
					delay = time.Duration(rng.Intn(300)+1) * time.Second
					loopSys.LogInfo("[%s] Cycle finished (%d rounds). Next cycle in %s", data.Config.ChannelName, cycleRounds, loopSys.FormatDuration(delay))
					state.NextRun = time.Now().UTC().Add(delay)
					select {
					case <-time.After(delay):
						state.NextRun = time.Time{}
					case <-stopChan:
						return
					}
				}
			}
		}

		if data.Config.IsSerial {
			loopSys.LogInfo("[%s] Serial batch finished. The queue will continue...", data.Config.ChannelName)
		} else {
			loopSys.LogInfo("[%s] Parallel batch finished.", data.Config.ChannelName)
		}
	})
}

func executeRound(ctx context.Context, data *ChannelData, client bot.Client, stopChan chan struct{}, content, threadContent, author, avatar string, rng *rand.Rand, hookBuf []WebhookData) {
	if len(hookBuf) != len(data.Hooks) {
		hookBuf = make([]WebhookData, len(data.Hooks))
	}
	copy(hookBuf, data.Hooks)

	rng.Shuffle(len(hookBuf), func(i, j int) {
		hookBuf[i], hookBuf[j] = hookBuf[j], hookBuf[i]
	})

	rate := rng.Intn(50) + 1
	delay := max(time.Second/time.Duration(rate), 20*time.Millisecond)

	var wg sync.WaitGroup
	for _, h := range hookBuf {
		select {
		case <-stopChan:
			goto Wait
		default:
		}

		workerJitter := time.Duration(rng.Intn(50)) * time.Millisecond

		wg.Add(1)
		safeGo(func() {
			func(hd WebhookData, startJitter time.Duration, stepDelay time.Duration) {
				defer wg.Done()

				select {
				case <-time.After(startJitter):
				case <-stopChan:
					return
				}

				// 1. Helper for sending with retries
				sendWithRetry := func(threadID snowflake.ID, msgContent string, wh WebhookIdentity) {
					if wh.ID == 0 {
						return
					}
					select {
					case messageSendSem <- struct{}{}:
						defer func() { <-messageSendSem }()
					case <-stopChan:
						return
					case <-ctx.Done():
						return
					}

					backoffs := []time.Duration{1 * time.Second, 2 * time.Second}
					for attempt := 0; attempt <= len(backoffs); attempt++ {
						select {
						case <-stopChan:
							return
						default:
						}

						params := rest.CreateWebhookMessageParams{Wait: false}
						if threadID != 0 {
							params.ThreadID = threadID
						}

						_, err := client.Rest.CreateWebhookMessage(wh.ID, wh.Token, discord.WebhookMessageCreate{
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

				// 2. Helper to get next webhook from pool
				getWebhook := func(idx int) WebhookIdentity {
					if len(hd.Webhooks) == 0 {
						return WebhookIdentity{}
					}
					return hd.Webhooks[idx%len(hd.Webhooks)]
				}

				// 3. Send to main channel (Synchronous - Priority)
				isStructured := hd.IsStructured
				if content != "" && !isStructured {
					sendWithRetry(0, content, getWebhook(0))
				}

				// 4. Send to threads (Parallel)
				var shardWg sync.WaitGroup
				if threadContent != "" && len(hd.ThreadIDs) > 0 {
					for i, tid := range hd.ThreadIDs {
						select {
						case <-stopChan:
							return
						default:
						}

						shardWg.Add(1)
						safeGo(func() {
							func(tID snowflake.ID, index int) {
								defer shardWg.Done()
								sendWithRetry(tID, threadContent, getWebhook(index))
							}(tid, i)
						})

						if i > 0 && i%50 == 0 {
							select {
							case <-time.After(5 * time.Millisecond):
							case <-stopChan:
								return
							case <-ctx.Done():
								return
							}
						}
					}
				}

				shardWg.Wait()
			}(h, workerJitter, delay)
		})
		select {
		case <-time.After(delay):
		case <-stopChan:
			goto Wait
		}
	}

Wait:
	wg.Wait()
}

func startNextInQueue(ctx context.Context, client bot.Client) {
	loopQueueMu.Lock()
	if len(loopQueue) == 0 {
		shuffledQueue = nil
		loopQueueMu.Unlock()
		return
	}
	if len(shuffledQueue) == 0 {
		shuffledQueue = make([]snowflake.ID, len(loopQueue))
		copy(shuffledQueue, loopQueue)
		rand.Shuffle(len(shuffledQueue), func(i, j int) { shuffledQueue[i], shuffledQueue[j] = shuffledQueue[j], shuffledQueue[i] })
	}
	nextID := shuffledQueue[0]
	shuffledQueue = shuffledQueue[1:]
	loopQueueMu.Unlock()

	dataVal, ok := configuredChannels.Load(nextID)
	if !ok {
		startNextInQueue(ctx, client)
		return
	}
	data := dataVal.(*ChannelData)

	atomic.StoreInt32(&serialActive, 1)
	loopSys.LogInfo("[%s] Starting serial loop...", data.Config.ChannelName)
	startLoopInternal(ctx, nextID, data, client, 0)
}

func SetLoopConfig(ctx context.Context, client bot.Client, channelID snowflake.ID, config *LoopConfig) error {
	if err := loopSys.SetLoopConfigDB(ctx, channelID, config); err != nil {
		return err
	}
	configuredChannels.Store(channelID, &ChannelData{Config: config})
	loopSys.LogInfo(MsgLoopConfigured, config.ChannelName)
	return nil
}

func DeleteLoopConfig(ctx context.Context, channelID snowflake.ID, client bot.Client) error {
	StopLoopInternal(ctx, channelID, client)
	configuredChannels.Delete(channelID)
	return loopSys.DeleteLoopConfigDB(ctx, channelID)
}

func GetActiveLoops() map[snowflake.ID]*LoopState {
	res := make(map[snowflake.ID]*LoopState)
	activeLoops.Range(func(k, v any) bool {
		res[k.(snowflake.ID)] = v.(*LoopState)
		return true
	})
	return res
}

func getLoopStatusDetails(cfg *LoopConfig, state *LoopState) (string, string) {
	if state == nil {
		return MsgLoopStatusStopped, ""
	}
	emoji := MsgLoopStatusRunning
	details := ""
	if cfg.Interval > 0 {
		details += fmt.Sprintf(MsgLoopStatusRound, state.CurrentRound)
	} else {
		details += fmt.Sprintf(MsgLoopStatusRoundBatch, state.CurrentRound, state.RoundsTotal)
	}
	if !state.NextRun.IsZero() {
		details += fmt.Sprintf(MsgLoopStatusNextRun, state.NextRun.Format(loopSys.DefaultTimeFormat))
	}
	return emoji, details
}

func InitLoopManager(ctx context.Context, client bot.Client) (bool, func(), func()) {
	loopSys.RegisterComponentHandler("vote:", handleVoteButton)

	var rlMu sync.Mutex
	var rlLastTrigger time.Time
	var rlCount int
	loopSys.OnRateLimitExceeded(func() {
		if atomic.LoadInt32(&isCleaningThreads) > 0 {
			return
		}

		rlMu.Lock()
		defer rlMu.Unlock()

		now := time.Now()
		if now.Sub(rlLastTrigger) > 10*time.Second {
			rlCount = 1
		} else {
			rlCount++
		}
		rlLastTrigger = now

		if rlCount >= 5 {
			loopSys.LogInfo("🛑 Loop system fail-safe triggered (5 rate limits in 10s). Stopping all active operations.")
			StopAllLoops(ctx, client)
			rlCount = 0
		} else {
			loopSys.LogInfo("⚠️ Rate limit detected (%d/5). Continuing...", rlCount)
		}
	})

	if err := loopSys.ResetAllLoopStates(ctx); err != nil {
		loopSys.LogInfo("⚠️ Failed to reset loop states: %v", err)
	}

	configs, err := loopSys.GetAllLoopConfigsDB(ctx)
	if err != nil {
		loopSys.LogInfo(MsgLoopFailedToLoadConfigs, err)
	} else {
		for _, config := range configs {
			data := &ChannelData{
				Config: config,
				Hooks:  nil,
			}
			configuredChannels.Store(config.ChannelID, data)
		}

		if len(configs) > 0 {
			loopSys.LogInfo(MsgLoopLoadedChannels, len(configs))
		}
	}

	return true, func() {
	}, func() { ShutdownLoopManager(ctx, client) }
}

func ShutdownLoopManager(ctx context.Context, client bot.Client) {
	loopSys.LogInfo("Shutting down Loop Manager...")
	StopAllLoops(ctx, client)
}
