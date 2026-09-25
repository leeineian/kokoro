package loop

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disgoorg/disgo/rest"
	"golang.org/x/time/rate"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/snowflake/v2"
)

func handleLoop(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	subCmd := *data.SubCommandName
	switch subCmd {
	case "stats":
		handleLoopStats(event)
	case "erase":
		handleLoopErase(event)
	case "set":
		handleLoopSet(event, data)
	case "start":
		handleLoopStart(event, data)
	case "stop":
		handleLoopStop(event, data)
	case "close":
		handleLoopClose(event, data)
	case "clean":
		handleLoopClean(event, data)
	default:
		log.Printf("Unknown loop subcommand: %s", subCmd)
	}
}

func handleLoopErase(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	targetID, ok := data.OptString("target")
	if !ok {
		return
	}

	_ = event.DeferCreateMessage(true)

	safeGo(func() {
		ctx := loopSys.AppContext
		client := event.Client()

		if targetID == "all" {
			configs, _ := loopSys.GetAllLoopConfigsDB(ctx)
			if len(configs) == 0 {
				loopRespond(event, MsgLoopEraseNoConfigs, true)
				return
			}
			count := 0
			for _, cfg := range configs {
				if err := DeleteLoopConfig(ctx, cfg.ChannelID, *client); err == nil {
					count++
				}
			}
			loopRespond(event, fmt.Sprintf(MsgLoopErasedBatch, count), true)
			return
		}

		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, MsgLoopErrInvalidSelection, true)
			return
		}

		cfg, err := loopSys.GetLoopConfigDB(ctx, tID)
		if err != nil || cfg == nil {
			loopRespond(event, MsgLoopErrConfigNotFound, true)
			return
		}

		name := cfg.ChannelName
		if ch, ok := client.Caches.Channel(tID); ok {
			name = ch.Name()
		}

		if err := DeleteLoopConfig(ctx, tID, *client); err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopDeleteFail, name, err), true)
		} else {
			loopRespond(event, fmt.Sprintf(MsgLoopDeleted, name), true)
		}
	})
}

