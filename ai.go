package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
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
	MsgAIStatsTemplate       = "### AI Engine Metrics\n**Total Tokens:** %d\n**Loaded Models:** %d\n**Total Transitions (In Memory):** %d\n**Persistence:** ENABLED"
	MsgAIInvalidRegex        = "Invalid regex: %v"
	MsgAICleanHashSuccess    = "AI memory for hash `%s` has been cleared!"
	MsgAICleanContentSuccess = "AI memory for content `%s` has been cleared!"
	MsgAICleanRegexSuccess   = "AI memory for %d items matching `%s` has been cleared!"
	MsgAICleanNoMatch        = "No AI memory matched that regex."
	MsgAIGetMemoryFail       = "Failed to get AI memory: %v"
	MsgAIDumpChannel         = "Here is the AI memory dump for this channel."
	MsgAINotEnoughData       = "Not enough data to generate a response. Keep chatting!"
	MsgAIFallback            = "-# ..:."

	LogAIDumpFail          = "Error sending AI memory dump: %v"
	LogAITokensLoadFail    = "Failed to load AI tokens: %v"
	LogAIInit              = "Engine ready: %d tokens loaded"
	LogAIHistoryFetchFail  = "Error fetching history chunk (after %d scanned): %v"
	LogAISaveHistoryFail   = "Failed to save AI message from history: %v"
	LogAISaveProactiveFail = "Failed to proactively save AI message: %v"
	LogAITypingFail        = "Failed to send typing: %v"
	LogAIResponseSendFail  = "AI response send failed: %v"
	LogAIReactionAddFail   = "Failed to add response reaction %s: %v"
	LogAIUserReactionFail  = "Failed to add user reaction %s: %v"
	LogAISaveReactionFail  = "Failed to save AI reaction: %v"

	FileAITextMsgs     = "text_messages.txt"
	LabelAITextMsgs    = "Text Messages"
	FileAIStickers     = "sticker_emojis.txt"
	LabelAIStickers    = "Stickers and Emojis"
	FileAIAttachments  = "attachment_links.txt"
	LabelAIAttachments = "Attachment Links"

	DescAICommand       = "AI management commands"
	DescAICleanSub      = "Clean AI memory (granular options available)"
	DescAIHashOption    = "Delete specific hash from vocab"
	DescAIContentOption = "Delete specific content from vocab"
	DescAIRegexOption   = "Delete all vocab matching regex"
	DescAIMemorySub     = "Dump ALL AI memory"
	DescAIStatsSub      = "Show AI engine metrics"
)

func init() {
	var (
		adminPerm = discord.PermissionAdministrator
	)

	OnClientReady(func(ctx context.Context, client bot.Client) {
		RegisterDaemon("AI", LogAI, func(ctx context.Context) (bool, func(), func()) {
			return true, nil, func() {
				GlobalAI.Shutdown()
			}
		})
	})

	RegisterCommand(discord.SlashCommandCreate{
		Name:                     "ai",
		Description:              DescAICommand,
		DefaultMemberPermissions: omit.New(&adminPerm),
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "clean",
				Description: DescAICleanSub,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "hash",
						Description: DescAIHashOption,
					},
					discord.ApplicationCommandOptionString{
						Name:        "content",
						Description: DescAIContentOption,
					},
					discord.ApplicationCommandOptionString{
						Name:        "regex",
						Description: DescAIRegexOption,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "memory",
				Description: DescAIMemorySub,
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stats",
				Description: DescAIStatsSub,
			},
		},
	}, handleAI)
	GlobalAI.StartCleanup()
}

func handleAI(event *events.ApplicationCommandInteractionCreate) {
	var (
		data = event.SlashCommandInteractionData()
	)
	if data.SubCommandName == nil {
		return
	}

	switch *data.SubCommandName {
	case "clean":
		handleAIClean(event)
	case "memory":
		handleAIMemory(event)
	case "stats":
		handleAIStats(event)
	}
}

func handleAIStats(event *events.ApplicationCommandInteractionCreate) {
	var (
		tokenCount int
		modelCount int
		transCount int
		msg        string
		model      *MarkovModel
		nexts      map[int]int
	)

	GlobalAI.Markov.mu.RLock()
	tokenCount = len(GlobalAI.Markov.Tokens.forward)
	modelCount = len(GlobalAI.Markov.Models)
	for _, model = range GlobalAI.Markov.Models {
		model.mu.RLock()
		for _, nexts = range model.Transitions {
			transCount += len(nexts)
		}
		model.mu.RUnlock()
	}
	GlobalAI.Markov.mu.RUnlock()

	msg = fmt.Sprintf(MsgAIStatsTemplate,
		tokenCount, modelCount, transCount)

	_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, msg, true)
}

