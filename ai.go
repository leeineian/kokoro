package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"
)

// ============================================================================
// AI Command Registration
// ============================================================================

const (
	MsgAICleanSuccess        = "AI memory for this channel has been cleared!"
	MsgAICleanFail           = "Failed to clear AI memory: %v"
	MsgAICleanAllSuccess     = "AI memory has been cleared for ALL channels!"
	MsgInvalidChannelID      = "Invalid channel ID."
	MsgAICleanChannelSuccess = "AI memory has been cleared for <#%s>!"
)

func init() {
	adminPerm := discord.PermissionAdministrator
	RegisterCommand(discord.SlashCommandCreate{
		Name:                     "ai",
		Description:              "AI management commands",
		DefaultMemberPermissions: omit.New(&adminPerm),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "clean",
				Description: "Clean AI memory (granular options available)",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "hash",
						Description: "Delete specific hash from vocab",
					},
					discord.ApplicationCommandOptionString{
						Name:        "content",
						Description: "Delete specific content from vocab",
					},
					discord.ApplicationCommandOptionString{
						Name:        "regex",
						Description: "Delete all vocab matching regex",
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "memory",
				Description: "Dump ALL AI memory",
			},
		},
	}, handleAI)
	GlobalAI.StartCleanup()
}

func handleAI(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}

	switch *data.SubCommandName {
	case "clean":
		handleAIClean(event)
	case "memory":
		handleAIMemory(event)
	}
}

func handleAIClean(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	hashStr := data.String("hash")
	contentStr := data.String("content")
	regexStr := data.String("regex")

	ctx := context.Background()
	var err error
	msg := ""

	if hashStr != "" {
		err = ClearAIMessagesByHashes(ctx, []string{hashStr})
		msg = fmt.Sprintf("AI memory for hash `%s` has been cleared!", hashStr)
	} else if contentStr != "" {
		hash := sha256.Sum256([]byte(contentStr))
		hStr := hex.EncodeToString(hash[:])
		err = ClearAIMessagesByHashes(ctx, []string{hStr})
		msg = fmt.Sprintf("AI memory for content `%s` has been cleared!", contentStr)
	} else if regexStr != "" {
		re, rErr := regexp.Compile(regexStr)
		if rErr != nil {
			_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, fmt.Sprintf("Invalid regex: %v", rErr), true)
			return
		}
		vocab, vErr := GetAllAIVocab(ctx)
		if vErr != nil {
			err = vErr
		} else {
			var toDelete []string
			for h, c := range vocab {
				if re.MatchString(c) {
					toDelete = append(toDelete, h)
				}
			}
			if len(toDelete) > 0 {
				err = ClearAIMessagesByHashes(ctx, toDelete)
				msg = fmt.Sprintf("AI memory for %d items matching `%s` has been cleared!", len(toDelete), regexStr)
			} else {
				msg = "No AI memory matched that regex."
			}
		}
	} else {
		err = ClearAllAIMessages(ctx)
		msg = MsgAICleanAllSuccess
	}

	if err != nil {
		_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, fmt.Sprintf(MsgAICleanFail, err), true)
		return
	}

	GlobalAI.mu.Lock()
	GlobalAI.Models = make(map[snowflake.ID]*MarkovModel)
	GlobalAI.Tokens = NewTokenMap()
	GlobalAI.mu.Unlock()

	_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, msg, true)
}

func handleAIMemory(event *events.ApplicationCommandInteractionCreate) {
	dump, err := GetAIMemoryDump(context.Background())
	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreate().
			WithContent(fmt.Sprintf("Failed to get AI memory: %v", err)).
			WithEphemeral(true))
		return
	}

	var textBuffer strings.Builder
	for _, msg := range dump.TextMessages {
		textBuffer.WriteString(msg + "\n")
	}

	var stickerBuffer strings.Builder
	for _, s := range dump.StickerIDs {
		stickerBuffer.WriteString(s + "\n")
	}
	for _, r := range dump.ReactionEmojis {
		stickerBuffer.WriteString(r + "\n")
	}

	var attachmentBuffer strings.Builder
	for _, url := range dump.AttachmentURLs {
		attachmentBuffer.WriteString(url + "\n")
	}

	files := []*discord.File{
		discord.NewFile("text_messages.txt", "Text Messages", strings.NewReader(textBuffer.String())),
		discord.NewFile("sticker_emojis.txt", "Stickers and Emojis", strings.NewReader(stickerBuffer.String())),
		discord.NewFile("attachment_links.txt", "Attachment Links", strings.NewReader(attachmentBuffer.String())),
	}

	container := NewV2Container(
		NewTextDisplay("Here is the AI memory dump for this channel."),
		NewFile("attachment://text_messages.txt", "Text Messages"),
		NewFile("attachment://sticker_emojis.txt", "Stickers & Emojis"),
		NewFile("attachment://attachment_links.txt", "Attachment Links"),
	)

	err = RespondInteractionContainerV2Files(*event.Client(), event.ApplicationCommandInteraction, container, files, true)
	if err != nil {
		fmt.Printf("Error sending AI memory dump: %v\n", err)
	}
}

