package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/obinnaokechukwu/ffgo"
	"github.com/obinnaokechukwu/ffgo/avcodec"
	"github.com/obinnaokechukwu/ffgo/avformat"
	"github.com/obinnaokechukwu/ffgo/avutil"
	"github.com/obinnaokechukwu/ffgo/swresample"
)

const (
	AudioCacheDir = ".tracks"
	MinTrackSize  = 32_000
	YoutubePrefix = "[YT]"
)

var (
	VoiceManager          *VoiceSystem
	OnceVoice             sync.Once
	audioCacheInitialized atomic.Bool
	audioCacheMu          sync.Mutex
	voiceShuttingDown     atomic.Bool
	cachedJSArgs          []string
	jsOnce                sync.Once
	camelCaseRegex        = regexp.MustCompile(`([a-z])([A-Z])`)
	metadataBlockRegex    = regexp.MustCompile(`[\(\[\{].*?[\)\]\}]`)
	OpusSilence           = []byte{0xf8, 0xff, 0xfe}
	SilenceDuration       = 1 * time.Second
	VoicePanels           = make(map[snowflake.ID]*VoicePanel)
	VoicePanelsMu         sync.Mutex
	maxStall              = 20 * time.Second
)

type VoiceSystem struct {
	mu       sync.Mutex
	sessions map[snowflake.ID]*VoiceSession
	cache    *QueryCache
}
type QueryCache struct {
	sync.RWMutex
	items map[string]cachedItem
}
type cachedItem struct {
	results   []SearchResult
	expiresAt time.Time
}
type VoiceSession struct {
	GuildID                snowflake.ID
	ChannelID              snowflake.ID
	channelMu              sync.RWMutex
	Conn                   voice.Conn
	queue                  []*Track
	queueMuRaw             sync.Mutex
	queueUpdate            chan struct{}
	joined                 bool
	joinedMu               sync.Mutex
	joinMu                 sync.Mutex
	joinedChan             chan struct{}
	joinedChanMu           sync.Mutex
	downloadMu             sync.Mutex
	downloadCond           *sync.Cond
	pendingDownloads       *PriorityQueue
	activeDownloads        int
	cancelCtx              context.Context
	cancelFunc             context.CancelFunc
	Autoplay, Looping      bool
	History, HistoryTitles []string
	HistoryAuthors         []string
	HistoryTokens          [][]string
	IDFStats               map[string]int
	streamCancel           context.CancelFunc
	provider               *StreamProvider
	clientMu               sync.RWMutex
	client                 bot.Client
	currentTrack           *Track
	lastStatus             string
	pauseChan              chan struct{}
	pauseMu                sync.RWMutex
	skipLoop               bool
	autoplayTrack          *Track
	statusMu               sync.Mutex
	statusChan             chan string
	goroutineWg            sync.WaitGroup
	nearingEnd             bool
	transcoder             *AstiavTranscoder
	Volume                 atomic.Int32
}
type VoicePanel struct {
	UserID    snowflake.ID
	GuildID   snowflake.ID
	Token     string
	AppID     snowflake.ID
	ExpiresAt time.Time
}
type Track struct {
	URL, Path, Title, Channel string
	ArtworkURL                string
	Duration                  time.Duration
	Downloaded                bool
	Enriched                  bool
	Error                     error
	NeedsResolution           bool
	LiveStream                io.Reader
	done                      chan struct{}
	MetadataReady             chan struct{}
	PlaybackStarted           chan struct{}
	onceStart                 sync.Once
	mu                        sync.Mutex
	cancel                    context.CancelFunc
	downloadCancel            context.CancelFunc
	Started                   bool
	NextTrackLogged           bool
	Priority                  int
	index                     int
	WrittenBytes              int64
	TotalSize                 int64
	SeekOffset                time.Duration
	FileCreated               chan struct{}
	metadataOnce              sync.Once
}
type SignalWriter struct {
	w   io.Writer
	sig chan struct{}
}
type TrackSignalWriter struct {
	w       io.Writer
	onWrite func(int)
}
type TailingReader struct {
	f       *os.File
	mu      sync.Mutex
	done    chan struct{}
	ctx     context.Context
	sig     chan struct{}
	playCtx context.Context
}
type StreamProvider struct {
	frames        chan []byte
	OnFinish      func()
	once          sync.Once
	sess          *VoiceSession
	ctx           context.Context
	frameCount    int64
	draining      bool
	silenceFrames int
}
type AstiavTranscoder struct {
	inputCtx               avformat.FormatContext
	decoderCtx, encoderCtx avcodec.Context
	audioStreamIndex       int
	packet                 avcodec.Packet
	frame                  avutil.Frame
	resampleCtx            swresample.SwrContext
	resampleFrame          avutil.Frame
	pcmBuffer              []byte
	_readCb, _seekCb       uintptr
	reader                 io.Reader
	onFrame                func([]byte)
	pts                    int64
	OnNearingEnd           func()
	nearingEndTriggered    bool
	seekChan               chan int64
	volume                 *atomic.Int32
	frameCount             int64
}
type SearchResult struct{ Title, ChannelName, URL string }
type CachedMetadata struct {
	Title, Channel string
	Duration       time.Duration
}
type ytdlpSearchResult struct {
	URL, Title, Uploader string
	Duration             time.Duration
}
type ytdlpMetadata struct {
	URL, Title, Uploader, Filename, ID string
	Duration                           time.Duration
}
type ytdlpPlaylistEntry struct{ URL, Title, Uploader string }
type recResult struct {
	es   []ytdlpPlaylistEntry
	prio int
}
type prioritizedSearchResult struct {
	res  []ytdlpSearchResult
	prio int
}
type metadataResult struct {
	title    string
	artist   string
	duration time.Duration
	source   string
	err      error
}
type PriorityQueue []*Track