func handleLoopSet(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	channelIDStr, _ := data.OptString("category")
	channelID, err := snowflake.Parse(channelIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidChannel, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	safeGo(func() {
		channel, ok := event.Client().Caches.Channel(channelID)
		if !ok {
			loopRespond(event, MsgLoopErrChannelFetchFail, true)
			return
		}

		if channel.Type() != discord.ChannelTypeGuildCategory {
			loopRespond(event, MsgLoopErrOnlyCategories, true)
			return
		}

		existing, _ := loopSys.GetLoopConfigDB(loopSys.AppContext, channelID)

		message := "@everyone"
		if msg, ok := data.OptString("message"); ok {
			message = msg
		} else if existing != nil {
			message = existing.Message
		}

		webhookAuthor := "LoopHook"
		if author, ok := data.OptString("webhook_author"); ok {
			webhookAuthor = author
		} else if existing != nil {
			webhookAuthor = existing.WebhookAuthor
		}

		webhookAvatar := ""
		if avatar, ok := data.OptString("webhook_avatar"); ok {
			webhookAvatar = avatar
		} else if existing != nil {
			webhookAvatar = existing.WebhookAvatar
		}

		threadMessage := ""
		if tmsg, ok := data.OptString("thread_message"); ok {
			threadMessage = tmsg
		} else if existing != nil {
			threadMessage = existing.ThreadMessage
		}

		threadCount := 0
		if count, ok := data.OptInt("thread_count"); ok {
			threadCount = count
		} else if existing != nil {
			threadCount = existing.ThreadCount
		}

		voteChannelID := ""
		if vc, ok := data.OptChannel("vote_channel"); ok {
			voteChannelID = vc.ID.String()
		} else if existing != nil {
			voteChannelID = existing.VoteChannelID
		}

		voteRole := ""
		if vr, ok := data.OptRole("vote_role"); ok {
			voteRole = vr.ID.String()
		} else if existing != nil {
			voteRole = existing.VoteRole
		}

		voteMessage := ""
		if vm, ok := data.OptString("vote_message"); ok {
			voteMessage = strings.ReplaceAll(vm, "\\n", "\n")
		} else if existing != nil {
			voteMessage = existing.VoteMessage
		}

		voteThreshold := 0
		if vt, ok := data.OptInt("vote_threshold"); ok {
			if vt < 0 {
				vt = 0
			}
			if vt > 100 {
				vt = 100
			}
			voteThreshold = vt
		} else if existing != nil {
			voteThreshold = existing.VoteThreshold
		}

		isSerial := false
		if isq, ok := data.OptString("queue"); ok {
			isSerial = isq == "serial"
		} else if existing != nil {
			isSerial = existing.IsSerial
		}

		config := &LoopConfig{
			ChannelID:     channelID,
			ChannelName:   channel.Name(),
			ChannelType:   resolveChannelType(channel.Type()),
			Message:       message,
			WebhookAuthor: webhookAuthor,
			WebhookAvatar: webhookAvatar,
			UseThread:     threadCount > 0,
			ThreadMessage: threadMessage,
			ThreadCount:   threadCount,
			VoteChannelID: voteChannelID,
			VoteRole:      voteRole,
			VoteMessage:   voteMessage,
			VoteThreshold: voteThreshold,
			IsSerial:      isSerial,
		}

		if err := SetLoopConfig(loopSys.AppContext, *event.Client(), channelID, config); err != nil {
			loopRespond(event, fmt.Sprintf(MsgLoopSaveFail, err), true)
			return
		}

		loopRespond(event, fmt.Sprintf(MsgLoopConfiguredDisp, channel.Name()), true)
	})
}

func handleLoopStart(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetID := ""
	if t, ok := data.OptString("target"); ok {
		targetID = t
	}
	rounds, _ := data.OptInt("rounds")

	if targetID == "all" {
		_ = event.DeferCreateMessage(true)
		safeGo(func() {
			configs, _ := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
			if len(configs) == 0 {
				_ = loopSys.EditInteractionV2(*event.Client(), event, MsgLoopErrNoChannels)
				return
			}

			var ids []snowflake.ID
			for _, cfg := range configs {
				ids = append(ids, cfg.ChannelID)
			}

			_ = BatchStartLoops(loopSys.AppContext, *event.Client(), ids, rounds)

			activeNow := GetActiveLoops()
			var startedNames []string
			for _, cfg := range configs {
				if _, ok := activeNow[cfg.ChannelID]; ok {
					name := cfg.ChannelName
					if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
						name = ch.Name()
					}
					startedNames = append(startedNames, name)
				}
			}

			msg := MsgLoopErrNoneStarted
			if len(startedNames) > 0 {
				msg = fmt.Sprintf(MsgLoopStartedBatch, len(startedNames), strings.Join(startedNames, "**, **"))
			}
			_ = loopSys.EditInteractionV2(*event.Client(), event, "> "+msg)
		})
	} else {
		tID, err := snowflake.Parse(targetID)
		if err != nil {
			loopRespond(event, MsgLoopErrInvalidSelection, true)
			return
		}
		_ = event.DeferCreateMessage(true)
		safeGo(func() {
			err = StartLoop(loopSys.AppContext, *event.Client(), tID, rounds)
			name := targetID
			if ch, ok := event.Client().Caches.Channel(tID); ok {
				name = ch.Name()
			}
			msg := fmt.Sprintf(MsgLoopStarted, name)
			if err != nil {
				msg = fmt.Sprintf(MsgLoopStartFail, name, err)
			}
			_ = loopSys.EditInteractionV2(*event.Client(), event, "> "+msg)
		})
	}
}