func handleAIClean(event *events.ApplicationCommandInteractionCreate) {
	var (
		data       = event.SlashCommandInteractionData()
		hashStr    = data.String("hash")
		contentStr = data.String("content")
		regexStr   = data.String("regex")
		ctx        = context.Background()
		err        error
		msg        string
		toDelete   []string
		hash       [32]byte
		hStr       string
		re         *regexp.Regexp
		rErr       error
		vocab      map[string]string
		vErr       error
		h          string
		c          string
	)

	if hashStr != "" {
		err = ClearAIMessagesByHashes(ctx, []string{hashStr})
		msg = fmt.Sprintf(MsgAICleanHashSuccess, hashStr)
	} else if contentStr != "" {
		hash = sha256.Sum256([]byte(contentStr))
		hStr = hex.EncodeToString(hash[:])
		err = ClearAIMessagesByHashes(ctx, []string{hStr})
		msg = fmt.Sprintf(MsgAICleanContentSuccess, contentStr)
	} else if regexStr != "" {
		re, rErr = regexp.Compile(regexStr)
		if rErr != nil {
			_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, fmt.Sprintf(MsgAIInvalidRegex, rErr), true)
			return
		}
		vocab, vErr = GetAllAIVocab(ctx)
		if vErr != nil {
			err = vErr
		} else {
			for h, c = range vocab {
				if re.MatchString(c) {
					toDelete = append(toDelete, h)
				}
			}
			if len(toDelete) > 0 {
				err = ClearAIMessagesByHashes(ctx, toDelete)
				msg = fmt.Sprintf(MsgAICleanRegexSuccess, len(toDelete), regexStr)
			} else {
				msg = MsgAICleanNoMatch
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

	GlobalAI.Markov.Reset()

	_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, msg, true)
}

func handleAIMemory(event *events.ApplicationCommandInteractionCreate) {
	var (
		dump             *AIMemoryDump
		err              error
		textBuffer       strings.Builder
		stickerBuffer    strings.Builder
		attachmentBuffer strings.Builder
		files            []*discord.File
		container        Container
		msgData          string
		sStr             string
		rStr             string
		url              string
	)

	dump, err = GetAIMemoryDump(context.Background())
	if err != nil {
		_ = event.CreateMessage(discord.NewMessageCreate().
			WithContent(fmt.Sprintf(MsgAIGetMemoryFail, err)).
			WithEphemeral(true))
		return
	}

	for _, msgData = range dump.TextMessages {
		textBuffer.WriteString(msgData + "\n")
	}

	for _, sStr = range dump.StickerIDs {
		stickerBuffer.WriteString(sStr + "\n")
	}
	for _, rStr = range dump.ReactionEmojis {
		stickerBuffer.WriteString(rStr + "\n")
	}

	for _, url = range dump.AttachmentURLs {
		attachmentBuffer.WriteString(url + "\n")
	}

	files = []*discord.File{
		discord.NewFile(FileAITextMsgs, LabelAITextMsgs, strings.NewReader(textBuffer.String())),
		discord.NewFile(FileAIStickers, LabelAIStickers, strings.NewReader(stickerBuffer.String())),
		discord.NewFile(FileAIAttachments, LabelAIAttachments, strings.NewReader(attachmentBuffer.String())),
	}

	container = NewV2Container(
		NewTextDisplay(MsgAIDumpChannel),
		NewFile("attachment://"+FileAITextMsgs, LabelAITextMsgs),
		NewFile("attachment://"+FileAIStickers, LabelAIStickers),
		NewFile("attachment://"+FileAIAttachments, LabelAIAttachments),
	)

	err = RespondInteractionContainerV2Files(*event.Client(), event.ApplicationCommandInteraction, container, files, true)
	if err != nil {
		LogError(LogAIDumpFail, err)
	}
}

// ============================================================================
// Markov-chain Logic
// ============================================================================

const (
	mrkvStartToken           = "__start"
	mrkvEndToken             = "__end"
	AICleanupInterval        = 10 * time.Minute
	AIModelTTL               = 1 * time.Hour
	TargetHumanMessages      = 100
	MaxScanDepth             = 500
	ChunkSize                = 100
	MinTransitionsToGenerate = 20
	GroupingWindow           = 60 * time.Second
	HistoryDBLimit           = 200
	AICooldownDuration       = 1 * time.Second
)

type stickerInfo struct {
	ID     snowflake.ID
	Format discord.StickerFormatType
}

var (
	keepCasePrefixes   = []string{"http:", "https:", "<a:", "<:", "<t:"}
	normalizedPrefixes = []string{"STICKER:", "REACTION:", "ATTACHMENT:", "MENTION:"}
	punctuationRegex   = regexp.MustCompile(`^([.,!?;:]+)$`)
	tokenRegex         = regexp.MustCompile(`(?i)(https?://\S+|<a?:\w+:\d+>|<t:\d+(?::[a-zA-Z])?>|<@!?[0-9]+>|<@&[0-9]+>|<#[0-9]+>|(?:STICKER|REACTION|ATTACHMENT):\S+|[:;xX8][\-~]?[DdPpsS0()\[\]\\/|]|<3|o7|[\w']+|[.,!?;:]+)`)

	GlobalAI = &AIGenerator{
		Markov:    NewMarkovManager(),
		cooldowns: make(map[snowflake.ID]time.Time),
	}
)

type AIGenerator struct {
	Markov       *MarkovManager
	cooldowns    map[snowflake.ID]time.Time
	mu           sync.RWMutex
	LoadDuration time.Duration
}

func (ai *AIGenerator) Initialize(ctx context.Context) {
	start := time.Now()
	var err error
	err = ai.Markov.Tokens.Load(ctx)
	if err != nil {
		LogError(LogAITokensLoadFail, err)
	}
	ai.LoadDuration = time.Since(start)
}

func (ai *AIGenerator) StartCleanup() {
	go func() {
		var (
			ticker = time.NewTicker(AICleanupInterval)
			now    time.Time
			cid    snowflake.ID
			last   time.Time
		)
		for range ticker.C {
			ai.mu.Lock()
			now = time.Now()
			for cid, last = range ai.cooldowns {
				if now.Sub(last) > AICooldownDuration*2 {
					delete(ai.cooldowns, cid)
				}
			}
			ai.mu.Unlock()
		}
	}()
	ai.Markov.StartCleanup()
}

func (ai *AIGenerator) Shutdown() {
	LogAI("Shutting down AI System...")
}

func (ai *AIGenerator) Train(channelID snowflake.ID, sample string) {
	ai.Markov.Train(channelID, sample)
}

func (ai *AIGenerator) IsOnCooldown(channelID snowflake.ID) bool {
	var (
		last time.Time
		ok   bool
	)
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	last, ok = ai.cooldowns[channelID]
	return ok && time.Since(last) < AICooldownDuration
}

func (ai *AIGenerator) Generate(ctx context.Context, client bot.Client, channelID snowflake.ID, begin string) (string, bool) {
	if ai.IsOnCooldown(channelID) {
		return "", false
	}

	ai.mu.Lock()
	ai.cooldowns[channelID] = time.Now()
	ai.mu.Unlock()

	return ai.Markov.Generate(ctx, client, channelID, begin), true
}

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
	var (
		id    int
		ok    bool
		lower string
	)
	tm.mu.RLock()
	id, ok = tm.forward[token]
	tm.mu.RUnlock()
	if ok {
		return id
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	if id, ok = tm.forward[token]; ok {
		return id
	}

	id = tm.nextID
	tm.nextID++
	tm.forward[token] = id
	tm.backward[id] = token
	lower = strings.ToLower(token)
	tm.variants[lower] = append(tm.variants[lower], id)

	_, _ = DB.ExecContext(context.Background(), `INSERT OR IGNORE INTO ai_tokens (id, token) VALUES (?, ?)`, id, token)

	return id
}

func (tm *TokenMap) Load(ctx context.Context) error {
	var (
		rows  *sql.Rows
		err   error
		id    int
		token string
		lower string
	)
	rows, err = DB.QueryContext(ctx, "SELECT id, token FROM ai_tokens")
	if err != nil {
		return err
	}
	defer rows.Close()

	tm.mu.Lock()
	defer tm.mu.Unlock()

	for rows.Next() {
		err = rows.Scan(&id, &token)
		if err == nil {
			tm.forward[token] = id
			tm.backward[id] = token
			lower = strings.ToLower(token)
			tm.variants[lower] = append(tm.variants[lower], id)
			if id >= tm.nextID {
				tm.nextID = id + 1
			}
		}
	}
	return nil
}

func (tm *TokenMap) FromID(id int) string {
	var (
		res string
		ok  bool
	)
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	res, ok = tm.backward[id]
	if !ok {
		return ""
	}
	return res
}

func (tm *TokenMap) GetVariants(id int) []int {
	var (
		token string
		ok    bool
		lower string
	)
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	token, ok = tm.backward[id]
	if !ok {
		return nil
	}
	lower = strings.ToLower(token)
	return append([]int{}, tm.variants[lower]...)
}

type MarkovModel struct {
	Transitions map[string]map[int]int
	LastAccess  time.Time
	channelID   snowflake.ID
	mu          sync.RWMutex
}

func NewMarkovModel(channelID snowflake.ID) *MarkovModel {
	return &MarkovModel{
		Transitions: make(map[string]map[int]int),
		LastAccess:  time.Now(),
		channelID:   channelID,
	}
}

func (m *MarkovModel) Load(ctx context.Context) error {
	var (
		rows   *sql.Rows
		err    error
		key    string
		nextID int
		weight int
		ok     bool
	)
	rows, err = DB.QueryContext(ctx, "SELECT key_text, next_id, weight FROM ai_transitions WHERE channel_id = ?", m.channelID.String())
	if err != nil {
		return err
	}
	defer rows.Close()

	m.mu.Lock()
	defer m.mu.Unlock()

	for rows.Next() {
		err = rows.Scan(&key, &nextID, &weight)
		if err == nil {
			if _, ok = m.Transitions[key]; !ok {
				m.Transitions[key] = make(map[int]int)
			}
			m.Transitions[key][nextID] = weight
		}
	}
	return nil
}

func wordProcess(word string) string {
	var (
		prefix string
	)
	for _, prefix = range normalizedPrefixes {
		if len(word) >= len(prefix) && strings.EqualFold(word[:len(prefix)], prefix) {
			return strings.ToUpper(prefix) + word[len(prefix):]
		}
	}

	for _, prefix = range keepCasePrefixes {
		if len(word) >= len(prefix) && strings.EqualFold(word[:len(prefix)], prefix) {
			if strings.HasPrefix(strings.ToLower(prefix), "http") {
				return strings.ToLower(prefix) + word[len(prefix):]
			}
			return prefix + word[len(prefix):]
		}
	}

	return word
}

func aiTokenize(content string) []string {
	var (
		matches   = tokenRegex.FindAllString(content, -1)
		processed []string
		m         string
	)

	if len(matches) == 0 {
		return nil
	}
	processed = make([]string, 0, len(matches))
	for _, m = range matches {
		if strings.HasPrefix(m, "<@") {
			processed = append(processed, "MENTION:")
			continue
		}
		processed = append(processed, wordProcess(m))
	}
	return processed
}

func (m *MarkovModel) Train(sample string, tokens *TokenMap) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		processed []string
		tokenIDs  []int
		startID   = tokens.ToID(mrkvStartToken)
		endID     = tokens.ToID(mrkvEndToken)
		window    = make([]int, GlobalConfig.AIMaxKeySize)
		key       string
		finalKey  string
		t         string
		i         int
		nextWord  int
		ok        bool
	)

	processed = aiTokenize(sample)
	if len(processed) == 0 {
		return
	}

	tokenIDs = make([]int, len(processed))
	for i, t = range processed {
		tokenIDs[i] = tokens.ToID(t)
	}

	for i = range window {
		window[i] = startID
	}

	for _, nextWord = range tokenIDs {
		key = idsToKey(window)
		if _, ok = m.Transitions[key]; !ok {
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

	finalKey = idsToKey(window)
	if _, ok = m.Transitions[finalKey]; !ok {
		m.Transitions[finalKey] = make(map[int]int)
	}
	m.Transitions[finalKey][endID]++

	_, _ = DB.ExecContext(context.Background(), `
		INSERT INTO ai_transitions (channel_id, key_text, next_id, weight)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(channel_id, key_text, next_id) DO UPDATE SET weight = excluded.weight
	`, m.channelID.String(), key, nextWord, m.Transitions[key][nextWord])
}

func idsToKey(ids []int) string {
	var (
		i   int
		id  int
		buf []byte
	)
	for i, id = range ids {
		if i > 0 {
			buf = append(buf, ' ')
		}
		buf = strconv.AppendInt(buf, int64(id), 10)
	}
	return string(buf)
}

func generateText(m *MarkovModel, maxLength int, begin string, temperature float64, tokens *TokenMap) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var (
		startID       = tokens.ToID(mrkvStartToken)
		endID         = tokens.ToID(mrkvEndToken)
		resultIDs     = make([]int, 0, maxLength)
		window        = make([]int, GlobalConfig.AIMaxKeySize)
		sb            strings.Builder
		startWords    []string
		key           string
		possibilities map[int]int
		ok            bool
		choices       map[int]int
		nextID        int
		i             int
		w             string
		id            int
		weight        int
		variantID     int
	)

	for i = range window {
		window[i] = startID
	}

	if len(begin) > 0 {
		startWords = aiTokenize(begin)
		for _, w = range startWords {
			id = tokens.ToID(w)
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
			for _, w = range startWords {
				resultIDs = append(resultIDs, tokens.ToID(w))
			}
		}
	}

	for {
		key = idsToKey(window)
		possibilities, ok = m.Transitions[key]
		if !ok || len(possibilities) == 0 {
			break
		}

		choices = make(map[int]int)
		for id, weight = range possibilities {
			choices[id] = weight
			for _, variantID = range tokens.GetVariants(id) {
				if _, ok = choices[variantID]; !ok {
					choices[variantID] = 1
				}
			}
		}

		nextID = weightedChoice(choices, temperature)
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

	for i, id = range resultIDs {
		w = tokens.FromID(id)
		if i > 0 && !punctuationRegex.MatchString(w) {
			sb.WriteString(" ")
		}
		sb.WriteString(w)
	}
	return sb.String(), nil
}

func weightedChoice(choices map[int]int, temperature float64) int {
	var (
		totalWeight      float64
		processedChoices = make(map[int]float64)
		r                float64
		runningTotal     float64
		id               int
		count            int
		weight           float64
	)

	for id, count = range choices {
		if temperature <= 0 {
			weight = float64(count)
		} else {
			weight = math.Pow(float64(count), 1.0/temperature)
		}
		processedChoices[id] = weight
		totalWeight += weight
	}

	if totalWeight <= 0 {
		for id = range choices {
			return id
		}
		return 0
	}

	r = rand.Float64() * totalWeight
	for id, weight = range processedChoices {
		runningTotal += weight
		if r <= runningTotal {
			return id
		}
	}

	for id = range choices {
		return id
	}
	return 0
}

type MarkovManager struct {
	mu     sync.RWMutex
	Models map[snowflake.ID]*MarkovModel
	Tokens *TokenMap
}

func NewMarkovManager() *MarkovManager {
	return &MarkovManager{
		Models: make(map[snowflake.ID]*MarkovModel),
		Tokens: NewTokenMap(),
	}
}

func (mm *MarkovManager) Reset() {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.Models = make(map[snowflake.ID]*MarkovModel)
	mm.Tokens = NewTokenMap()
}

func (mm *MarkovManager) ClearModelCache(channelID snowflake.ID) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	delete(mm.Models, channelID)
}

