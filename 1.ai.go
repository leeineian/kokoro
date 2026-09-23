package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
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
	"google.golang.org/genai"
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
	MsgAINotEnoughData       = "Not enough data to generate a response. Keep chatting!"
	MsgAIFallback            = "-# ..."

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
	FileAIStickers     = "stickers_emojis.txt"
	LabelAIStickers    = "Stickers and Emojis"
	FileAIAttachments  = "attachment_links.txt"
	LabelAIAttachments = "Attachment Links"

	DescAICommand       = "AI management commands"
	DescAISwitchSub     = "Switch the AI model for this channel"
	DescAISwitchModel   = "The model to use"
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
				Name:        "switch",
				Description: DescAISwitchSub,
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "model",
						Description:  DescAISwitchModel,
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "global",
						Description: "Set this model for all channels",
						Required:    false,
					},
				},
			},
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
	RegisterAutocompleteHandler("ai", handleAIAutocomplete)
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
	case "switch":
		handleAISwitch(event)
	case "clean":
		handleAIClean(event)
	case "memory":
		handleAIMemory(event)
	case "stats":
		handleAIStats(event)
	}
}

func handleAIAutocomplete(event *events.AutocompleteInteractionCreate) {
	data := event.Data
	if data.SubCommandName == nil || *data.SubCommandName != "switch" {
		return
	}

	current := strings.ToLower(data.String("model"))
	choices := []discord.AutocompleteChoice{
		discord.AutocompleteChoiceString{Name: "Markov Chain", Value: "markov-chain"},
	}

	choices = append(choices, discord.AutocompleteChoiceString{
		Name:  "Google: Gemini 1.5 Flash (Default)",
		Value: GlobalConfig.AIModel,
	})

	puterModelsMu.RLock()
	for _, model := range puterModels {
		if model.ID == "markov-chain" || model.ID == GlobalConfig.AIModel {
			continue
		}

		if current == "" ||
			strings.Contains(strings.ToLower(model.Name), current) ||
			strings.Contains(strings.ToLower(model.ID), current) {
			choices = append(choices, discord.AutocompleteChoiceString{
				Name:  model.Name,
				Value: model.ID,
			})
		}
	}
	puterModelsMu.RUnlock()

	if len(choices) > 25 {
		choices = choices[:25]
	}

	_ = event.AutocompleteResult(choices)
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

	seenText := make(map[string]struct{})
	for _, msgData = range dump.TextMessages {
		if _, ok := seenText[msgData]; !ok {
			textBuffer.WriteString(msgData + "\n")
			seenText[msgData] = struct{}{}
		}
	}

	seenStickers := make(map[string]struct{})
	for _, sStr = range dump.StickerIDs {
		if _, ok := seenStickers[sStr]; !ok {
			stickerBuffer.WriteString(sStr + "\n")
			seenStickers[sStr] = struct{}{}
		}
	}
	for _, rStr = range dump.ReactionEmojis {
		if _, ok := seenStickers[rStr]; !ok {
			stickerBuffer.WriteString(rStr + "\n")
			seenStickers[rStr] = struct{}{}
		}
	}

	seenAttachments := make(map[string]struct{})
	for _, url = range dump.AttachmentURLs {
		if _, ok := seenAttachments[url]; !ok {
			attachmentBuffer.WriteString(url + "\n")
			seenAttachments[url] = struct{}{}
		}
	}

	files = []*discord.File{
		discord.NewFile(FileAITextMsgs, LabelAITextMsgs, strings.NewReader(textBuffer.String())),
		discord.NewFile(FileAIStickers, LabelAIStickers, strings.NewReader(stickerBuffer.String())),
		discord.NewFile(FileAIAttachments, LabelAIAttachments, strings.NewReader(attachmentBuffer.String())),
	}

	err = RespondInteractionFiles(*event.Client(), event.ApplicationCommandInteraction, "", files, true)
	if err != nil {
		LogError(LogAIDumpFail, err)
	}
}