// ============================================================================
// AI Generation Logic
// ============================================================================

const (
	mrkvStartToken    = "__start"
	mrkvEndToken      = "__end"
	AICleanupInterval = 10 * time.Minute
	AIModelTTL        = 1 * time.Hour
)

var (
	keepCasePrefixes   = []string{"http:", "https:", "<a:", "<:", "<t:"}
	normalizedPrefixes = []string{"STICKER:", "REACTION:", "ATTACHMENT:", "MENTION:"}
	punctuationRegex   = regexp.MustCompile(`^([.,!?;:]+)$`)
	tokenRegex         = regexp.MustCompile(`(?i)(https?://\S+|<a?:\w+:\d+>|<t:\d+(?::[a-zA-Z])?>|<@!?[0-9]+>|<@&[0-9]+>|<#[0-9]+>|(?:STICKER|REACTION|ATTACHMENT):\S+|[:;xX8][\-~]?[DdPpsS0()\[\]\\/|]|<3|o7|[\w']+|[.,!?;:]+)`)
)

type TokenMap struct {
	mu       sync.RWMutex
	forward  map[string]int
	backward map[int]string
	variants map[string][]int
	nextID   int
}

func NewTokenMap() *TokenMap {
	return &TokenMap{
		forward:  make(map[string]int),
		backward: make(map[int]string),
		variants: make(map[string][]int),
		nextID:   1,
	}
}

func (tm *TokenMap) ToID(token string) int {
	tm.mu.RLock()
	id, ok := tm.forward[token]
	tm.mu.RUnlock()
	if ok {
		return id
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	if id, ok := tm.forward[token]; ok {
		return id
	}

	id = tm.nextID
	tm.nextID++
	tm.forward[token] = id
	tm.backward[id] = token
	lower := strings.ToLower(token)
	tm.variants[lower] = append(tm.variants[lower], id)
	return id
}

func (tm *TokenMap) FromID(id int) string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.backward[id]
}

func (tm *TokenMap) GetVariants(id int) []int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	token, ok := tm.backward[id]
	if !ok {
		return nil
	}
	lower := strings.ToLower(token)
	return append([]int{}, tm.variants[lower]...)
}

type MarkovModel struct {
	Transitions map[string]map[int]int
	LastAccess  time.Time
	mu          sync.RWMutex
}

func NewMarkovModel() *MarkovModel {
	return &MarkovModel{
		Transitions: make(map[string]map[int]int),
		LastAccess:  time.Now(),
	}
}

func wordProcess(word string) string {
	// 1. Handle AI-specific tokens (Normalize to uppercase)
	for _, prefix := range normalizedPrefixes {
		if len(word) >= len(prefix) && strings.EqualFold(word[:len(prefix)], prefix) {
			return strings.ToUpper(prefix) + word[len(prefix):]
		}
	}

	// 2. Handle standard prefixes
	for _, prefix := range keepCasePrefixes {
		if len(word) >= len(prefix) && strings.EqualFold(word[:len(prefix)], prefix) {
			if strings.HasPrefix(strings.ToLower(prefix), "http") {
				return strings.ToLower(prefix) + word[len(prefix):]
			}
			return prefix + word[len(prefix):]
		}
	}

	// 3. Keep original case for everything else
	return word
}

func aiTokenize(content string) []string {
	matches := tokenRegex.FindAllString(content, -1)
	if len(matches) == 0 {
		return nil
	}
	processed := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasPrefix(m, "<@") {
			processed = append(processed, "MENTION:")
			continue
		}
		processed = append(processed, wordProcess(m))
	}
	return processed
}