type Hooks struct {
	LogInfo                           func(format string, v ...any)
	LogWarn                           func(format string, v ...any)
	LogError                          func(format string, v ...any)
	LogDebug                          func(format string, v ...any)
	RegisterCommand                   func(cmd discord.ApplicationCommandCreate, handler func(event *events.ApplicationCommandInteractionCreate))
	RegisterAutocompleteHandler       func(commandName string, handler func(event *events.AutocompleteInteractionCreate))
	RegisterComponentHandler          func(customID string, handler func(event *events.ComponentInteractionCreate))
	RegisterDaemon                    func(name string, logFunc func(format string, v ...any), start func(ctx context.Context) (bool, func(), func()))
	RespondInteractionV2              func(client bot.Client, interaction discord.Interaction, content string, ephemeral bool) error
	EditInteractionV2                 func(client bot.Client, interaction discord.Interaction, content string) error
	EditInteractionContainerV2        func(client bot.Client, interaction discord.Interaction, c any) error
	EditInteractionContainerV2ByToken func(client bot.Client, applicationID snowflake.ID, token string, c any) error
	SendComponentsV2                  func(client bot.Client, channelID snowflake.ID, components []any, messageReference *discord.MessageReference, stickers []snowflake.ID, embeds []discord.Embed) (*discord.Message, error)
	NewV2Container                    func(components ...any) any
	NewTextDisplay                    func(text string) any
	NewSeparator                      func(b bool) any
	NewMediaGallery                   func(urls ...string) any
	AppContext                        context.Context
	FormatDuration                    func(d time.Duration) string
	Truncate                          func(s string, length int) string
	AdminPerm                         int64
	GetAppConfig                      func(ctx context.Context, key string) (string, error)
	OnClientReady                     func(handler func(ctx context.Context, client bot.Client))
	TruncateWithPreserve              func(s string, length int, left, right string) string
	TruncateCenter                    func(s string, length int) string
	RegisterVoiceStateUpdateHandler   func(handler func(client bot.Client, sequenceNumber int, voiceState discord.VoiceState))
	OnVoiceStateUpdate                func(handler func(event *events.GuildVoiceStateUpdate))
}

var voiceSys Hooks