func (mm *MarkovManager) GetModel(ctx context.Context, client bot.Client, channelID snowflake.ID) (*MarkovModel, error) {
	var (
		model    *MarkovModel
		ok       bool
		newModel *MarkovModel
		samples  []string
		err      error
		s        string
	)
	mm.mu.RLock()
	model, ok = mm.Models[channelID]
	mm.mu.RUnlock()

	if ok {
		model.mu.Lock()
		model.LastAccess = time.Now()
		model.mu.Unlock()
		return model, nil
	}

	newModel = NewMarkovModel(channelID)
	err = newModel.Load(ctx)
	if err == nil && len(newModel.Transitions) > 0 {
		mm.mu.Lock()
		mm.Models[channelID] = newModel
		mm.mu.Unlock()
		return newModel, nil
	}

	samples, err = fetchHistory(ctx, client, channelID)
	if err != nil {
		return nil, err
	}

	for _, s = range samples {
		newModel.Train(s, mm.Tokens)
	}

	mm.mu.Lock()
	mm.Models[channelID] = newModel
	mm.mu.Unlock()
	return newModel, nil
}

func (mm *MarkovManager) StartCleanup() {
	go func() {
		var (
			ticker = time.NewTicker(AICleanupInterval)
		)
		for range ticker.C {
			mm.Cleanup()
		}
	}()
}