func handleAISwitch(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	model := data.String("model")
	global, _ := data.OptBool("global")

	key := ""
	channelID := event.Channel().ID().String()

	if global {
		key = "ai_model:global"
	} else {
		key = fmt.Sprintf("ai_model:%s", channelID)
	}

	err := SetBotConfig(context.Background(), key, model)
	if err != nil {
		_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, fmt.Sprintf("Failed to switch model: %v", err), true)
		return
	}

	resp := fmt.Sprintf("AI model switched to **%s** for this channel.", model)
	if global {
		resp = fmt.Sprintf("Global AI model switched to **%s**.", model)
	}
	_ = RespondInteractionV2(*event.Client(), event.ApplicationCommandInteraction, resp, false)
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
	normalizedPrefixes = []string{"STICKER:", "REACTION:", "ATTACHMENT:", "MENTION:", "REPLY:"}
	punctuationRegex   = regexp.MustCompile(`^([.,!?;:]+)$`)
	tokenRegex         = regexp.MustCompile(`(?i)(https?://\S+|<a?:\w+:\d+>|<t:\d+(?::[a-zA-Z])?>|<@!?[0-9]+>|<@&[0-9]+>|<#[0-9]+>|(?:STICKER|REACTION|ATTACHMENT|MENTION|REPLY):\S*|[:;xX8][\-~]?[DdPpsS0()\[\]\\/|]|<3|o7|[\w']+|[.,!?;:]+)`)

	GlobalAI = &AIGenerator{
		Markov:    NewMarkovManager(),
		cooldowns: make(map[snowflake.ID]time.Time),
	}
)

type AIGenerator struct {
	Markov       *MarkovManager
	GenAIClient  *genai.Client
	PuterClient  *PuterClient
	ExaClient    *ExaClient
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

	ai.GenAIClient, err = genai.NewClient(ctx, nil)
	if err != nil {
		LogError("Failed to initialize GenAI client: %v", err)
	} else {
		LogAI("GenAI client initialized successfully")
	}

	if GlobalConfig.PuterToken != "" {
		ai.PuterClient = NewPuterClient(GlobalConfig.PuterToken)
		LogAI("Puter client initialized")
	}

	if GlobalConfig.ExaApiKey != "" {
		ai.ExaClient = NewExaClient(GlobalConfig.ExaApiKey)
		LogAI("Exa client initialized")
	}

	ai.LoadDuration = time.Since(start)

	StartModelRefresh(ctx)
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

func (ai *AIGenerator) Generate(ctx context.Context, client bot.Client, channelID snowflake.ID, begin string, yield func(string)) (string, bool) {
	if ai.IsOnCooldown(channelID) {
		return "", false
	}

	ai.mu.Lock()
	ai.cooldowns[channelID] = time.Now()
	ai.mu.Unlock()

	key := fmt.Sprintf("ai_model:%s", channelID)
	modelName, _ := GetBotConfig(ctx, key)

	if modelName == "" {
		modelName, _ = GetBotConfig(ctx, "ai_model:global")
	}

	if modelName == "" && GlobalConfig.AIModel != "" {
		modelName = GlobalConfig.AIModel
	}

	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	prompt := begin

	if GlobalConfig.AIPrompt != "" {
		prompt = GlobalConfig.AIPrompt + "\n\n" + prompt
	}

	if ai.PuterClient != nil && modelName != "markov-chain" {
		puterPrompt := prompt
		if ai.ExaClient != nil {
			LogAI("Searching Exa for: %s", begin)
			if results, err := ai.ExaClient.Search(begin); err == nil && len(results) > 0 {
				puterPrompt += "\n\nSearch Results:\n"
				count := 0
				for _, res := range results {
					if count >= 10 {
						break
					}
					puterPrompt += fmt.Sprintf("%d. [%s](%s): %s\n", count+1, res.Title, res.URL, res.Text)
					count++
				}
				LogAI("Exa search found %d results", len(results))
			} else if err != nil {
				LogAI("Exa search failed: %v", err)
			}
		}

		modelToUse := modelName
		if puterResp, pErr := ai.PuterClient.Generate(puterPrompt, modelToUse); pErr == nil && puterResp != "" {
			return puterResp, true
		} else {
			LogAI("Puter generation failed: %v. Falling back to Gemini...", pErr)
		}
	}

	if ai.GenAIClient != nil && modelName != "markov-chain" {
		var err error
		maxRetries := 3
		backoff := 500 * time.Millisecond

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return "", false
				case <-time.After(backoff):
				}
				backoff *= 2
				LogAI("Retrying GenAI generation... attempt %d", attempt)
			}

			currentText := ""
			streamFailed := false

			var tools []*genai.Tool
			if strings.Contains(modelName, "gemini") {
				tools = []*genai.Tool{
					{
						GoogleSearch: &genai.GoogleSearch{},
					},
				}
			}

			temp32 := float32(GlobalConfig.AITemperatureMin)

			config := &genai.GenerateContentConfig{
				Tools:       tools,
				Temperature: &temp32,
			}

			iter := ai.GenAIClient.Models.GenerateContentStream(ctx, modelName, genai.Text(prompt), config)

			for resp, iterErr := range iter {
				if iterErr != nil {
					LogAI("GenAI streaming error (attempt %d): %v", attempt, iterErr)
					streamFailed = true
					err = iterErr
					break
				}
				if resp != nil {
					chunk := resp.Text()
					currentText += chunk
					if yield != nil {
						yield(chunk)
					}
				}
			}

			if !streamFailed {
				return currentText, true
			}

			if currentText != "" {
				return currentText, true
			}
		}
		LogError("GenAI failed fully: %v. Falling back to Markov.", err)
	}

	result := ai.Markov.Generate(ctx, client, channelID, begin)
	return result, true
}