func Init(h Hooks) {
	voiceSys = h

	ffgo.SetLogLevel(ffgo.LogFatal)

	voiceSys.OnClientReady(func(ctx context.Context, client bot.Client) {
		vm := GetVoiceManager()

		voiceSys.RegisterDaemon("VOICE", voiceSys.LogInfo, func(ctx context.Context) (bool, func(), func()) {
			return true, func() {
					safeGo(func() {
						vm.startCacheGC(ctx)
					})
				}, func() {
					if vm != nil {
						voiceSys.LogInfo("Shutting down Voice Manager...")
						vm.Shutdown(context.Background())
					}
				}
		})

		voiceSys.OnVoiceStateUpdate(vm.onVoiceStateUpdate)
	})

	voiceSys.RegisterCommand(discord.SlashCommandCreate{
		Name:        "voice",
		Description: "Voice System",
		Options: []discord.ApplicationCommandOption{
			discord.ApplicationCommandOptionSubCommand{
				Name:        "play",
				Description: "Play audio from a URL",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:         "query",
						Description:  "The URL or song name to play",
						Required:     true,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionString{
						Name:         "queue",
						Description:  "Playback mode (now, next, or a number)",
						Required:     false,
						Autocomplete: true,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "autoplay",
						Description: "Enable or disable autoplay after this song",
						Required:    false,
					},
					discord.ApplicationCommandOptionBool{
						Name:        "loop",
						Description: "Loop the playback",
						Required:    false,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "stop",
				Description: "Stop audio and leave",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "queue",
				Description: "Show the current queue",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "forward",
				Description: "Forward the track by a duration",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "duration",
						Description: "Duration to seek (e.g. 10s, 1m)",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "rewind",
				Description: "Rewind the track by a duration",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionString{
						Name:        "duration",
						Description: "Duration to seek (e.g. 10s, 1m)",
						Required:    true,
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "skip",
				Description: "Skip the current track",
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "volume",
				Description: "Adjust the volume of the current session",
				Options: []discord.ApplicationCommandOption{
					discord.ApplicationCommandOptionInt{
						Name:        "set",
						Description: "Volume percentage (0-200)",
						Required:    true,
						MinValue:    intPtr(0),
						MaxValue:    intPtr(200),
					},
				},
			},
			discord.ApplicationCommandOptionSubCommand{
				Name:        "panel",
				Description: "Open a live Now Playing panel",
			},
		},
	}, handleVoice)

	voiceSys.RegisterAutocompleteHandler("voice", handleMusicAutocomplete)
	voiceSys.RegisterComponentHandler("voice:", handleVoiceComponent)
}

// --- UTILS ---

func ensureAudioCacheDir() {
	if voiceShuttingDown.Load() {
		return
	}
	if audioCacheInitialized.Load() {
		return
	}
	audioCacheMu.Lock()
	defer audioCacheMu.Unlock()
	if voiceShuttingDown.Load() || audioCacheInitialized.Load() {
		return
	}
	voiceSys.LogInfo("Initializing %s...", AudioCacheDir)
	_ = os.RemoveAll(AudioCacheDir)
	_ = os.MkdirAll(AudioCacheDir, 0755)
	audioCacheInitialized.Store(true)
}

func cleanupAudioCache() {
	audioCacheMu.Lock()
	defer audioCacheMu.Unlock()

	if _, err := os.Stat(AudioCacheDir); os.IsNotExist(err) {
		audioCacheInitialized.Store(false)
		return
	}

	voiceSys.LogInfo("Cleaning up %s...", AudioCacheDir)
	err := os.RemoveAll(AudioCacheDir)
	if err != nil {
		voiceSys.LogError("Failed to remove %s: %v", AudioCacheDir, err)
	}
	audioCacheInitialized.Store(false)
}

func (s *VoiceSession) lockQueue() {
	s.queueMuRaw.Lock()
}

func (s *VoiceSession) unlockQueue() {
	s.queueMuRaw.Unlock()
}

func mustGetSession(event *events.ApplicationCommandInteractionCreate) (*VoiceSession, bool) {
	guildID := event.GuildID()
	if guildID == nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "Not in a guild.", true)
		return nil, false
	}
	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "Not running.", true)
		return nil, false
	}
	return s, true
}

func mustGetUserVoiceState(event *events.ApplicationCommandInteractionCreate) (*discord.VoiceState, bool) {
	guildID := event.GuildID()
	if guildID == nil {
		return nil, false
	}
	vs, ok := event.Client().Caches.VoiceState(*guildID, event.User().ID)
	if !ok || vs.ChannelID == nil {
		_ = voiceSys.RespondInteractionV2(*event.Client(), event, "You must be in a voice channel.", true)
		return nil, false
	}
	return &vs, true
}

func (s *VoiceSession) buildTrackComponents(t *Track, prefix string) []any {
	var components []any
	components = append(components, voiceSys.NewTextDisplay(fmt.Sprintf("**%s**", prefix)))
	components = append(components, voiceSys.NewTextDisplay(t.FullDisplay()))
	t.mu.Lock()
	art := t.ArtworkURL
	t.mu.Unlock()
	if art != "" {
		components = append(components, voiceSys.NewMediaGallery(art))
	}
	return components
}

func strPtr(s string) *string {
	return &s
}

func GetVoiceManager() *VoiceSystem {
	OnceVoice.Do(func() {
		voiceShuttingDown.Store(false)
		VoiceManager = &VoiceSystem{
			sessions: make(map[snowflake.ID]*VoiceSession),
			cache: &QueryCache{
				items: make(map[string]cachedItem),
			},
		}
	})
	return VoiceManager
}

