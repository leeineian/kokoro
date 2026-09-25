package ai

import (
	"context"
	"database/sql"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/snowflake/v2"
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

	_, _ = hooks.DB.ExecContext(context.Background(), `INSERT OR IGNORE INTO ai_tokens (id, token) VALUES (?, ?)`, id, token)

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
	rows, err = hooks.DB.QueryContext(ctx, "SELECT id, token FROM ai_tokens")
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
	rows, err = hooks.DB.QueryContext(ctx, "SELECT key_text, next_id, weight FROM ai_transitions WHERE channel_id = ?", m.channelID.String())
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
		window    = make([]int, AIMaxKeySize)
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

	tx, err := hooks.DB.BeginTx(context.Background(), nil)
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

		if AIMaxKeySize > 0 {
			if AIMaxKeySize > 1 {
				copy(window, window[1:])
				window[AIMaxKeySize-1] = nextWord
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
		window        = make([]int, AIMaxKeySize)
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
			if AIMaxKeySize > 0 {
				if AIMaxKeySize > 1 {
					copy(window, window[1:])
					window[AIMaxKeySize-1] = id
				} else {
					window[0] = id
				}
			}
		}

		if rand.Float64() < AISeedPrefixChance {
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

		if AIMaxKeySize > 0 {
			if AIMaxKeySize > 1 {
				copy(window, window[1:])
				window[AIMaxKeySize-1] = nextID
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
	mu        sync.RWMutex
	Models    map[snowflake.ID]*MarkovModel
	Tokens    *TokenMap
	cooldowns map[snowflake.ID]time.Time
}

func NewMarkovManager() *MarkovManager {
	return &MarkovManager{
		Tokens:    NewTokenMap(),
		Models:    make(map[snowflake.ID]*MarkovModel),
		cooldowns: make(map[snowflake.ID]time.Time),
	}
}

func (mm *MarkovManager) IsOnCooldown(channelID snowflake.ID) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	last, ok := mm.cooldowns[channelID]
	return ok && time.Since(last) < AICooldownDuration
}

func (mm *MarkovManager) setCooldown(channelID snowflake.ID) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.cooldowns[channelID] = time.Now()
}

func (mm *MarkovManager) StartCleanup() {
	go func() {
		defer func() { recover() }()
		var (
			ticker = time.NewTicker(AICleanupInterval)
			now    time.Time
			cid    snowflake.ID
			last   time.Time
		)
		for range ticker.C {
			mm.mu.Lock()
			now = time.Now()
			for cid, last = range mm.cooldowns {
				if now.Sub(last) > AICooldownDuration*2 {
					delete(mm.cooldowns, cid)
				}
			}
			mm.mu.Unlock()

			mm.mu.RLock()
			for _, model := range mm.Models {
				if time.Since(model.LastAccess) > AIModelTTL {
					mm.mu.RUnlock()
					mm.mu.Lock()
					if time.Since(model.LastAccess) > AIModelTTL {
						delete(mm.Models, model.channelID)
					}
					mm.mu.Unlock()
					mm.mu.RLock()
				}
			}
			mm.mu.RUnlock()
		}
	}()
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

	samples, err = hooks.FetchAIHistory(ctx, client, channelID)
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

	temp = AITemperatureMin + rand.Float64()*(AITemperatureMax-AITemperatureMin)

	for range AIAttempts {
		res, err = generateText(model, AIMaxLength, begin, temp, mm.Tokens)
		if err == nil && len(res) > len(begin) {
			return res
		}
	}

	for range AIAttempts {
		res, err = generateText(model, AIMaxLength, "", temp, mm.Tokens)
		if err == nil {
			return res
		}
	}

	return ""
}