// ============================================================================
// Puter Client Implementation
// ============================================================================

type PuterClient struct {
	Token  string
	Client *http.Client
}

type PuterRequest struct {
	Interface string    `json:"interface"`
	Driver    string    `json:"driver"`
	Method    string    `json:"method"`
	Args      PuterArgs `json:"args"`
}

type PuterArgs struct {
	Messages []PuterMessage `json:"messages"`
	Model    string         `json:"model"`
	Stream   bool           `json:"stream"`
}

type PuterMessage struct {
	Content string `json:"content"`
}

type PuterResponse struct {
	Result PuterResult `json:"result,omitempty"`
}

type PuterResult struct {
	Message PuterMessage `json:"message"`
}

func NewPuterClient(token string) *PuterClient {
	return &PuterClient{
		Token: token,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *PuterClient) Generate(prompt, model string) (string, error) {
	if c.Token == "" {
		return "", fmt.Errorf("Puter token is missing")
	}

	payload := PuterRequest{
		Interface: "puter-chat-completion",
		Driver:    "ai-chat",
		Method:    "complete",
		Args: PuterArgs{
			Messages: []PuterMessage{{Content: prompt}},
			Model:    model,
			Stream:   false,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.puter.com/drivers/call", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("request creation error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("User-Agent", "puter-js/1.0")
	req.Header.Set("Origin", "https://puter.work")
	req.Header.Set("Referer", "https://puter.work/")

	resp, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request execution error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var puterResp PuterResponse
	if err := json.Unmarshal(body, &puterResp); err != nil {
		return "", fmt.Errorf("unmarshal error: %w body=%s", err, string(body))
	}

	if puterResp.Result.Message.Content == "" {
		return "", fmt.Errorf("empty response content: %s", string(body))
	}

	return puterResp.Result.Message.Content, nil
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

	tx, err := DB.BeginTx(context.Background(), nil)
	if err != nil {
		return
	}
	defer tx.Rollback()

	for _, nextWord = range tokenIDs {
		key = idsToKey(window)
		if _, ok = m.Transitions[key]; !ok {
			m.Transitions[key] = make(map[int]int)
		}
		m.Transitions[key][nextWord]++

		_, _ = tx.ExecContext(context.Background(), `
			INSERT INTO ai_transitions (channel_id, key_text, next_id, weight)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(channel_id, key_text, next_id) DO UPDATE SET weight = excluded.weight
		`, m.channelID.String(), key, nextWord, m.Transitions[key][nextWord])

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

	_, _ = tx.ExecContext(context.Background(), `
		INSERT INTO ai_transitions (channel_id, key_text, next_id, weight)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(channel_id, key_text, next_id) DO UPDATE SET weight = excluded.weight
	`, m.channelID.String(), finalKey, endID, m.Transitions[finalKey][endID])

	_ = tx.Commit()
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

					if msg.ReferencedMessage != nil {
						msgStr = "REPLY: " + msgStr
					}

					if strings.Contains(msg.Content, fmt.Sprintf("<@%s>", client.ID())) || strings.Contains(msg.Content, fmt.Sprintf("<@!%s>", client.ID())) {
						if !strings.HasPrefix(msgStr, "REPLY:") {
							msgStr = "MENTION: " + msgStr
						}
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

			trainStr := content
			if event.Message.ReferencedMessage != nil {
				trainStr = "REPLY: " + trainStr
			}
			if isMentioned {
				if !strings.HasPrefix(trainStr, "REPLY:") {
					trainStr = "MENTION: " + trainStr
				}
			}

			for _, s = range event.Message.StickerItems {
				if trainStr != "" {
					trainStr += " "
				}
				trainStr += fmt.Sprintf("STICKER:%s:%d", s.ID.String(), s.FormatType)
			}
			for _, a = range event.Message.Attachments {
				if trainStr != "" {
					trainStr += " "
				}
				trainStr += "ATTACHMENT:" + a.URL
			}

			if trainStr != "" {
				GlobalAI.Train(event.ChannelID, trainStr)
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
		begin       string
		prompt      string
		generated   string
		resp        *discord.Message
		err         error
		items       responseItems
		ok          bool
		cleaned     string
		info        stickerInfo
		ext         string
		host        string
		param       string
		stickerLink string
		r           string

		sIDs []snowflake.ID
	)

	err = event.Client().Rest.SendTyping(event.ChannelID)
	if err != nil {
		LogBot(LogAITypingFail, err)
	}

	typingDone := make(chan bool)
	safeGo(func() {
		ticker := time.NewTicker(8 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = event.Client().Rest.SendTyping(event.ChannelID)
			case <-typingDone:
				return
			}
		}
	})
	defer func() { close(typingDone) }()

	prompt = strings.ReplaceAll(event.Message.Content, fmt.Sprintf("<@%s>", event.Client().ID()), "")
	prompt = strings.TrimSpace(prompt)
	begin = prompt

	for i := 0; i < 3; i++ {
		seed := begin
		if i == 1 {
			seed = ""
		} else if i == 2 {
			words := strings.Fields(begin)
			if len(words) > 0 {
				seed = words[len(words)-1]
			} else {
				seed = ""
			}
		}

		generated, ok = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, seed, nil)
		if !ok {
			return
		}
		if generated == "" && seed != "" {
			generated, _ = GlobalAI.Generate(ctx, *event.Client(), event.ChannelID, "", nil)
		}
		if generated == "" {
			generated = MsgAIFallback
		}

		items = parseResponseItems(generated)
		cleaned = items.CleanedText

		if cleaned != "" || len(items.Stickers) > 0 || len(items.ImageURLs) > 0 || len(items.ReactionIDs) > 0 {
			break
		}
		LogAI("Empty cleaned text generated, retrying (%s) with seed '%s'...", generated, seed)
	}

	if cleaned == "" && len(items.Stickers) == 0 && len(items.ImageURLs) == 0 && len(items.ReactionIDs) > 0 {
		for _, r = range items.ReactionIDs {
			_ = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
		}
		return
	}

	if cleaned == "" && len(items.Stickers) == 0 && len(items.ImageURLs) == 0 {
		cleaned = MsgAIFallback
	}

	if items.ShouldPing && !items.ShouldReply {
		cleaned = fmt.Sprintf("<@%s> %s", event.Message.Author.ID.String(), cleaned)
	}

	chunks := SplitMessage(cleaned, 2000)

	for i, chunk := range chunks {
		isLast := i == len(chunks)-1

		msgCreate := discord.NewMessageCreate()

		if items.ShouldReply {
			msgCreate = msgCreate.WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID})
		}

		shouldPing := items.ShouldPing && i == 0
		msgCreate = msgCreate.WithAllowedMentions(&discord.AllowedMentions{
			RepliedUser: shouldPing,
		})

		msgCreate = msgCreate.WithContent(chunk)

		if isLast && len(items.Stickers) > 0 {
			sIDs = make([]snowflake.ID, len(items.Stickers))
			for j, info := range items.Stickers {
				sIDs[j] = info.ID
			}
			msgCreate = msgCreate.WithStickers(sIDs...)
		}

		resp, err = event.Client().Rest.CreateMessage(event.ChannelID, msgCreate)
		if err != nil {
			if strings.Contains(err.Error(), "50081") && isLast {
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
					_, _ = event.Client().Rest.CreateMessage(event.ChannelID, discord.NewMessageCreate().
						WithMessageReference(&discord.MessageReference{MessageID: &event.MessageID}).
						WithContent(stickerLink))
				}
			} else {
				LogError(LogAIResponseSendFail, err)
			}
		}

		if isLast && err == nil && len(items.ReactionIDs) > 0 {
			for _, r = range items.ReactionIDs {
				_ = event.Client().Rest.AddReaction(resp.ChannelID, resp.ID, r)
				_ = event.Client().Rest.AddReaction(event.ChannelID, event.MessageID, r)
			}
		}
	}
}