func (mm *MarkovManager) Cleanup() {
	var (
		now   time.Time
		id    snowflake.ID
		model *MarkovModel
		last  time.Time
	)
	mm.mu.Lock()
	defer mm.mu.Unlock()

	now = time.Now()
	for id, model = range mm.Models {
		model.mu.RLock()
		last = model.LastAccess
		model.mu.RUnlock()

		if now.Sub(last) > AIModelTTL {
			delete(mm.Models, id)
		}
	}
}

func (mm *MarkovManager) Train(channelID snowflake.ID, sample string) {
	var (
		model *MarkovModel
		ok    bool
	)
	mm.mu.RLock()
	model, ok = mm.Models[channelID]
	mm.mu.RUnlock()

	if ok {
		model.Train(sample, mm.Tokens)
	}
}

func (mm *MarkovManager) Generate(ctx context.Context, client bot.Client, channelID snowflake.ID, begin string) string {
	var (
		model *MarkovModel
		err   error
		temp  float64
		res   string
	)
	model, err = mm.GetModel(ctx, client, channelID)
	if err != nil {
		return ""
	}

	if len(model.Transitions) < MinTransitionsToGenerate {
		return MsgAINotEnoughData
	}

	temp = GlobalConfig.AITemperatureMin + rand.Float64()*(GlobalConfig.AITemperatureMax-GlobalConfig.AITemperatureMin)

	for range GlobalConfig.AIAttempts {
		res, err = generateText(model, GlobalConfig.AIMaxLength, begin, temp, mm.Tokens)
		if err == nil && len(res) > len(begin) {
			return res
		}
	}

	for range GlobalConfig.AIAttempts {
		res, err = generateText(model, GlobalConfig.AIMaxLength, "", temp, mm.Tokens)
		if err == nil {
			return res
		}
	}

	return ""
}