func (m *MarkovModel) Train(sample string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	processed := aiTokenize(sample)
	if len(processed) == 0 {
		return
	}

	tokenIDs := make([]int, len(processed))
	for i, t := range processed {
		tokenIDs[i] = GlobalAI.Tokens.ToID(t)
	}

	startID := GlobalAI.Tokens.ToID(mrkvStartToken)
	endID := GlobalAI.Tokens.ToID(mrkvEndToken)

	window := make([]int, GlobalConfig.AIMaxKeySize)
	for i := range window {
		window[i] = startID
	}

	for _, nextWord := range tokenIDs {
		key := idsToKey(window)
		if _, ok := m.Transitions[key]; !ok {
			m.Transitions[key] = make(map[int]int)
		}
		m.Transitions[key][nextWord]++

		if GlobalConfig.AIMaxKeySize > 0 {
			if GlobalConfig.AIMaxKeySize > 1 {
				copy(window, window[1:])
				window[GlobalConfig.AIMaxKeySize-1] = nextWord
			} else {
				window[0] = nextWord
			}
		}
	}

	finalKey := idsToKey(window)
	if _, ok := m.Transitions[finalKey]; !ok {
		m.Transitions[finalKey] = make(map[int]int)
	}
	m.Transitions[finalKey][endID]++
}

func idsToKey(ids []int) string {
	var sb strings.Builder
	for i, id := range ids {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(fmt.Sprintf("%d", id))
	}
	return sb.String()
}

func generateText(m *MarkovModel, maxLength int, begin string, temperature float64) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	startID := GlobalAI.Tokens.ToID(mrkvStartToken)
	endID := GlobalAI.Tokens.ToID(mrkvEndToken)

	resultIDs := make([]int, 0, maxLength)
	window := make([]int, GlobalConfig.AIMaxKeySize)
	for i := range window {
		window[i] = startID
	}

	if len(begin) > 0 {
		startWords := aiTokenize(begin)
		for _, w := range startWords {
			id := GlobalAI.Tokens.ToID(w)
			if GlobalConfig.AIMaxKeySize > 0 {
				if GlobalConfig.AIMaxKeySize > 1 {
					copy(window, window[1:])
					window[GlobalConfig.AIMaxKeySize-1] = id
				} else {
					window[0] = id
				}
			}
		}

		if rand.Float64() < GlobalConfig.AISeedPrefixChance {
			for _, w := range startWords {
				resultIDs = append(resultIDs, GlobalAI.Tokens.ToID(w))
			}
		}
	}

	for {
		key := idsToKey(window)
		possibilities, ok := m.Transitions[key]
		if !ok || len(possibilities) == 0 {
			break
		}

		choices := make(map[int]int)
		for id, weight := range possibilities {
			choices[id] = weight
			for _, variantID := range GlobalAI.Tokens.GetVariants(id) {
				if _, ok := choices[variantID]; !ok {
					choices[variantID] = 1
				}
			}
		}

		nextID := weightedChoice(choices, temperature)
		if nextID == endID || nextID == 0 {
			break
		}

		resultIDs = append(resultIDs, nextID)

		if GlobalConfig.AIMaxKeySize > 0 {
			if GlobalConfig.AIMaxKeySize > 1 {
				copy(window, window[1:])
				window[GlobalConfig.AIMaxKeySize-1] = nextID
			} else {
				window[0] = nextID
			}
		}

		if len(resultIDs) > maxLength {
			break
		}
	}

	var sb strings.Builder
	for i, id := range resultIDs {
		w := GlobalAI.Tokens.FromID(id)
		if i > 0 && !punctuationRegex.MatchString(w) {
			sb.WriteString(" ")
		}
		sb.WriteString(w)
	}
	return sb.String(), nil
}

func weightedChoice(choices map[int]int, temperature float64) int {
	var totalWeight float64
	processedChoices := make(map[int]float64)

	for id, count := range choices {
		weight := math.Pow(float64(count), 1.0/temperature)
		processedChoices[id] = weight
		totalWeight += weight
	}

	r := rand.Float64() * totalWeight
	var runningTotal float64
	for id, weight := range processedChoices {
		runningTotal += weight
		if r <= runningTotal {
			return id
		}
	}

	for id := range choices {
		return id
	}
	return 0
}

// ============================================================================
// Integration Helper
// ============================================================================

type AIGenerator struct {
	mu     sync.RWMutex
	Models map[snowflake.ID]*MarkovModel
	Tokens *TokenMap
}

var GlobalAI = &AIGenerator{
	Models: make(map[snowflake.ID]*MarkovModel),
	Tokens: NewTokenMap(),
}

func (ai *AIGenerator) ClearModelCache(channelID snowflake.ID) {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	delete(ai.Models, channelID)
}