func SplitMessage(content string, limit int) []string {
	if len(content) <= limit {
		return []string{content}
	}
	var chunks []string
	for len(content) > limit {
		splitAt := -1
		if val := strings.LastIndex(content[:limit], "\n"); val != -1 {
			splitAt = val
		} else if val := strings.LastIndex(content[:limit], " "); val != -1 {
			splitAt = val
		} else {
			splitAt = limit
		}

		chunk := content[:splitAt]
		chunks = append(chunks, chunk)
		content = content[splitAt:]
		content = strings.TrimPrefix(content, "\n")
		content = strings.TrimPrefix(content, " ")
	}
	if len(content) > 0 {
		chunks = append(chunks, content)
	}
	return chunks
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
	ShouldReply  bool
}

func parseResponseItems(content string) responseItems {
	res := responseItems{OriginalText: content}
	cleaned := content

	// 1. Extract MENTION:
	if strings.Contains(strings.ToUpper(cleaned), "MENTION:") {
		res.ShouldPing = true
		cleaned = regexp.MustCompile(`(?i)MENTION:`).ReplaceAllString(cleaned, "")
	}

	// 2. Extract REPLY:
	if strings.Contains(strings.ToUpper(cleaned), "REPLY:") {
		res.ShouldReply = true
		cleaned = regexp.MustCompile(`(?i)REPLY:`).ReplaceAllString(cleaned, "")
	}

	// 3. Extract STICKER:ID:FORMAT
	stickerRegex := regexp.MustCompile(`(?i)STICKER:([\w-]+)(?::(\d+))?`)
	matches := stickerRegex.FindAllStringSubmatch(cleaned, 1)
	if len(matches) > 0 {
		id, err := snowflake.Parse(matches[0][1])
		if err == nil {
			info := stickerInfo{ID: id, Format: discord.StickerFormatTypePNG}
			if matches[0][2] != "" {
				fmtVal, _ := strconv.Atoi(matches[0][2])
				info.Format = discord.StickerFormatType(fmtVal)
			}
			res.Stickers = []stickerInfo{info}
		}
		cleaned = stickerRegex.ReplaceAllString(cleaned, "")
	}

	// 4. Extract ATTACHMENT:URL
	attachmentRegex := regexp.MustCompile(`(?i)ATTACHMENT:([^\s>]+)`)
	attachmentMatches := attachmentRegex.FindAllStringSubmatch(cleaned, -1)
	for _, m := range attachmentMatches {
		res.ImageURLs = append(res.ImageURLs, m[1])
	}
	cleaned = attachmentRegex.ReplaceAllString(cleaned, "")

	// 5. Extract REACTION:EMOJI
	reactionRegex := regexp.MustCompile(`(?i)REACTION:([^\s]+)`)
	reactionMatches := reactionRegex.FindAllStringSubmatch(cleaned, -1)
	for _, m := range reactionMatches {
		res.ReactionIDs = append(res.ReactionIDs, m[1])
	}
	cleaned = reactionRegex.ReplaceAllString(cleaned, "")

	// Final cleanup
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimLeft(cleaned, ".,!?;: ")
	cleaned = strings.TrimRight(cleaned, ".,!?;: ")

	// --- Truncation Guards ---

	// 1. Triple backticks - always trim unclosed (they break entire channels)
	if strings.Count(cleaned, "```")%2 != 0 {
		idx := strings.LastIndex(cleaned, "```")
		if idx != -1 {
			cleaned = cleaned[:idx]
		}
	}

	// 2. Trailing Partial Symbols: "Hello *" -> "Hello"
	for {
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			break
		}
		lastChar := cleaned[len(cleaned)-1]
		if strings.ContainsRune("*_~`[(:-,", rune(lastChar)) {
			cleaned = cleaned[:len(cleaned)-1]
			continue
		}
		break
	}

	// 3. Partial Bot Tokens: "See STICKER:" -> "See"
	tokenSuffixes := []string{"STICKER:", "ATTACHMENT:", "REACTION:", "MENTION:", "REPLY:"}
	for _, t := range tokenSuffixes {
		upperCleaned := strings.ToUpper(cleaned)
		stem := strings.TrimSuffix(t, ":")
		if strings.HasSuffix(upperCleaned, stem) || strings.HasSuffix(upperCleaned, t) {
			idx := strings.LastIndex(upperCleaned, stem)
			if idx != -1 {
				cleaned = cleaned[:idx]
			}
		}
	}

	// 4. Partial Numbered Lists: "Recipe 12." -> "Recipe"
	numberRegex := regexp.MustCompile(`\n?\s*\d+\.$`)
	cleaned = numberRegex.ReplaceAllString(cleaned, "")

	// 5. Partial Mentions: "Hello <@123" -> "Hello"
	if strings.Contains(cleaned, "<@") {
		lastOpenIdx := strings.LastIndex(cleaned, "<@")
		lastCloseIdx := strings.LastIndex(cleaned, ">")
		if lastOpenIdx > lastCloseIdx {
			cleaned = cleaned[:lastOpenIdx]
		}
	}

	// 6. Partial Markdown Links: "[Text](http..." -> "[Text]"
	if strings.Contains(cleaned, "[") {
		lastOpenBracket := strings.LastIndex(cleaned, "[")
		lastCloseBracket := strings.LastIndex(cleaned, "]")
		lastOpenParen := strings.LastIndex(cleaned, "(")
		lastCloseParen := strings.LastIndex(cleaned, ")")

		if lastOpenParen > lastCloseBracket && lastOpenParen > lastCloseParen {
			cleaned = cleaned[:lastOpenParen]
		}
		if lastOpenBracket > lastCloseBracket {
			cleaned = cleaned[:lastOpenBracket]
		}
	}

	// 7. Unclosed Bold/Italic on the LAST line only
	// (Prevents stripping bullets from early lines in the message)
	lines := strings.Split(cleaned, "\n")
	if len(lines) > 0 {
		lastLine := lines[len(lines)-1]
		for _, wrap := range []string{"***", "**", "*", "__", "_", "`"} {
			if strings.Count(lastLine, wrap)%2 != 0 {
				// Special check: ignore if it looks like a bullet (* Bullet)
				trimmed := strings.TrimSpace(lastLine)
				if (wrap == "*" || wrap == "_" || wrap == "-") &&
					(strings.HasPrefix(trimmed, wrap+" ") || strings.HasPrefix(trimmed, wrap+"\t")) {
					continue
				}

				idx := strings.LastIndex(lastLine, wrap)
				if idx != -1 {
					lastLine = lastLine[:idx]
				}
			}
		}
		lines[len(lines)-1] = lastLine
		cleaned = strings.Join(lines, "\n")
	}

	cleaned = strings.TrimSpace(cleaned)
	res.CleanedText = cleaned
	return res
}