func fetchHistory(ctx context.Context, client bot.Client, channelID snowflake.ID) ([]string, error) {
	var (
		allMessages    []*AIMessageData
		seenIDs        = make(map[snowflake.ID]bool)
		humanCount     = 0
		scannedCount   = 0
		beforeID       snowflake.ID
		guildID        snowflake.ID
		groupedSamples []string
		reacts         []string
		content        string
		stickerID      string
		msgStr         string
		attachmentID   string
		attachmentURL  string
		reactions      string
		reactionList   []string
		msgGuildID     snowflake.ID
		currentGroup   string
		lastAuthor     snowflake.ID
		lastTime       time.Time
		msg            discord.Message
		msgData        *AIMessageData
		messages       []discord.Message
		err            error
		r              discord.MessageReaction
		s              string
		dbMessages     []*AIMessageData
		i              int
		ch             any
		ok             bool
		gID            interface{ GuildID() snowflake.ID }
		rStr           string
	)

	if ch, ok = client.Caches.Channel(channelID); ok {
		if gID, ok = ch.(interface{ GuildID() snowflake.ID }); ok {
			guildID = gID.GuildID()
		}
	}

	for humanCount < TargetHumanMessages && scannedCount < MaxScanDepth {
		messages, err = client.Rest.GetMessages(channelID, 0, beforeID, 0, ChunkSize)
		if err != nil {
			LogBot(LogAIHistoryFetchFail, scannedCount, err)
			break
		}

		if len(messages) == 0 {
			break
		}

		for _, msg = range messages {
			scannedCount++
			if !msg.Author.Bot && len(msg.Content) > 0 {
				content = msg.Content
				stickerID = ""
				if len(msg.StickerItems) > 0 {
					stickerID = msg.StickerItems[0].ID.String()
				}

				if strings.HasPrefix(content, "/") || strings.HasPrefix(content, "!") {
					continue
				}

				msgStr = content
				if stickerID != "" {
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "STICKER:" + stickerID
				}

				attachmentID = ""
				attachmentURL = ""
				if len(msg.Attachments) > 0 {
					attachmentID = msg.Attachments[0].ID.String()
					attachmentURL = msg.Attachments[0].URL
					if msgStr != "" {
						msgStr += " "
					}
					msgStr += "ATTACHMENT:" + attachmentURL
				}

				if msgStr != "" {
					reactions = ""
					if len(msg.Reactions) > 0 {
						reacts = nil
						for _, r = range msg.Reactions {
							s = r.Emoji.Name
							if r.Emoji.ID != 0 {
								s = fmt.Sprintf("%s:%s", r.Emoji.Name, r.Emoji.ID.String())
							}
							reacts = append(reacts, s)
						}
						reactions = strings.Join(reacts, ",")
					}
					if reactions != "" {
						reactionList = strings.Split(reactions, ",")
						for _, rStr = range reactionList {
							if rStr != "" {
								msgStr += " REACTION:" + rStr
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

					msgGuildID = guildID
					if msg.GuildID != nil {
						msgGuildID = *msg.GuildID
					}
					err = SaveAIMessage(ctx, msg.ID, msgGuildID, msg.ChannelID, content, msg.Author.ID, stickerID, reactions, attachmentID, attachmentURL)
					if err != nil {
						LogError(LogAISaveHistoryFail, err)
					}
				}
			}
		}

		beforeID = messages[len(messages)-1].ID

		if len(messages) < ChunkSize {
			break
		}
	}

	dbMessages, err = GetRecentAIMessages(ctx, channelID, HistoryDBLimit)
	if err == nil {
		for _, msgData = range dbMessages {
			if !seenIDs[msgData.MessageID] {
				allMessages = append(allMessages, msgData)
				seenIDs[msgData.MessageID] = true
			}
		}
	}

	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].CreatedAt.Before(allMessages[j].CreatedAt)
	})

	if len(allMessages) > 0 {
		currentGroup = allMessages[0].Content
		lastAuthor = allMessages[0].AuthorID
		lastTime = allMessages[0].CreatedAt
		for i = 1; i < len(allMessages); i++ {
			msgData = allMessages[i]
			if msgData.AuthorID == lastAuthor && msgData.CreatedAt.Sub(lastTime) < GroupingWindow {
				currentGroup += " " + msgData.Content
			} else {
				groupedSamples = append(groupedSamples, currentGroup)
				currentGroup = msgData.Content
			}
			lastAuthor = msgData.AuthorID
			lastTime = msgData.CreatedAt
		}
		groupedSamples = append(groupedSamples, currentGroup)
	}

	return groupedSamples, nil
}