func (p *StreamProvider) SetContext(ctx context.Context) {
	p.ctx = ctx
}

func getYoutubePrefix() string {
	return YoutubePrefix
}

func tokenize(text string) []string {
	return strings.Fields(strings.ToLower(text))
}

func (s *VoiceSession) updateIDF(tokens []string, add bool) {
	for _, t := range tokens {
		if add {
			s.IDFStats[t]++
		} else {
			s.IDFStats[t]--
			if s.IDFStats[t] <= 0 {
				delete(s.IDFStats, t)
			}
		}
	}
}

func (s *VoiceSession) checkSimilarity(candidateTokens []string) bool {
	if len(s.HistoryTokens) == 0 {
		return false
	}

	cMap := make(map[string]bool)
	for _, t := range candidateTokens {
		cMap[t] = true
	}

	N := float64(len(s.HistoryTitles) + 1)

	for _, hTokens := range s.HistoryTokens {
		iScore, uScore := 0.0, 0.0

		for t := range cMap {
			df := s.IDFStats[t] + 1
			wt := math.Log(1.0 + N/float64(df))
			uScore += wt
		}

		for _, t := range hTokens {
			if !cMap[t] {
				df := s.IDFStats[t]
				if cMap[t] {
					df++
				}
				wt := math.Log(1.0 + N/float64(df))
				uScore += wt
			} else {
				df := s.IDFStats[t] + 1
				wt := math.Log(1.0 + N/float64(df))
				iScore += wt
			}
		}

		if uScore > 0 && (iScore/uScore) >= 0.7 {
			return true
		}
	}
	return false
}

func readMetadataCache(videoID string) *CachedMetadata {
	f, err := os.ReadFile(filepath.Join(AudioCacheDir, videoID+".meta"))
	if err != nil {
		return nil
	}
	var cm CachedMetadata
	if json.Unmarshal(f, &cm) != nil {
		return nil
	}
	return &cm
}

func writeMetadataCache(videoID, title, channel string, d time.Duration) {
	ensureAudioCacheDir()
	cm := CachedMetadata{Title: title, Channel: channel, Duration: d}
	b, _ := json.Marshal(cm)
	_ = os.WriteFile(filepath.Join(AudioCacheDir, videoID+".meta"), b, 0644)
}

func checkSimilarityAgainst(candidateTokens []string, historyTokens [][]string, idfStats map[string]int) bool {
	if len(historyTokens) == 0 {
		return false
	}

	cMap := make(map[string]bool)
	for _, t := range candidateTokens {
		cMap[t] = true
	}

	N := float64(len(historyTokens) + 1)

	for _, hTokens := range historyTokens {
		iScore, uScore := 0.0, 0.0

		for t := range cMap {
			df := idfStats[t] + 1
			wt := math.Log(1.0 + N/float64(df))
			uScore += wt
		}

		for _, t := range hTokens {
			if !cMap[t] {
				df := idfStats[t]
				wt := math.Log(1.0 + N/float64(df))
				uScore += wt
			} else {
				df := idfStats[t] + 1
				wt := math.Log(1.0 + N/float64(df))
				iScore += wt
			}
		}

		if uScore > 0 && (iScore/uScore) >= 0.7 {
			return true
		}
	}
	return false
}

func (s *VoiceSession) IsPaused() bool {
	s.pauseMu.RLock()
	defer s.pauseMu.RUnlock()
	select {
	case <-s.pauseChan:
		return false
	default:
		return true
	}
}

func (s *VoiceSession) TogglePause() bool {
	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()
	isClosed := false
	select {
	case <-s.pauseChan:
		isClosed = true
	default:
	}
	if isClosed {
		s.pauseChan = make(chan struct{})
		return true // Now paused
	} else {
		close(s.pauseChan)
		return false // Now playing
	}
}

func getRandomRecommendation(guildID *snowflake.ID) string {
	if guildID != nil {
		if s := GetVoiceManager().GetSession(*guildID); s != nil {
			s.lockQueue()
			defer s.unlockQueue()
			l := len(s.HistoryTitles)
			if l > 0 {
				idx := l - 1
				if l > 5 {
					idx = l - 1 - (int(time.Now().UnixNano()/1000) % 5)
				} else {
					idx = int(time.Now().UnixNano()/1000) % l
				}
				if len(s.HistoryAuthors) > idx {
					author := s.HistoryAuthors[idx]
					if author != "" && author != "NA" {
						return "Mix - " + author
					}
				}
				return "Mix - " + s.HistoryTitles[idx]
			}
		}
	}
	return "Trending Music"
}