type ExaClient struct {
	ApiKey string
	Client *http.Client
}

type ExaRequest struct {
	Query         string      `json:"query"`
	Type          string      `json:"type"`
	UseAutoprompt bool        `json:"useAutoprompt"`
	Contents      ExaContents `json:"contents"`
	NumResults    int         `json:"numResults,omitempty"`
}

type ExaContents struct {
	Text bool `json:"text"`
}

type ExaResponse struct {
	Results []ExaResult `json:"results"`
}

type ExaResult struct {
	Title string  `json:"title"`
	URL   string  `json:"url"`
	Text  string  `json:"text"`
	Score float64 `json:"score"`
}

func NewExaClient(apiKey string) *ExaClient {
	return &ExaClient{
		ApiKey: apiKey,
		Client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *ExaClient) Search(query string) ([]ExaResult, error) {
	if c.ApiKey == "" {
		return nil, fmt.Errorf("Exa API key is missing")
	}

	payload := ExaRequest{
		Query:         query,
		Type:          "neural",
		UseAutoprompt: true,
		NumResults:    10,
		Contents: ExaContents{
			Text: true,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.exa.ai/search", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("request creation error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.ApiKey)
	req.Header.Set("accept", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request execution error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body error: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var exaResp ExaResponse
	if err := json.Unmarshal(body, &exaResp); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w body=%s", err, string(body))
	}

	return exaResp.Results, nil
}

type AIModelInfo struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

var (
	puterModels   []AIModelInfo
	puterModelsMu sync.RWMutex
)

func FetchPuterModels(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://puter.com/puterai/chat/models/details", nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	var data map[string]json.RawMessage

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}

	var newModels []AIModelInfo
	seenIDs := make(map[string]bool)

	addModel := func(name, id string, aliases []string) {
		finalID := id
		if len(aliases) > 0 {
			finalID = aliases[0]
		}
		if finalID == "" || seenIDs[finalID] {
			return
		}
		finalName := name
		if finalName == "" {
			finalName = finalID
		}
		newModels = append(newModels, AIModelInfo{
			Name: finalName,
			ID:   finalID,
		})
		seenIDs[finalID] = true
	}

	for _, raw := range data {
		var m struct {
			Name    string   `json:"name"`
			ID      string   `json:"id"`
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal(raw, &m); err == nil && m.ID != "" {
			addModel(m.Name, m.ID, m.Aliases)
			continue
		}

		var arr []struct {
			Name    string   `json:"name"`
			ID      string   `json:"id"`
			Aliases []string `json:"aliases"`
		}
		if err := json.Unmarshal(raw, &arr); err == nil {
			for _, item := range arr {
				addModel(item.Name, item.ID, item.Aliases)
			}
		}
	}

	sort.Slice(newModels, func(i, j int) bool {
		return newModels[i].Name < newModels[j].Name
	})

	puterModelsMu.Lock()
	puterModels = newModels
	puterModelsMu.Unlock()

	return nil
}

func StartModelRefresh(ctx context.Context) {
	if err := FetchPuterModels(ctx); err != nil {
		LogAI("Failed initial model fetch: %v", err)
	}

	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := FetchPuterModels(ctx); err != nil {
					LogAI("Failed periodic model fetch: %v", err)
				}
			}
		}
	}()
}