func onMessageCreate(event *events.MessageCreate) {
	var (
		ctx              = context.Background()
		content          string
		isMentioned      bool
		isReply          bool
		author           discord.User
		s                discord.MessageSticker
		a                discord.Attachment
		guildID          snowflake.ID
		primarySticker   string
		primaryAttachID  string
		primaryAttachURL string
		err              error
	)

	if !event.Message.Author.Bot {
		content = event.Message.Content

		if (len(content) > 0 || len(event.Message.StickerItems) > 0 || len(event.Message.Attachments) > 0) && !strings.HasPrefix(content, "/") && !strings.HasPrefix(content, "!") {
			if event.GuildID != nil {
				guildID = *event.GuildID
			}

			if len(event.Message.StickerItems) > 0 {
				primarySticker = event.Message.StickerItems[0].ID.String()
			}
			if len(event.Message.Attachments) > 0 {
				primaryAttachID = event.Message.Attachments[0].ID.String()
				primaryAttachURL = event.Message.Attachments[0].URL
			}

			err = SaveAIMessage(ctx, event.Message.ID, guildID, event.ChannelID, content, event.Message.Author.ID, primarySticker, "", primaryAttachID, primaryAttachURL)
			if err != nil {
				LogError(LogAISaveProactiveFail, err)
			}

			if content != "" {
				GlobalAI.Train(event.ChannelID, content)
			}

			for _, s = range event.Message.StickerItems {
				GlobalAI.Train(event.ChannelID, fmt.Sprintf("STICKER:%s:%d", s.ID.String(), s.FormatType))
			}

			for _, a = range event.Message.Attachments {
				GlobalAI.Train(event.ChannelID, "ATTACHMENT:"+a.URL)
			}
		}
	}

	if event.Message.Author.Bot {
		return
	}

	isMentioned = false
	for _, author = range event.Message.Mentions {
		if author.ID == event.Client().ID() {
			isMentioned = true
			break
		}
	}

	isReply = false
	if event.Message.ReferencedMessage != nil && event.Message.ReferencedMessage.Author.ID == event.Client().ID() {
		isReply = true
	}

	if !isMentioned && !isReply {
		if GlobalConfig.AIRandomResponseChance <= 0 || rand.Float64() >= GlobalConfig.AIRandomResponseChance {
			return
		}
	}

	if GlobalAI.IsOnCooldown(event.ChannelID) {
		return
	}

	go generateAndSendAIResponse(ctx, event)
}