func extractVideoID(u string) string {
	u = strings.TrimSpace(u)
	if strings.Contains(u, "youtu.be/") {
		parts := strings.Split(u, "youtu.be/")
		if len(parts) >= 2 {
			return strings.Split(parts[1], "?")[0]
		}
	}

	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err == nil {
		if v := parsed.Query().Get("v"); v != "" {
			return v
		}
		if id := parsed.Query().Get("id"); id != "" {
			return id
		}
		path := parsed.Path
		if strings.Contains(path, "/shorts/") {
			return strings.Split(strings.TrimPrefix(path, "/shorts/"), "/")[0]
		}
		if strings.Contains(path, "/embed/") {
			return strings.Split(strings.TrimPrefix(path, "/embed/"), "/")[0]
		}
		if strings.Contains(path, "/v/") {
			return strings.Split(strings.TrimPrefix(path, "/v/"), "/")[0]
		}
	}

	if len(u) == 11 && !strings.ContainsAny(u, "/?&.") {
		return u
	}
	return ""
}

func isYouTubeURL(u string) bool {
	return extractVideoID(u) != "" || strings.Contains(u, "youtube.com") || strings.Contains(u, "youtu.be") || strings.Contains(u, "google.com/url")
}

func normalizeTitle(ti, ch string) string {
	if ti == "" {
		return ""
	}

	tBuf := camelCaseRegex.ReplaceAllString(ti, "${1} ${2}")
	cBuf := camelCaseRegex.ReplaceAllString(ch, "${1} ${2}")

	t, c := strings.ToLower(tBuf), strings.ToLower(cBuf)

	for _, sep := range []string{"|", "//", " ─ ", " - "} {
		if strings.Contains(t, sep) {
			ps := strings.Split(t, sep)
			var nps []string
			for _, p := range ps {
				pt := strings.TrimSpace(p)
				shouldStrip := pt == c || pt == strings.ReplaceAll(c, " ", "")
				if !shouldStrip {
					nps = append(nps, pt)
				}
			}
			if len(nps) > 0 {
				t = strings.Join(nps, " ")
			}
			break
		}
	}
	for {
		t = strings.TrimSpace(t)
		loc := metadataBlockRegex.FindStringIndex(t)
		if loc != nil && loc[1] == len(t) {
			t = t[:loc[0]]
			continue
		}
		break
	}
	if c != "" {
		t = strings.ReplaceAll(t, c, " ")
	}
	var sb strings.Builder
	for _, r := range t {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

func calculateTFIDF(corpus []string) map[string]float64 {
	df := make(map[string]int)
	total := len(corpus)
	if total == 0 {
		return nil
	}
	for _, doc := range corpus {
		seen := make(map[string]bool)
		for _, w := range strings.Fields(strings.ToLower(doc)) {
			if !seen[w] {
				df[w]++
				seen[w] = true
			}
		}
	}
	weights := make(map[string]float64)
	for w, count := range df {
		weights[w] = math.Log(1.0 + float64(total)/float64(count))
	}
	return weights
}

func weightedSimilarity(a, b string, weights map[string]float64) bool {
	wa, wb := strings.Fields(strings.ToLower(a)), strings.Fields(strings.ToLower(b))
	sa, sb := make(map[string]bool), make(map[string]bool)
	union := make(map[string]bool)

	for _, w := range wa {
		sa[w] = true
		union[w] = true
	}
	for _, w := range wb {
		sb[w] = true
		union[w] = true
	}
	if len(union) == 0 {
		return false
	}
	if a == b {
		return true
	}

	iScore, uScore := 0.0, 0.0
	for w := range union {
		wt := 1.0
		if weights != nil {
			if val, ok := weights[w]; ok {
				wt = val
			} else {
				wt = math.Log(1.0 + float64(len(weights)))
			}
		}
		if sa[w] && sb[w] {
			iScore += wt
		}
		uScore += wt
	}
	if uScore == 0 {
		return false
	}
	return (iScore / uScore) >= 0.7
}

func (pq PriorityQueue) Len() int { return len(pq) }

func (pq PriorityQueue) Less(i, j int) bool {
	return pq[i].Priority > pq[j].Priority
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x any) {
	n := len(*pq)
	item := x.(*Track)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

func intPtr(i int) *int {
	return &i
}
func safeGo(f func()) {
	go func() {
		defer func() { recover() }()
		f()
	}()
}

func boolPtr(b bool) *bool {
	return &b
}