func handleLoopStop(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	var targetID string
	if t, ok := data.OptString("target"); ok {
		targetID = t
	}

	_ = event.DeferCreateMessage(true)

	if targetID == "all" {
		safeGo(func() {
			activeLoops := GetActiveLoops()
			if len(activeLoops) == 0 {
				loopRespond(event, MsgLoopNoRunning, true)
				return
			}

			stopped := 0
			for channelID := range activeLoops {
				if StopLoopInternal(loopSys.AppContext, channelID, *event.Client()) {
					stopped++
				}
			}

			loopRespond(event, fmt.Sprintf(MsgLoopStoppedBatch, stopped), true)
		})
	} else {
		tID, err := snowflake.Parse(targetID)
		safeGo(func() {
			if err == nil && StopLoopInternal(loopSys.AppContext, tID, *event.Client()) {
				loopRespond(event, MsgLoopStoppedDisp, true)
			} else {
				loopRespond(event, MsgLoopErrStopFail, true)
			}
		})
	}
}

func handleLoopClose(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetIDStr, _ := data.OptString("target")
	shouldDelete, _ := data.OptBool("delete")

	targetID, err := snowflake.Parse(targetIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidSelection, true)
		return
	}

	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	action := "Closed (Archived)"
	if shouldDelete {
		action = "Deleted"
	}

	safeGo(func() {
		ctx := loopSys.AppContext
		client := event.Client()
		scopeIDs, targetName := resolveScope(*client, *guildID, targetID)
		loopSys.LogInfo("[CLOSE] Final scope IDs count: %d for %s", len(scopeIDs), targetName)

		var threadsToProcess []discord.GuildThread

		var activeFeed struct {
			Threads []discord.GuildThread `json:"threads"`
		}
		activeEndpoint := rest.NewEndpoint(http.MethodGet, "/guilds/"+guildID.String()+"/threads/active")
		loopSys.LogInfo("[CLOSE] Fetching all active threads for guild %s via /threads/active...", guildID.String())
		if err := client.Rest.Do(activeEndpoint.Compile(nil), nil, &activeFeed); err == nil {
			loopSys.LogInfo("[CLOSE] Found %d total active threads in guild.", len(activeFeed.Threads))
			for _, t := range activeFeed.Threads {
				pid := t.ParentID()
				if pid != nil && scopeIDs[*pid] {
					threadsToProcess = append(threadsToProcess, t)
					loopSys.LogInfo("[CLOSE] -> MATCH found: Thread '%s' (%s) in parent %s", t.Name(), t.ID(), *pid)
				}
			}
		} else {
			loopSys.LogInfo("⚠️ [CLOSE] Failed to fetch active threads via REST: %v", err)
		}

		if shouldDelete {
			for sid := range scopeIDs {
				if sid == targetID {
					if ch, ok := client.Caches.Channel(sid); ok && ch.Type() == discord.ChannelTypeGuildCategory {
						continue
					}
				}

				archived, err := client.Rest.GetPublicArchivedThreads(sid, time.Time{}, 0)
				if err == nil {
					for _, t := range archived.Threads {
						threadsToProcess = append(threadsToProcess, t)
						loopSys.LogInfo("[CLOSE] -> MATCH (Archived) found: Thread '%s' (%s)", t.Name(), t.ID())
					}
				}
				select {
				case <-time.After(100 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
		}

		loopSys.LogInfo("[CLOSE] Total threads marked for processing: %d", len(threadsToProcess))

		limiter := rate.NewLimiter(rate.Limit(4), 10)

		count := 0
		processedIDs := make(map[snowflake.ID]bool)
		for _, t := range threadsToProcess {
			if processedIDs[t.ID()] {
				continue
			}
			processedIDs[t.ID()] = true

			if err := limiter.Wait(ctx); err != nil {
				break
			}

			if shouldDelete {
				err := client.Rest.DeleteChannel(t.ID())
				if err == nil {
					count++
				}
			} else {
				if !t.ThreadMetadata.Archived {
					_, err := client.Rest.UpdateChannel(t.ID(), discord.GuildThreadUpdate{
						Archived: boolPtr(true),
					})
					if err == nil {
						count++
					}
				}
			}
		}

		msg := fmt.Sprintf("✅ **Loop Close**: Successfully %s **%d** matching threads.", action, count)
		_ = loopSys.EditInteractionV2(*client, event, msg)
	})
}

func handleLoopStats(event *events.ApplicationCommandInteractionCreate) {
	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	configs, err := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
	if err != nil {
		loopRespond(event, MsgLoopErrRetrieveFail, true)
		return
	}

	activeLoops := GetActiveLoops()
	var guildConfigs []*LoopConfig

	for _, cfg := range configs {
		if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
			if ch.GuildID() == *guildID {
				guildConfigs = append(guildConfigs, cfg)
			}
		}
	}

	if len(guildConfigs) == 0 {
		loopRespond(event, MsgLoopErrNoGuildConfigs, true)
		return
	}

	var sb strings.Builder
	sb.WriteString(MsgLoopStatsHeader)

	for _, cfg := range guildConfigs {
		state := activeLoops[cfg.ChannelID]
		emoji, details := getLoopStatusDetails(cfg, state)
		intervalStr := loopSys.FormatDuration(loopSys.IntervalMsToDuration(cfg.Interval))

		sb.WriteString(fmt.Sprintf("%s **#%s**\n", emoji, cfg.ChannelName))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsStatus, details))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsInterval, intervalStr))
		sb.WriteString(fmt.Sprintf(MsgLoopStatsMessage, cfg.Message))
		if cfg.WebhookAuthor != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsAuthor, cfg.WebhookAuthor))
		}
		if cfg.WebhookAvatar != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsAvatar, cfg.WebhookAvatar))
		}
		if cfg.UseThread {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsThreads, cfg.ThreadCount))
			if cfg.ThreadMessage != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsThreadMsg, cfg.ThreadMessage))
			}
		}
		if cfg.VoteChannelID != "" {
			sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteChan, cfg.VoteChannelID))
			if cfg.VoteRole != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteRole, cfg.VoteRole))
			}
			if cfg.VoteMessage != "" {
				sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteMsg, cfg.VoteMessage))
			}
			sb.WriteString(fmt.Sprintf(MsgLoopStatsVoteThreshold, cfg.VoteThreshold))
		}
		queueType := "Parallel"
		if cfg.IsSerial {
			queueType = "Serial"
		}
		sb.WriteString(fmt.Sprintf(MsgLoopStatsQueue, queueType))
		sb.WriteString("\n")
	}

	loopRespond(event, sb.String(), true)
}