func (ai *AIGenerator) GetModel(ctx context.Context, client bot.Client, channelID snowflake.ID) (*MarkovModel, error) {
	ai.mu.RLock()
	model, ok := ai.Models[channelID]
	ai.mu.RUnlock()

	if ok {
		model.mu.Lock()
		model.LastAccess = time.Now()
		model.mu.Unlock()
		return model, nil
	}

	ai.mu.Lock()
	defer ai.mu.Unlock()

	if model, ok := ai.Models[channelID]; ok {
		model.mu.Lock()
		model.LastAccess = time.Now()
		model.mu.Unlock()
		return model, nil
	}

	newModel := NewMarkovModel()
	samples, err := fetchHistory(ctx, client, channelID)
	if err != nil {
		return nil, err
	}

	for _, s := range samples {
		newModel.Train(s)
	}

	ai.Models[channelID] = newModel
	return newModel, nil
}

func (ai *AIGenerator) StartCleanup() {
	go func() {
		ticker := time.NewTicker(AICleanupInterval)
		for range ticker.C {
			ai.Cleanup()
		}
	}()
}

func (ai *AIGenerator) Cleanup() {
	ai.mu.Lock()
	defer ai.mu.Unlock()

	now := time.Now()
	for id, model := range ai.Models {
		model.mu.RLock()
		last := model.LastAccess
		model.mu.RUnlock()

		if now.Sub(last) > AIModelTTL {
			delete(ai.Models, id)
		}
	}
}

func (ai *AIGenerator) Train(channelID snowflake.ID, sample string) {
	ai.mu.RLock()
	model, ok := ai.Models[channelID]
	ai.mu.RUnlock()

	if ok {
		model.Train(sample)
	}
}

func (ai *AIGenerator) Generate(ctx context.Context, client bot.Client, channelID snowflake.ID, begin string) string {
	model, err := ai.GetModel(ctx, client, channelID)
	if err != nil {
		return ""
	}

	if len(model.Transitions) < 20 {
		return "Not enough data to generate a response. Keep chatting!"
	}

	temp := GlobalConfig.AITemperatureMin + rand.Float64()*(GlobalConfig.AITemperatureMax-GlobalConfig.AITemperatureMin)

	for range GlobalConfig.AIAttempts {
		res, err := generateText(model, GlobalConfig.AIMaxLength, begin, temp)
		if err == nil && len(res) > len(begin) {
			return res
		}
	}

	for range GlobalConfig.AIAttempts {
		res, err := generateText(model, GlobalConfig.AIMaxLength, "", temp)
		if err == nil {
			return res
		}
	}

	return ""
}

// ============================================================================
// Event Handler
// ============================================================================