func generateAndSendAIResponse(ctx context.Context, event *events.MessageCreate) {
	var (
		begin         string
		prompt        string
		generated     string
		resp          *discord.Message
		err           error
		messageCreate discord.MessageCreate
		items         responseItems
		ok            bool
		info          stickerInfo
		ext           string
		host          string
		param         string
		stickerLink   string
		r             string
		i             int
		msgs          []discord.Message
		sIDs          []snowflake.ID
	)

	err = event.Client().Rest.SendTyping(event.ChannelID)
	if err != nil {
		LogBot(LogAITypingFail, err)
	}

	msgs, err = event.Client().Rest.GetMessages(event.ChannelID, 0, event.Message.ID, 0, 3)
	if err == nil {
		for i = len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Author.Bot {
				continue
			}
			if begin != "" {
				begin += " "
			}
			begin += msgs[i].Content
		}
	}

	prompt = strings.ReplaceAll(event.Message.Content, fmt.Sprintf("<@%s>", event.Client().ID()), "")
	prompt = strings.TrimSpace(prompt)
	if len(prompt) > 0 {
		if begin != "" {
			begin += " "
		}
		begin += prompt
	}

	generated, ok = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, begin)
	if !ok {
		return
	}
	if generated == "" && begin != "" {
		generated, _ = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, prompt)
	}
	if generated == "" {
		generated = MsgAIFallback
	}

	items = parseResponseItems(generated)
	generated = items.CleanedText

	messageCreate = discord.NewMessageCreate().
		WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID}).
		WithAllowedMentions(&discord.AllowedMentions{
			RepliedUser: items.ShouldPing,
		})

	if len(items.ImageURLs) > 0 {
		if generated == MsgAIFallback || generated == "" {
			generated = strings.Join(items.ImageURLs, "\n")
		} else {
			generated += "\n" + strings.Join(items.ImageURLs, "\n")
		}
	} else {
		if generated == "" && len(items.Stickers) == 0 {
			generated = MsgAIFallback
		}
	}

	if generated != "" {
		messageCreate = messageCreate.WithContent(generated)
	}

	if len(items.Stickers) > 0 {
		sIDs = make([]snowflake.ID, len(items.Stickers))
		for i, info = range items.Stickers {
			sIDs[i] = info.ID
		}
		messageCreate = messageCreate.WithStickers(sIDs...)
	}

	resp, err = event.Client().Rest.CreateMessage(event.ChannelID, messageCreate)
	if err != nil {
		if strings.Contains(err.Error(), "50081") {
			for _, info = range items.Stickers {
				ext = "png"
				host = "cdn.discordapp.com"
				param = ""

				switch info.Format {
				case discord.StickerFormatTypeLottie:
					ext = "json"
				case discord.StickerFormatTypeGIF:
					ext = "gif"
					host = "media.discordapp.net"
				case discord.StickerFormatTypeAPNG, discord.StickerFormatTypePNG:
					ext = "png"
				}

				stickerLink = fmt.Sprintf("https://%s/stickers/%s.%s%s", host, info.ID.String(), ext, param)
				if generated == "" || generated == MsgAIFallback {
					generated = stickerLink
				} else {
					generated += "\n" + stickerLink
				}
			}
			messageCreate = messageCreate.WithContent(generated).WithStickers()
			resp, err = event.Client().Rest.CreateMessage(event.ChannelID, messageCreate)
		}
	}

	if err != nil {
		LogError(LogAIResponseSendFail, err)
	}

	if err == nil && len(items.ReactionIDs) > 0 {
		for _, r = range items.ReactionIDs {
			err = event.Client().Rest.AddReaction(resp.ChannelID, resp.ID, r)
			if err != nil {
				LogBot(LogAIReactionAddFail, r, err)
			}
			err = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
			if err != nil {
				LogBot(LogAIUserReactionFail, r, err)
			}
		}
	}
}