func handleLoopClean(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	targetIDStr, _ := data.OptString("target")
	targetID, err := snowflake.Parse(targetIDStr)
	if err != nil {
		loopRespond(event, MsgLoopErrInvalidSelection, true)
		return
	}

	guildID := event.GuildID()
	if guildID == nil {
		loopRespond(event, MsgLoopErrGuildOnly, true)
		return
	}

	_ = event.DeferCreateMessage(true)

	safeGo(func() {
		client := event.Client()
		ctx := loopSys.AppContext

		// 1. Resolve Scope (Target + Children if Category)
		scopeIDs, targetName := resolveScope(*client, *guildID, targetID)

		// 2. Identify all parents (channels where we might have threads)
		totalThreadsFound := 0
		var threadMap = make(map[snowflake.ID][]snowflake.ID) // parentID -> []threadIDs

		// A. Get Active Threads to narrow down which channels have actual bot threads
		var activeFeed struct {
			Threads []discord.GuildThread `json:"threads"`
		}
		activeEndpoint := rest.NewEndpoint(http.MethodGet, "/guilds/"+guildID.String()+"/threads/active")
		if err := client.Rest.Do(activeEndpoint.Compile(nil), nil, &activeFeed); err == nil {
			for _, t := range activeFeed.Threads {
				pid := t.ParentID()
				if pid != nil && scopeIDs[*pid] {
					// Check if it's a bot thread (generally same name as parent)
					if parent, ok := client.Caches.Channel(*pid); ok && t.Name() == parent.Name() {
						threadMap[*pid] = append(threadMap[*pid], t.ID())
						totalThreadsFound++
					}
				}
			}
		}

		if totalThreadsFound == 0 {
			msg := fmt.Sprintf("✅ **Loop Clean**: No active bot threads found in **%s**.", targetName)
			_ = loopSys.EditInteractionV2(*client, event, msg)
			return
		}

		msgStart := fmt.Sprintf("🧹 **Loop Clean**: Starting cleanup for **%d** threads in **%s**...\nThis may take a while.", totalThreadsFound, targetName)
		_ = loopSys.EditInteractionV2(*client, event, msgStart)

		// 3. Execution
		for parentID, tIDs := range threadMap {
			fastCleanThreadParticipants(ctx, *client, parentID, tIDs)
		}

		msgEnd := fmt.Sprintf("✅ **Loop Clean**: Finished cleaning **%d** matching threads in **%s**.", totalThreadsFound, targetName)
		_ = loopSys.EditInteractionV2(*client, event, msgEnd)
	})
}

func handleLoopAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	focusedOpt := ""
	for _, opt := range data.Options {
		if opt.Focused {
			if opt.Value != nil {
				focusedOpt = strings.Trim(string(opt.Value), `"`)
			}
			break
		}
	}

	subCmd := ""
	if data.SubCommandName != nil {
		subCmd = *data.SubCommandName
	}

	var choices []discord.AutocompleteChoice

	switch subCmd {
	case "start":
		configs, _ := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
		activeLoops := GetActiveLoops()
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchStartAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceStartAll, Value: "all"})
			}
		}
		for _, data := range configs {
			if ch, ok := event.Client().Caches.Channel(data.ChannelID); ok {
				if ch.GuildID() != *event.GuildID() {
					continue
				}
				displayName := ch.Name()
				intervalStr := loopSys.FormatDuration(loopSys.IntervalMsToDuration(data.Interval))
				emoji, details := getLoopStatusDetails(data, activeLoops[data.ChannelID])
				if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceStart, displayName, emoji, details, intervalStr),
						Value: data.ChannelID.String(),
					})
				}
			}
		}

	case "set":
		guildID := *event.GuildID()
		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() == guildID && ch.Type() == discord.ChannelTypeGuildCategory {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{Name: fmt.Sprintf(MsgLoopChoiceCategory, ch.Name()), Value: ch.ID().String()})
				}
			}
		}

	case "erase":
		configs, _ := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
		guildID := *event.GuildID()
		if len(configs) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchEraseAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceEraseAll, Value: "all"})
			}
		}
		activeLoops := GetActiveLoops()
		for _, cfg := range configs {
			if ch, ok := event.Client().Caches.Channel(cfg.ChannelID); ok {
				if ch.GuildID() != guildID {
					continue
				}
				displayName := ch.Name()
				intervalStr := loopSys.FormatDuration(loopSys.IntervalMsToDuration(cfg.Interval))
				emoji, details := getLoopStatusDetails(cfg, activeLoops[cfg.ChannelID])
				if focusedOpt == "" || strings.Contains(strings.ToLower(displayName), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceErase, displayName, emoji, details, intervalStr),
						Value: cfg.ChannelID.String(),
					})
				}
			}
		}

	case "stop":
		activeLoops := GetActiveLoops()
		configs, _ := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
		guildID := *event.GuildID()
		if len(activeLoops) > 1 {
			if focusedOpt == "" || strings.Contains(MsgLoopSearchStopAll, strings.ToLower(focusedOpt)) {
				choices = append(choices, discord.AutocompleteChoiceString{Name: MsgLoopChoiceStopAll, Value: "all"})
			}
		}
		for channelID, state := range activeLoops {
			if ch, ok := event.Client().Caches.Channel(channelID); ok {
				if ch.GuildID() != guildID {
					continue
				}
				name := ch.Name()
				var config *LoopConfig
				for _, cfg := range configs {
					if cfg.ChannelID == channelID {
						config = cfg
						break
					}
				}
				if config == nil {
					continue
				}
				intervalStr := loopSys.FormatDuration(loopSys.IntervalMsToDuration(config.Interval))
				emoji, details := getLoopStatusDetails(config, state)
				if focusedOpt == "" || strings.Contains(strings.ToLower(name), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf(MsgLoopChoiceStop, name, emoji, details, intervalStr),
						Value: channelID.String(),
					})
				}
			}
		}

	case "close", "clean":
		guildID := *event.GuildID()
		configs, _ := loopSys.GetAllLoopConfigsDB(loopSys.AppContext)
		configuredMap := make(map[snowflake.ID]bool)
		for _, cfg := range configs {
			configuredMap[cfg.ChannelID] = true
		}

		for ch := range event.Client().Caches.Channels() {
			if ch.GuildID() != guildID {
				continue
			}

			isTarget := false
			prefix := ""
			switch ch.Type() {
			case discord.ChannelTypeGuildCategory:
				isTarget = true
				prefix = "📁"
			case discord.ChannelTypeGuildForum, discord.ChannelTypeGuildMedia:
				isTarget = true
				prefix = "🏷️"
			default:
				if configuredMap[ch.ID()] {
					isTarget = true
					prefix = "🔄"
				}
			}

			if isTarget {
				if focusedOpt == "" || strings.Contains(strings.ToLower(ch.Name()), strings.ToLower(focusedOpt)) {
					choices = append(choices, discord.AutocompleteChoiceString{
						Name:  fmt.Sprintf("%s %s", prefix, ch.Name()),
						Value: ch.ID().String(),
					})
				}
			}
			if len(choices) >= 25 {
				break
			}
		}

	default:
	}

	if len(choices) > 25 {
		choices = choices[:25]
	}
	event.AutocompleteResult(choices)
}