func fetchHistory(ctx context.Context, client bot.Client, channelID snowflake.ID) ([]string, error) {
	var allMessages []*AIMessageData
	seenIDs := make(map[snowflake.ID]bool)

	const (
		TargetHumanMessages = 100
		MaxScanDepth        = 500
		ChunkSize           = 100
	)

	humanCount := 0
	scannedCount := 0
	var beforeID snowflake.ID

	// 1. Fetch recent from Discord with pagination
	for humanCount < TargetHumanMessages && scannedCount < MaxScanDepth {
		messages, err := client.Rest.GetMessages(channelID, 0, beforeID, 0, ChunkSize)
		if err != nil {
			fmt.Printf("Error fetching history chunk (after %d scanned): %v\n", scannedCount, err)
			break
		}

		if len(messages) == 0 {
			break
		}

		var guildID snowflake.ID
		if ch, ok := client.Caches.Channel(channelID); ok {
			if gID, ok := ch.(interface{ GuildID() snowflake.ID }); ok {
				guildID = gID.GuildID()
			}
		}

		for _, msg := range messages {
			scannedCount++
			if !msg.Author.Bot && len(msg.Content) > 0 {
				content := msg.Content
				stickerID := ""
				if len(msg.StickerItems) > 0 {
					stickerID = msg.StickerItems[0].ID.String()
				}

				if strings.HasPrefix(content, "/") || strings.HasPrefix(content, "!") {
					continue
				}

				msgStr := content
				if stickerID != "" {
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "STICKER:" + stickerID
				}

				attachmentID := ""
				attachmentURL := ""
				if len(msg.Attachments) > 0 {
					attachmentID = msg.Attachments[0].ID.String()
					attachmentURL = msg.Attachments[0].URL
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "ATTACHMENT:" + attachmentURL
				}

				if msgStr != "" {
					reactions := ""
					if len(msg.Reactions) > 0 {
						var reacts []string
						for _, r := range msg.Reactions {
							s := r.Emoji.Name
							if r.Emoji.ID != 0 {
								s = fmt.Sprintf("%s:%s", r.Emoji.Name, r.Emoji.ID.String())
							}
							reacts = append(reacts, s)
						}
						reactions = strings.Join(reacts, ",")
					}
					if reactions != "" {
						reacts := strings.Split(reactions, ",")
						for _, r := range reacts {
							if r != "" {
								msgStr += " REACTION:" + r
							}
						}
					}

					if !seenIDs[msg.ID] {
						allMessages = append(allMessages, &AIMessageData{
							MessageID: msg.ID,
							Content:   msgStr,
							AuthorID:  msg.Author.ID,
							CreatedAt: msg.CreatedAt,
						})
						seenIDs[msg.ID] = true
						humanCount++
					}

					msgGuildID := guildID
					if msg.GuildID != nil {
						msgGuildID = *msg.GuildID
					}
					_ = SaveAIMessage(ctx, msg.ID, msgGuildID, msg.ChannelID, content, msg.Author.ID, stickerID, reactions, attachmentID, attachmentURL)
				}
			}
		}

		beforeID = messages[len(messages)-1].ID

		if len(messages) < ChunkSize {
			break
		}
	}

	// 2. Fetch older from DB to supplement
	dbMessages, err := GetRecentAIMessages(ctx, channelID, 200)
	if err == nil {
		for _, msg := range dbMessages {
			if !seenIDs[msg.MessageID] {
				allMessages = append(allMessages, msg)
				seenIDs[msg.MessageID] = true
			}
		}
	}

	// 3. Deduplicate and Sort
	// Sort Oldest -> Newest
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].CreatedAt.Before(allMessages[j].CreatedAt)
	})

	// 4. Grouping
	var groupedSamples []string
	if len(allMessages) > 0 {
		currentGroup := allMessages[0].Content
		lastAuthor := allMessages[0].AuthorID
		lastTime := allMessages[0].CreatedAt

		for i := 1; i < len(allMessages); i++ {
			msg := allMessages[i]
			if msg.AuthorID == lastAuthor && msg.CreatedAt.Sub(lastTime) < 60*time.Second {
				currentGroup += " " + msg.Content
			} else {
				groupedSamples = append(groupedSamples, currentGroup)
				currentGroup = msg.Content
			}
			lastAuthor = msg.AuthorID
			lastTime = msg.CreatedAt
		}
		groupedSamples = append(groupedSamples, currentGroup)
	}

	return groupedSamples, nil
}