func onMessageReactionAdd(event *events.MessageReactionAdd) {
	var (
		ctx      = context.Background()
		emojiStr string
		guildID  snowflake.ID
		authorID snowflake.ID
		msg      *discord.Message
		err      error
		name     string
	)

	if event.Emoji.ID != nil {
		name = ""
		if event.Emoji.Name != nil {
			name = *event.Emoji.Name
		}
		emojiStr = fmt.Sprintf("%s:%s", name, event.Emoji.ID.String())
	} else if event.Emoji.Name != nil {
		emojiStr = *event.Emoji.Name
	}

	if event.GuildID != nil {
		guildID = *event.GuildID
	}

	msg, err = event.Client().Rest.GetMessage(event.ChannelID, event.MessageID)
	if err == nil {
		authorID = msg.Author.ID
	}

	err = SaveAIMessage(ctx, event.MessageID, guildID, event.ChannelID, "", authorID, "", emojiStr, "", "")
	if err != nil {
		LogError(LogAISaveReactionFail, err)
	}

	if emojiStr != "" {
		GlobalAI.Train(event.ChannelID, "REACTION:"+emojiStr)
	}
}

type responseItems struct {
	OriginalText string
	CleanedText  string
	Stickers     []stickerInfo
	ImageURLs    []string
	ReactionIDs  []string
	ShouldPing   bool
}

func parseResponseItems(content string) responseItems {
	var (
		res          = responseItems{OriginalText: content}
		words        = strings.Fields(content)
		cleanedWords []string
		itemFound    bool
		w            string
		upperW       string
		after        string
		parts        []string
		id           snowflake.ID
		err          error
		val          string
		ok           bool
		info         stickerInfo
		fmtVal       int
		sanitized    string
	)

	for _, w = range words {
		upperW = strings.ToUpper(w)

		if strings.HasPrefix(upperW, "STICKER:") || strings.HasPrefix(upperW, "ATTACHMENT:") {
			if !itemFound {
				if strings.HasPrefix(upperW, "STICKER:") {
					after = upperW[len("STICKER:"):]
					parts = strings.Split(strings.TrimRight(after, ".,!?;:> "), ":")
					id, err = snowflake.Parse(parts[0])
					if err == nil {
						info = stickerInfo{ID: id, Format: discord.StickerFormatTypePNG}
						if len(parts) > 1 {
							fmtVal, err = strconv.Atoi(parts[1])
							if err == nil {
								info.Format = discord.StickerFormatType(fmtVal)
							}
						}
						res.Stickers = []stickerInfo{info}
						itemFound = true
					}
				} else if strings.HasPrefix(upperW, "ATTACHMENT:") {
					val = strings.TrimRight(w[len("ATTACHMENT:"):], "> ")
					if val != "" {
						res.ImageURLs = []string{val}
						itemFound = true
					}
				}
			}
			continue
		}

		after, ok = strings.CutPrefix(upperW, "REACTION:")
		if ok {
			res.ReactionIDs = append(res.ReactionIDs, after)
			continue
		}

		if upperW == "MENTION:" {
			res.ShouldPing = true
			continue
		}

		cleanedWords = append(cleanedWords, w)
	}

	sanitized = strings.Join(cleanedWords, " ")
	sanitized = strings.TrimLeft(sanitized, ".,!?;: ")
	sanitized = strings.TrimRight(sanitized, ".,!?;: ")
	if strings.Contains(sanitized, "<@") && !strings.Contains(sanitized, ">") {
		sanitized = strings.Split(sanitized, "<@")[0]
		sanitized = strings.TrimSpace(sanitized)
	}

	res.CleanedText = sanitized
	return res
}