func loopRespond(event *events.ApplicationCommandInteractionCreate, content string, ephemeral bool) {
	var displayContent string
	if !strings.HasPrefix(content, "#") && !strings.HasPrefix(content, ">") {
		displayContent = "> " + content
	} else {
		displayContent = content
	}

	err := loopSys.RespondInteractionV2(*event.Client(), event, displayContent, ephemeral)
	if err != nil {
		_ = loopSys.EditInteractionV2(*event.Client(), event, displayContent)
	}
}

func handleVoteButton(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	parts := strings.Split(customID, ":")
	if len(parts) < 2 {
		return
	}
	channelID, _ := snowflake.Parse(parts[1])
	stateVal, ok := activeLoops.Load(channelID)
	if !ok {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("⚠️ Loop is no longer active.").WithEphemeral(true))
		return
	}
	state := stateVal.(*LoopState)
	dataVal, ok := configuredChannels.Load(channelID)
	if !ok {
		return
	}
	cfg := dataVal.(*ChannelData).Config
	if !state.IsPaused || state.VoteMessageID != event.Message.ID {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("⚠️ This vote panel is no longer valid.").WithEphemeral(true))
		return
	}

	hasRole := false
	reqRoleID, _ := snowflake.Parse(cfg.VoteRole)
	for _, rid := range event.Member().RoleIDs {
		if rid == reqRoleID {
			hasRole = true
			break
		}
	}
	if !hasRole {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("⚠️ You do not have the required role to vote.").WithEphemeral(true))
		return
	}

	if state.Votes == nil {
		state.Votes = make(map[snowflake.ID]struct{})
	}
	if _, voted := state.Votes[event.User().ID]; voted {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("⚠️ You have already voted!").WithEphemeral(true))
		return
	}
	state.Votes[event.User().ID] = struct{}{}

	valid := len(state.Votes)
	needed := state.NeededVotes
	if needed == 0 {
		needed = 1
	}

	style := discord.ButtonStyleDanger
	if valid >= needed {
		style = discord.ButtonStyleSuccess
	}

	panel := cfg.VoteMessage
	if panel == "" {
		panel = fmt.Sprintf("⏸️ **Loop Paused**\nTo resume **%s**, click the button below!", cfg.ChannelName)
	}

	update := discord.NewMessageUpdate().WithIsComponentsV2(true).WithComponents(
		discord.NewContainer(discord.NewSection(discord.NewTextDisplay(panel)).WithAccessory(discord.NewButton(style, formatVoteLabel(valid, needed), customID, "", 0))),
	)
	_ = event.UpdateMessage(update)

	if valid >= needed {
		select {
		case state.ResumeChan <- struct{}{}:
		default:
		}
	}
}