func onMessageCreate(event *events.MessageCreate) {
	ctx := context.Background()

	// 1. Always save non-bot messages to DB
	if !event.Message.Author.Bot {
		content := event.Message.Content

		if (len(content) > 0 || len(event.Message.StickerItems) > 0 || len(event.Message.Attachments) > 0) && !strings.HasPrefix(content, "/") && !strings.HasPrefix(content, "!") {
			var guildID snowflake.ID
			if event.GuildID != nil {
				guildID = *event.GuildID
			}

			primarySticker := ""
			if len(event.Message.StickerItems) > 0 {
				primarySticker = event.Message.StickerItems[0].ID.String()
			}
			primaryAttachID := ""
			primaryAttachURL := ""
			if len(event.Message.Attachments) > 0 {
				primaryAttachID = event.Message.Attachments[0].ID.String()
				primaryAttachURL = event.Message.Attachments[0].URL
			}

			_ = SaveAIMessage(ctx, event.Message.ID, guildID, event.ChannelID, content, event.Message.Author.ID, primarySticker, "", primaryAttachID, primaryAttachURL)

			if content != "" {
				GlobalAI.Train(event.ChannelID, content)
			}

			for _, s := range event.Message.StickerItems {
				GlobalAI.Train(event.ChannelID, "STICKER:"+s.ID.String())
			}

			for _, a := range event.Message.Attachments {
				GlobalAI.Train(event.ChannelID, "ATTACHMENT:"+a.URL)
			}
		}
	}

	// 2. Ignore bot messages for response generation
	if event.Message.Author.Bot {
		return
	}

	// 3. Check if mentioned or replied to
	isMentioned := false
	for _, user := range event.Message.Mentions {
		if user.ID == event.Client().ID() {
			isMentioned = true
			break
		}
	}

	isReply := false
	if event.Message.ReferencedMessage != nil && event.Message.ReferencedMessage.Author.ID == event.Client().ID() {
		isReply = true
	}

	if !isMentioned && !isReply {
		// Dice roll for random interaction
		if GlobalConfig.AIRandomResponseChance <= 0 || rand.Float64() >= GlobalConfig.AIRandomResponseChance {
			return
		}
	}

	// 4. Start processing
	go func() {
		_ = event.Client().Rest.SendTyping(event.ChannelID)
		begin := ""
		prompt := strings.ReplaceAll(event.Message.Content, fmt.Sprintf("<@%s>", event.Client().ID()), "")
		prompt = strings.TrimSpace(prompt)
		if len(prompt) > 0 {
			begin = prompt
		}

		generated := GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, begin)
		if generated == "" {
			generated = "..."
		}

		var finalStickerIDs []snowflake.ID
		var finalImageURLs []string

		words := strings.Fields(generated)
		var cleanedWords []string
		itemFound := false

		for _, w := range words {
			upperW := strings.ToUpper(w)
			if !itemFound {
				if after, ok := strings.CutPrefix(upperW, "STICKER:"); ok {
					idStr := strings.TrimRight(after, ".,!?;:")
					if id, err := snowflake.Parse(idStr); err == nil {
						finalStickerIDs = append(finalStickerIDs, id)
						itemFound = true
						continue
					}
				}
				if strings.HasPrefix(upperW, "ATTACHMENT:") {
					val := w[len("ATTACHMENT:"):]
					if val != "" {
						finalImageURLs = append(finalImageURLs, val)
						itemFound = true
						continue
					}
				}
			} else {
				if strings.HasPrefix(upperW, "STICKER:") || strings.HasPrefix(upperW, "ATTACHMENT:") {
					continue
				}
			}

			if strings.HasPrefix(upperW, "REACTION:") || upperW == "MENTION:" {
				continue
			}
			cleanedWords = append(cleanedWords, w)
		}
		generated = strings.Join(cleanedWords, " ")

		var reactionIDs []string
		shouldPing := false
		for _, w := range words {
			upperW := strings.ToUpper(w)
			if after, ok := strings.CutPrefix(upperW, "REACTION:"); ok {
				reactionIDs = append(reactionIDs, after)
			}
			if upperW == "MENTION:" {
				shouldPing = true
			}
		}

		var resp *discord.Message
		var err error

		messageCreate := discord.NewMessageCreate().
			WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID}).
			WithAllowedMentions(&discord.AllowedMentions{
				RepliedUser: shouldPing,
			})

		if len(finalImageURLs) > 0 {
			if generated == "..." || generated == "" {
				generated = strings.Join(finalImageURLs, "\n")
			} else {
				generated += "\n" + strings.Join(finalImageURLs, "\n")
			}
		} else {
			if generated == "" && len(finalStickerIDs) == 0 {
				generated = "..."
			}
		}

		if generated != "" {
			messageCreate = messageCreate.WithContent(generated)
		}

		if len(finalStickerIDs) > 0 {
			messageCreate = messageCreate.WithStickers(finalStickerIDs...)
		}

		resp, err = event.Client().Rest.CreateMessage(event.ChannelID, messageCreate)
		if err != nil {
			fmt.Printf("[ERROR] AI response send failed: %v\n", err)
		}

		if err == nil && len(reactionIDs) > 0 {
			for _, r := range reactionIDs {
				_ = event.Client().Rest.AddReaction(resp.ChannelID, resp.ID, r)
				_ = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
			}
		}
	}()
}

func onMessageReactionAdd(event *events.MessageReactionAdd) {
	ctx := context.Background()
	var emojiStr string
	if event.Emoji.ID != nil {
		name := ""
		if event.Emoji.Name != nil {
			name = *event.Emoji.Name
		}
		emojiStr = fmt.Sprintf("%s:%s", name, event.Emoji.ID.String())
	} else if event.Emoji.Name != nil {
		emojiStr = *event.Emoji.Name
	}
	var guildID snowflake.ID
	if event.GuildID != nil {
		guildID = *event.GuildID
	}
	var authorID snowflake.ID
	msg, err := event.Client().Rest.GetMessage(event.ChannelID, event.MessageID)
	if err == nil {
		authorID = msg.Author.ID
	}

	_ = SaveAIMessage(ctx, event.MessageID, guildID, event.ChannelID, "", authorID, "", emojiStr, "", "")

	if emojiStr != "" {
		GlobalAI.Train(event.ChannelID, "REACTION:"+emojiStr)
	}
}
