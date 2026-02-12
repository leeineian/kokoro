package main

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asticode/go-astiav"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/lrstanley/go-ytdlp"
	"github.com/ppalone/ytsearch"
	"github.com/raitonoberu/ytmusic"
)

// ===========================
// Command Registration
// ===========================

func init() {
	astiav.SetLogLevel(astiav.LogLevelFatal)

	OnClientReady(func(ctx context.Context, client bot.Client) {
		RegisterDaemon(LogVoice, func(ctx context.Context) (bool, func(), func()) {
			return true, func() {}, func() {
				if VoiceManager != nil {
					LogVoice("Shutting down Voice Manager...")
					VoiceManager.Shutdown(context.Background())
				}
			}
		})

		vm := GetVoiceManager()
		RegisterVoiceStateUpdateHandler(vm.onVoiceStateUpdate)
	})

	RegisterCommand(discord.SlashCommandCreate{
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

	RegisterAutocompleteHandler("voice", handleMusicAutocomplete)
	RegisterComponentHandler("voice:", handleVoiceComponent)

}

// ===========================
// Constants & Variables
// ===========================

const AudioCacheDir = ".tracks"

var (
	// System
	VoiceManager          *VoiceSystem
	OnceVoice             sync.Once
	audioCacheInitialized atomic.Bool
	audioCacheMu          sync.Mutex
	voiceShuttingDown     atomic.Bool

	// Strings
	cachedJSArgs []string
	jsOnce       sync.Once

	// Regex
	camelCaseRegex     = regexp.MustCompile(`([a-z])([A-Z])`)
	metadataBlockRegex = regexp.MustCompile(`[\(\[\{].*?[\)\]\}]`)

	// Audio
	OpusSilence     = []byte{0xf8, 0xff, 0xfe}
	SilenceDuration = 1 * time.Second

	// Panels
	VoicePanels   = make(map[snowflake.ID]*VoicePanel)
	VoicePanelsMu sync.Mutex

	// Download Configuration
	maxConnWait = 20 * time.Second
	maxStall    = 5 * time.Second
	maxTotal    = 60 * time.Second
)

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
	LogInfo("Initializing %s...", AudioCacheDir)
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

	LogInfo("Cleaning up %s...", AudioCacheDir)
	err := os.RemoveAll(AudioCacheDir)
	if err != nil {
		LogError("Failed to remove %s: %v", AudioCacheDir, err)
	}
	audioCacheInitialized.Store(false)
}

// ===========================
// Structs
// ===========================

// VoiceSystem manages all voice sessions across guilds
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

// VoiceSession represents an active voice connection for a guild
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

// VoicePanel represents an active live panel for a user
type VoicePanel struct {
	UserID    snowflake.ID
	GuildID   snowflake.ID
	Token     string
	AppID     snowflake.ID
	ExpiresAt time.Time
}

// Track represents a music track in the queue
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
	FileCreated               chan struct{} // Signal when the file is available for reading
	metadataOnce              sync.Once
}

// SignalWriter wraps an io.Writer and signals a channel on every successful write
type SignalWriter struct {
	w   io.Writer
	sig chan struct{}
}

// TrackSignalWriter wraps an io.Writer and signals a channel on every successful write
type TrackSignalWriter struct {
	w       io.Writer
	onWrite func(int)
}

// TailingReader reads from a file that is being written to effectively decoupling download speed from playback speed
type TailingReader struct {
	f       *os.File
	mu      sync.Mutex
	done    chan struct{}
	ctx     context.Context
	sig     chan struct{}
	playCtx context.Context
}

func (s *VoiceSession) lockQueue() {
	s.queueMuRaw.Lock()
}

func (s *VoiceSession) unlockQueue() {
	s.queueMuRaw.Unlock()
}

// StreamProvider provides a stream of audio frames to the voice session
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

// AstiavTranscoder transcodes audio frames to Opus format
type AstiavTranscoder struct {
	inputCtx               *astiav.FormatContext
	decoderCtx, encoderCtx *astiav.CodecContext
	audioStreamIndex       int
	packet                 *astiav.Packet
	frame                  *astiav.Frame
	resampleCtx            *astiav.SoftwareResampleContext
	resampleFrame          *astiav.Frame
	fifo                   *astiav.AudioFifo
	reader                 io.Reader
	onFrame                func([]byte)
	pts                    int64
	OnNearingEnd           func()
	nearingEndTriggered    bool
	seekChan               chan int64
	volume                 *atomic.Int32 // Pointer to session volume
	frameCount             int64
}

// SearchResult represents a search result
type SearchResult struct{ Title, ChannelName, URL string }

// CachedMetadata represents cached metadata for a track
type CachedMetadata struct {
	Title, Channel string
	Duration       time.Duration
}

// ytdlpSearchResult represents a search result from ytdlp
type ytdlpSearchResult struct {
	URL, Title, Uploader string
	Duration             time.Duration
}

// ytdlpMetadata represents metadata for a track from ytdlp
type ytdlpMetadata struct {
	URL, Title, Uploader, Filename, ID string
	Duration                           time.Duration
}

// ytdlpPlaylistEntry represents an entry in a playlist from ytdlp
type ytdlpPlaylistEntry struct{ URL, Title, Uploader string }

// recResult represents a prioritized search result from ytdlp
type recResult struct {
	es   []ytdlpPlaylistEntry
	prio int
}

// prioritizedSearchResult represents a prioritized search result from ytdlp
type prioritizedSearchResult struct {
	res  []ytdlpSearchResult
	prio int
}

// metadataResult represents metadata for a track
type metadataResult struct {
	title    string
	artist   string
	duration time.Duration
	source   string
	err      error
}

// ===========================
// Voice System Initialization
// ===========================

// handleVoice routes voice subcommands to their respective handlers
func handleVoice(event *events.ApplicationCommandInteractionCreate) {
	data := event.SlashCommandInteractionData()
	if data.SubCommandName == nil {
		return
	}
	switch *data.SubCommandName {
	case "play":
		handleMusicPlay(event, data)
	case "stop":
		handleMusicStop(event, data)
	case "queue":
		handleMusicQueue(event, data)
	case "forward":
		handleMusicSeek(event, data, 1)
	case "rewind":
		handleMusicSeek(event, data, -1)
	case "skip":
		handleMusicSkip(event)
	case "volume":
		handleVoiceVolume(event, data)
	case "panel":
		handleVoicePanel(event)
	}
}

func handleVoiceVolume(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	vol := data.Int("set")
	s := GetVoiceManager().GetSession(*event.GuildID())
	if s == nil {
		_ = RespondInteractionV2(*event.Client(), event, "Not playing anything.", true)
		return
	}

	s.Volume.Store(int32(vol))
	UpdateVoicePanels(*event.GuildID(), *event.Client())
	_ = RespondInteractionV2(*event.Client(), event, fmt.Sprintf("Volume set to **%d%%**.", vol), false)
}

func handleMusicSeek(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData, factor int) {
	dStr := data.String("duration")
	d, err := time.ParseDuration(dStr)
	if err != nil {
		_ = event.CreateMessage(discord.MessageCreate{Content: "Invalid duration format (use 10s, 1m etc)."})
		return
	}
	guildID := event.GuildID()
	if guildID == nil {
		_ = event.CreateMessage(discord.MessageCreate{Content: "Not in a guild."})
		return
	}
	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = event.CreateMessage(discord.MessageCreate{Content: "Not running."})
		return
	}
	seekDuration := d
	if factor < 0 {
		seekDuration = -d
	}
	if err := s.Seek(seekDuration); err != nil {
		_ = RespondInteractionV2(*event.Client(), event, fmt.Sprintf("Seek failed: %v", err), false)
		return
	}
	action := "Forwarded"
	if factor < 0 {
		action = "Rewound"
	}
	UpdateVoicePanels(*guildID, *event.Client())
	_ = RespondInteractionV2(*event.Client(), event, fmt.Sprintf("%s %v", action, d), false)
}

func handleMusicSkip(event *events.ApplicationCommandInteractionCreate) {
	_ = event.DeferCreateMessage(false)

	guildID := event.GuildID()
	if guildID == nil {
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.MessageUpdate{Content: strPtr("Not in a guild.")})
		return
	}
	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.MessageUpdate{Content: strPtr("Not running.")})
		return
	}

	start := time.Now()
	LogVoice("Attempting to skip track in guild %s...", *guildID)
	title, err := s.Skip()
	if err != nil {
		LogVoice("Skip failed after %v: %v", time.Since(start), err)
		_ = EditInteractionV2(*event.Client(), event, fmt.Sprintf("Failed to skip: %v", err))
		return
	}
	LogVoice("Skip success after %v: %s", time.Since(start), title)
	UpdateVoicePanels(*guildID, *event.Client())
	_ = EditInteractionV2(*event.Client(), event, fmt.Sprintf("Skipped: %s", title))
}

func strPtr(s string) *string {
	return &s
}

// ===========================
// Voice Manager
// ===========================

// GetVoiceManager returns the singleton VoiceSystem instance
func GetVoiceManager() *VoiceSystem {
	OnceVoice.Do(func() {
		voiceShuttingDown.Store(false)
		VoiceManager = &VoiceSystem{
			sessions: make(map[snowflake.ID]*VoiceSession),
			cache: &QueryCache{
				items: make(map[string]cachedItem),
			},
		}
		safeGo(VoiceManager.startCacheGC)
	})
	return VoiceManager
}

func (vs *VoiceSystem) startCacheGC() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		<-ticker.C
		vs.cache.Lock()
		now := time.Now()
		for q, item := range vs.cache.items {
			if now.After(item.expiresAt) {
				delete(vs.cache.items, q)
			}
		}
		vs.cache.Unlock()
	}
}

// GetSession retrieves the voice session for a guild
func (vs *VoiceSystem) GetSession(guildID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.sessions[guildID]
}

// Prepare creates or retrieves a voice session for a guild
func (vs *VoiceSystem) Prepare(client bot.Client, guildID, channelID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if sess, ok := vs.sessions[guildID]; ok {
		// If session is dead (canceled), discard it and create a new one
		if sess.cancelCtx.Err() != nil {
			delete(vs.sessions, guildID)
		} else {
			sess.channelMu.Lock()
			oldChannelID := sess.ChannelID
			if oldChannelID != channelID {
				sess.ChannelID = channelID
				sess.channelMu.Unlock()
				// Move Discord API call to goroutine to avoid holding vs.mu
				safeGo(func() {
					route := rest.NewEndpoint(http.MethodPut, "/channels/"+oldChannelID.String()+"/voice-status")
					_ = client.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
				})
			} else {
				sess.channelMu.Unlock()
			}
			return sess
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	sess := &VoiceSession{
		GuildID:          guildID,
		ChannelID:        channelID,
		Conn:             client.VoiceManager.CreateConn(guildID),
		cancelCtx:        ctx,
		cancelFunc:       cancel,
		queue:            make([]*Track, 0),
		client:           client,
		statusChan:       make(chan string, 10),
		queueUpdate:      make(chan struct{}, 1),
		joinedChan:       make(chan struct{}),
		pauseChan:        make(chan struct{}),
		IDFStats:         make(map[string]int),
		pendingDownloads: &PriorityQueue{},
	}
	sess.Volume.Store(100)
	sess.downloadCond = sync.NewCond(&sess.downloadMu)
	heap.Init(sess.pendingDownloads)

	close(sess.pauseChan)
	sess.goroutineWg.Add(2)
	safeGo(func() {
		defer sess.goroutineWg.Done()
		sess.statusManager()
	})
	safeGo(func() {
		defer sess.goroutineWg.Done()
		sess.downloadLoop()
	})
	vs.sessions[guildID] = sess
	return sess
}

// Join connects the bot to a voice channel
func (vs *VoiceSystem) Join(ctx context.Context, client bot.Client, guildID, channelID snowflake.ID) error {
	sess := vs.Prepare(client, guildID, channelID)

	sess.joinMu.Lock()
	defer sess.joinMu.Unlock()

	sess.joinedMu.Lock()
	if sess.joined && sess.ChannelID == channelID {
		sess.joinedMu.Unlock()
		return nil
	}
	sess.joinedMu.Unlock()

	LogVoice("Joining channel %s in guild %s", channelID, guildID)

	var lastErr error
	for i := range 5 {
		if i > 0 {
			backoff := time.Duration(1<<uint(i-1)) * 1000 * time.Millisecond
			LogVoice("Retrying voice connection in %v (Attempt %d/5)", backoff, i+1)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		openCtx, openCancel := context.WithTimeout(ctx, 20*time.Second)
		err := sess.Conn.Open(openCtx, channelID, false, false)
		openCancel()

		if err != nil {
			lastErr = err
			LogVoice("Join attempt %d failed for guild %s: %v", i+1, guildID, err)
			sess.Conn.Close(context.Background())
			continue
		}
		lastErr = nil
		break
	}

	if lastErr != nil {
		LogVoice("Failed to connect to voice in guild %s after 5 attempts: %v", guildID, lastErr)
		return lastErr
	}

	sess.joinedMu.Lock()
	if !sess.joined {
		sess.joined = true
		sess.joinedChanMu.Lock()
		select {
		case <-sess.joinedChan:
		default:
			close(sess.joinedChan)
		}
		sess.joinedChanMu.Unlock()
		sess.goroutineWg.Add(2)
		safeGo(func() {
			defer sess.goroutineWg.Done()
			sess.processQueue()
		})
		safeGo(sess.monitorConnection)
	}
	sess.joinedMu.Unlock()
	return nil
}

func (s *VoiceSession) Reconnect(ctx context.Context) error {
	s.channelMu.RLock()
	cid := s.ChannelID
	s.channelMu.RUnlock()
	return GetVoiceManager().Join(ctx, s.GetClient(), s.GuildID, cid)
}

func (s *VoiceSession) monitorConnection() {
	defer func() {
		if r := recover(); r != nil {
			LogVoice("CRITICAL: monitorConnection panic recovered: %v", r)
		}
	}()
	defer s.goroutineWg.Done()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.cancelCtx.Done():
			return
		case <-ticker.C:

			s.joinedMu.Lock()
			joined := s.joined
			s.joinedMu.Unlock()

			if joined && (s.Conn == nil || s.Conn.ChannelID() == nil) {
				LogVoice("Detected disconnected voice state for guild %s, marking as not joined.", s.GuildID)
				s.joinedMu.Lock()
				s.joined = false
				s.joinedMu.Unlock()
				joined = false
			}

			if !joined {
				_ = s.Reconnect(s.cancelCtx)
			}
		}
	}
}

// Leave disconnects the bot from a voice channel instantly and cleans up
func (vs *VoiceSystem) Leave(ctx context.Context, guildID snowflake.ID) {
	vs.mu.Lock()
	sess, ok := vs.sessions[guildID]
	if !ok {
		vs.mu.Unlock()
		return
	}
	delete(vs.sessions, guildID)
	vs.mu.Unlock()

	vs.cleanupSession(sess)
}

// cleanupSession performs the teardown of a voice session outside of the vs.mu lock
func (vs *VoiceSystem) cleanupSession(sess *VoiceSession) {
	if sess == nil {
		return
	}
	// client is a struct value, no nil check needed or check if empty.
	// We'll trust the caller passes a valid client.

	// 1. Instantly trigger panel updates that session ended
	UpdateVoicePanels(sess.GuildID, sess.GetClient())

	// 2. Cleanup in background to avoid blocking
	safeGo(func() {
		s := sess // Capture sess for the goroutine
		s.Stop()  // Clears queue, cancels context, clears voice status channel

		s.channelMu.RLock()
		cid := s.ChannelID
		s.channelMu.RUnlock()

		if cid != 0 {
			// Effort to clear Discord API voice channel status
			route := rest.NewEndpoint(http.MethodPut, "/channels/"+cid.String()+"/voice-status")
			_ = s.GetClient().Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
		}

		s.joinedMu.Lock()
		s.joined = false
		s.joinedMu.Unlock()

		if s.Conn != nil {
			s.Conn.Close(context.Background())
		}

		vs.mu.Lock()
		activeCount := len(vs.sessions)
		vs.mu.Unlock()

		if activeCount == 0 {
			cleanupAudioCache()
		}
	})
}

// Shutdown gracefully stops all voice sessions and clears their status
func (vs *VoiceSystem) Shutdown(ctx context.Context) {
	voiceShuttingDown.Store(true)
	vs.mu.Lock()
	sessions := make([]*VoiceSession, 0, len(vs.sessions))
	for id, sess := range vs.sessions {
		sessions = append(sessions, sess)
		delete(vs.sessions, id)
	}
	vs.mu.Unlock()

	var wg sync.WaitGroup
	for _, sess := range sessions {
		wg.Add(1)
		safeGo(func() {
			func(s *VoiceSession) {
				defer wg.Done()
				s.channelMu.RLock()
				channelID := s.ChannelID
				s.channelMu.RUnlock()

				route := rest.NewEndpoint(http.MethodPut, "/channels/"+channelID.String()+"/voice-status")
				_ = s.GetClient().Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
				s.Stop()
			}(sess)
		})
	}
	wg.Wait()
	LogVoice("All voice sessions stopped. Finalizing cleanup...")
	cleanupAudioCache()
}

// Play adds a track to the queue and starts playback
func (vs *VoiceSystem) Play(ctx context.Context, guildID snowflake.ID, url, mode string, pos int) (*Track, int, error) {
	s := vs.GetSession(guildID)
	if s == nil {
		return nil, 0, errors.New("not connected to voice")
	}

	tracks, _ := vs.resolvePlaylist(ctx, url)
	if len(tracks) == 0 {
		tracks = []*Track{NewTrack(url)}
	}

	firstTrack := tracks[0]
	s.queueTracks(tracks, mode, pos)

	firstTrack.Priority = 1
	s.scheduleDownload(firstTrack)
	s.addToHistory(url, "", "")

	return firstTrack, len(tracks), nil
}

func (vs *VoiceSystem) resolvePlaylist(ctx context.Context, url string) ([]*Track, error) {
	if !strings.Contains(url, "list=") {
		return nil, nil
	}
	entries, err := ytdlpExtractPlaylist(ctx, url, 100)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	tracks := make([]*Track, 0, len(entries))
	for _, e := range entries {
		nt := NewTrack(e.URL)
		nt.Title = e.Title
		nt.Channel = e.Uploader
		tracks = append(tracks, nt)
	}
	return tracks, nil
}

func (s *VoiceSession) queueTracks(tracks []*Track, mode string, pos int) {
	s.lockQueue()
	defer s.unlockQueue()

	if mode == "now" {
		for _, qt := range s.queue {
			qt.Cleanup()
		}
		s.queue = append([]*Track(nil), tracks...)
		s.skipLoop = true
		if s.currentTrack != nil {
			s.currentTrack.Cleanup()
		}
		s.currentTrack = nil
		if s.autoplayTrack != nil {
			s.autoplayTrack.Cleanup()
		}
		s.autoplayTrack = nil
		if s.streamCancel != nil {
			s.streamCancel()
		}
	} else if mode == "next" {
		s.queue = append(append([]*Track(nil), tracks...), s.queue...)
	} else if pos > 0 {
		idx := pos - 1
		if idx >= len(s.queue) {
			s.queue = append(s.queue, tracks...)
		} else {
			newQueue := make([]*Track, 0, len(s.queue)+len(tracks))
			newQueue = append(newQueue, s.queue[:idx]...)
			newQueue = append(newQueue, tracks...)
			newQueue = append(newQueue, s.queue[idx:]...)
			s.queue = newQueue
		}
	} else {
		s.queue = append(s.queue, tracks...)
	}

	select {
	case s.queueUpdate <- struct{}{}:
	default:
	}
}

// onVoiceStateUpdate handles voice state changes and auto-disconnect
func (vs *VoiceSystem) onVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	vs.mu.Lock()
	s, ok := vs.sessions[event.VoiceState.GuildID]
	vs.mu.Unlock()

	if event.VoiceState.UserID == event.Client().ID() {
		vs.handleBotVoiceStateUpdate(event, s)
		return
	}

	if ok {
		vs.updateAutoPauseState(event, s)
	}
}

func (vs *VoiceSystem) handleBotVoiceStateUpdate(event *events.GuildVoiceStateUpdate, s *VoiceSession) {
	if event.VoiceState.ChannelID == nil {
		LogVoice("Bot disconnected by external event in guild %s", event.VoiceState.GuildID)
		delete(vs.sessions, event.VoiceState.GuildID)
		// Move cleanup to goroutine to avoid calling into locked methods
		safeGo(func() { vs.cleanupSession(s) })
		return
	}

	s.channelMu.RLock()
	currentChannelID := s.ChannelID
	s.channelMu.RUnlock()

	if currentChannelID == 0 || *event.VoiceState.ChannelID != currentChannelID {
		oldChannelID := currentChannelID
		LogVoice("Bot moved from %s to %s in guild %s", oldChannelID, *event.VoiceState.ChannelID, event.VoiceState.GuildID)

		if oldChannelID != 0 {
			safeGo(func() {
				func(cid snowflake.ID, cli *bot.Client) {
					route := rest.NewEndpoint(http.MethodPut, "/channels/"+cid.String()+"/voice-status")
					_ = cli.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
				}(oldChannelID, event.Client())
			})
		}

		s.channelMu.Lock()
		s.ChannelID = *event.VoiceState.ChannelID
		s.channelMu.Unlock()
		s.statusMu.Lock()
		status := s.lastStatus
		s.statusMu.Unlock()
		s.setVoiceStatus(status)
	}
}

func (vs *VoiceSystem) updateAutoPauseState(event *events.GuildVoiceStateUpdate, s *VoiceSession) {
	s.channelMu.RLock()
	currentChannelID := s.ChannelID
	s.channelMu.RUnlock()

	if currentChannelID == 0 {
		return
	}
	humanCount := 0
	for state := range event.Client().Caches.VoiceStates(event.VoiceState.GuildID) {
		if state.ChannelID != nil && *state.ChannelID == currentChannelID && state.UserID != event.Client().ID() {
			if state.SelfDeaf {
				continue
			}
			if m, ok := event.Client().Caches.Member(event.VoiceState.GuildID, state.UserID); !ok || !m.User.Bot {
				humanCount++
			}
		}
	}
	s.pauseMu.RLock()
	paused := false
	select {
	case <-s.pauseChan:
	default:
		paused = true
	}
	s.pauseMu.RUnlock()
	if humanCount == 0 && !paused {
		LogVoice("Pausing playback in guild %s (No humans)", event.VoiceState.GuildID)
		s.pauseMu.Lock()
		isClosed := false
		select {
		case <-s.pauseChan:
			isClosed = true
		default:
		}
		if isClosed {
			s.pauseChan = make(chan struct{})
		}
		s.pauseMu.Unlock()
		s.statusMu.Lock()
		status := s.lastStatus
		s.statusMu.Unlock()
		if status != "" {
			if strings.HasPrefix(status, "⏸️ ") {
				status = "▶️ " + status[len("⏸️ "):]
			} else if strings.HasPrefix(status, "⏩ ") {
				status = "▶️ " + status[len("⏩ "):]
			} else {
				status = "▶️ " + status
			}
			s.setVoiceStatus(status)
		} else {
			s.setVoiceStatus("▶️ Paused")
		}
	} else if humanCount > 0 && paused {
		LogVoice("Resuming playback in guild %s", event.VoiceState.GuildID)
		s.pauseMu.Lock()
		isClosed := false
		select {
		case <-s.pauseChan:
			isClosed = true
		default:
		}
		if !isClosed {
			close(s.pauseChan)
		}
		s.pauseMu.Unlock()
		s.statusMu.Lock()
		status := s.lastStatus
		if status == "" {
			status = "Resuming..."
		}
		s.statusMu.Unlock()
		s.setVoiceStatus(status)
	}
}

// ===========================
// Voice Session
// ===========================

// Seek seeks the current track to a relative offset
func (s *VoiceSession) Seek(duration time.Duration) error {
	s.lockQueue()
	if s.currentTrack == nil {
		s.unlockQueue()
		return fmt.Errorf("no track currently playing")
	}

	// Create new context for the new playback
	ctx, cancel := context.WithCancel(s.cancelCtx)
	s.streamCancel = cancel

	cur := s.currentTrack
	tr := s.transcoder
	s.unlockQueue()

	if cur == nil {
		return errors.New("no active track")
	}

	cur.mu.Lock()
	trackDuration := cur.Duration
	downloaded := cur.Downloaded
	totalSize := cur.TotalSize
	written := cur.WrittenBytes
	id := extractVideoID(cur.URL)
	cur.mu.Unlock()

	current := tr.GetTimestamp()
	offset := int64(duration.Milliseconds()) * 48
	targetSamples := current + offset
	if targetSamples < 0 {
		targetSamples = 0
	}
	if trackDuration > 0 {
		maxSamples := int64(trackDuration.Seconds() * 48000)
		if targetSamples > maxSamples {
			targetSamples = maxSamples
		}
	}

	targetMs := targetSamples / 48
	targetDuration := time.Duration(targetMs) * time.Millisecond

	if !downloaded && totalSize > 0 && trackDuration > 0 {
		bufferedMs := (float64(written) / float64(totalSize)) * float64(trackDuration.Milliseconds())
		if float64(targetMs) > bufferedMs || targetMs < 0 {
			LogVoice("Smart Seek: Target %v beyond buffer (~%vms). Restarting stream...", targetDuration, int64(bufferedMs))

			cur.mu.Lock()
			if cur.downloadCancel != nil {
				cur.downloadCancel()
			}
			cur.SeekOffset = targetDuration
			if trackDuration > 0 && totalSize > 0 {
				cur.WrittenBytes = int64((float64(targetMs) / float64(trackDuration.Milliseconds())) * float64(totalSize))
			} else {
				cur.WrittenBytes = 0
			}
			cur.mu.Unlock()

			baseName := filepath.Join(".tracks", fmt.Sprintf("%s_%d", id, targetMs))
			fragmentPath := baseName + ".webm"
			partPath := fragmentPath + ".part"

			safeGo(func() {
				s.downloadAndCache(ctx, cur, fragmentPath, cur.URL)
			})

			select {
			case <-cur.FileCreated:
			case <-time.After(5 * time.Second):
				return errors.New("timeout waiting for seek stream")
			}

			if reader, ok := tr.reader.(*TailingReader); ok {
				if err := reader.SwitchFile(partPath); err != nil {
					return err
				}
			}

			tr.Seek(targetSamples, 0)
			return nil
		}
	}

	_, err := tr.Seek(targetSamples, 0)
	return err
}

// Skip skips the currently playing track
func (s *VoiceSession) Skip() (string, error) {
	s.lockQueue()
	if s.currentTrack == nil && len(s.queue) == 0 {
		s.unlockQueue()
		return "", errors.New("nothing playing")
	}
	// Prevent looping for this specific track if it was going to loop
	s.skipLoop = true

	title := "Track"
	if s.currentTrack != nil {
		title = s.currentTrack.Title
		if title == "" {
			title = s.currentTrack.URL
		}
	}

	cancel := s.streamCancel
	s.unlockQueue()

	if cancel != nil {
		cancel()
	}

	return title, nil
}

// WaitJoined waits for the bot to join the voice channel
func (s *VoiceSession) WaitJoined(ctx context.Context) error {
	select {
	case <-s.joinedChan:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.cancelCtx.Done():
		return errors.New("session closed")
	}
}

// Stop stops playback and clears the queue
func (s *VoiceSession) Stop() {
	s.skipLoop = true
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	s.lockQueue()
	if s.streamCancel != nil {
		s.streamCancel()
	}
	s.transcoder = nil
	if s.Conn != nil { // Should be safe to access Conn without lock as it is only written in Prepare
		s.setOpusFrameProviderSafe(nil)
		s.setSpeakingSafe(0)
	}
	s.unlockQueue()
	s.lockQueue()
	for _, t := range s.queue {
		t.Cleanup()
	}
	s.queue = nil
	if s.currentTrack != nil {
		s.currentTrack.Cleanup()
		s.currentTrack = nil
	}
	if s.autoplayTrack != nil {
		s.autoplayTrack.Cleanup()
		s.autoplayTrack = nil
	}
	s.unlockQueue()
	select {
	case s.queueUpdate <- struct{}{}:
	default:
	}

	s.setVoiceStatus("")
}

// WaitForCleanup waits for all session goroutines to exit
func (s *VoiceSession) WaitForCleanup() {
	s.goroutineWg.Wait()
}

// RefreshStatus recalculates the voice status based on current session state
func (s *VoiceSession) RefreshStatus() {
	// Lock for accessing state
	s.lockQueue()
	track := s.currentTrack
	paused := false
	s.pauseMu.RLock()
	select {
	case <-s.pauseChan:
	default:
		paused = true
	}
	s.pauseMu.RUnlock()

	queueLen := len(s.queue)
	if queueLen > 0 && s.queue[0] == track {
		queueLen--
	}
	s.unlockQueue()

	if track == nil {
		if paused {
			s.setVoiceStatus("▶️ Paused")
		} else {
			s.setVoiceStatus("")
		}
		return
	}

	track.mu.Lock()
	title, channel := track.Title, track.Channel
	track.mu.Unlock()

	if title == "" {
		if paused {
			s.setVoiceStatus("▶️ Paused")
		} else {
			s.setVoiceStatus("⏸️ Music Track")
		}
		return
	}

	prefix := "⏸️ "
	if paused {
		prefix = "▶️ "
	}
	sep := ""
	if channel != "" {
		sep = " · "
	}
	s.setVoiceStatus(TruncateWithPreserve(title, 128, prefix, sep+channel))
}

func (s *VoiceSession) setVoiceStatus(status string) {
	select {
	case s.statusChan <- status:
	default:
	}
}

// statusManager manages the voice channel status updates
func (s *VoiceSession) statusManager() {
	defer func() {
		if r := recover(); r != nil {
			LogVoice("CRITICAL: statusManager panic recovered: %v", r)
		}
	}()
	var cur string
	for {
		select {
		case <-s.cancelCtx.Done():
			return
		case n := <-s.statusChan:
		drain:
			for {
				select {
				case m := <-s.statusChan:
					n = m
				default:
					break drain
				}
			}

			if n == cur {
				continue
			}

			s.statusMu.Lock()
			target := n
			if len([]rune(target)) > 128 {
				target = TruncateCenter(target, 128)
			}
			if target != "" && !strings.HasPrefix(target, "▶️") {
				s.lastStatus = target
			}
			s.channelMu.RLock()
			channelID := s.ChannelID
			s.channelMu.RUnlock()

			// Fire and forget (log error if any)
			safeGo(func() {
				func(cid snowflake.ID, status string) {
					cl := s.GetClient()
					err := cl.Rest.Do(rest.NewEndpoint(http.MethodPut, "/channels/"+cid.String()+"/voice-status").Compile(nil), map[string]string{"status": status}, nil)
					if err != nil {
						LogVoice("Failed to update status for %s: %v", cid, err)
					}
				}(channelID, target)
			})

			cur = target
			s.statusMu.Unlock()
			UpdateVoicePanels(s.GuildID, s.GetClient())
		}
	}
}

func (s *VoiceSession) updateNextTrackStatusIfNeeded(next *Track) {
	s.lockQueue()
	if s.nearingEnd {
		s.unlockQueue()
		return
	}
	t := next
	isCurrent := s.currentTrack == t
	isNext := false
	if len(s.queue) > 0 && s.queue[0] == t {
		isNext = true
	} else if s.Autoplay && s.autoplayTrack == t {
		isNext = true
	}
	nearing := s.nearingEnd
	looping := s.Looping
	s.unlockQueue()

	if (isCurrent || isNext) && !looping && t.Title != "" {
		sep := ""
		if t.Channel != "" && t.Channel != "NA" {
			sep = " · "
		}
		if isNext {
			t.mu.Lock()
			if !t.NextTrackLogged {
				LogVoice("Next Track: %s%s%s (%s) [%s]", t.Title, sep, t.Channel, t.URL, t.Duration.Round(time.Second))
				t.NextTrackLogged = true
			}
			t.mu.Unlock()
		}

		if isCurrent || (isNext && nearing) {
			prefix := "⏩ "
			if isCurrent {
				prefix = "⏸️ "
			}
			s.setVoiceStatus(TruncateWithPreserve(t.Title, 128, prefix, sep+t.Channel))
		}
	}
}

// setOpusFrameProviderSafe sets the opus frame provider safely, recovering from any potential panics
func (s *VoiceSession) setOpusFrameProviderSafe(provider voice.OpusFrameProvider) {
	if s.cancelCtx.Err() != nil {
		return
	}
	if s.Conn == nil || (reflect.ValueOf(s.Conn).Kind() == reflect.Ptr && reflect.ValueOf(s.Conn).IsNil()) {
		return
	}

	for i := range 3 {
		if s.trySetOpusFrameProvider(provider) {
			return
		}
		if i < 2 {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.cancelCtx.Done():
				return
			}
		}
		if s.cancelCtx.Err() != nil {
			return
		}
	}
	LogVoice("Exhausted retries for SetOpusFrameProvider in guild %s", s.GuildID)
}

func (s *VoiceSession) trySetOpusFrameProvider(provider voice.OpusFrameProvider) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	s.Conn.SetOpusFrameProvider(provider)
	return true
}

// setSpeakingSafe sets the speaking state safely
func (s *VoiceSession) setSpeakingSafe(flags voice.SpeakingFlags) {
	if s.cancelCtx.Err() != nil {
		return
	}
	if s.Conn == nil || (reflect.ValueOf(s.Conn).Kind() == reflect.Ptr && reflect.ValueOf(s.Conn).IsNil()) {
		return
	}

	for i := 0; i < 3; i++ {
		if s.trySetSpeaking(flags) {
			return
		}
		if i < 2 {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.cancelCtx.Done():
				return
			}
		}
		if s.cancelCtx.Err() != nil {
			return
		}
	}
	LogVoice("Exhausted retries for SetSpeaking in guild %s", s.GuildID)
}

func (s *VoiceSession) trySetSpeaking(flags voice.SpeakingFlags) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	s.Conn.SetSpeaking(s.cancelCtx, flags)
	return true
}

// processQueue processes tracks from the queue and handles playback
func (s *VoiceSession) processQueue() {
	defer func() {
		if r := recover(); r != nil {
			LogVoice("CRITICAL: processQueue panic recovered: %v", r)
		}
	}()
	// Main loop
	for {
		s.lockQueue()

		for len(s.queue) == 0 {
			s.unlockQueue()
			select {
			case <-s.queueUpdate:
				s.lockQueue()
			case <-s.cancelCtx.Done():
				return
			}
		}
		t := s.queue[0]
		s.queue = s.queue[1:]
		s.currentTrack = t
		s.nearingEnd = false
		if s.autoplayTrack != nil {
			s.autoplayTrack.Cleanup()
			s.autoplayTrack = nil
		}
		s.unlockQueue()

		t.Priority = 1
		s.scheduleDownload(t)

		t.mu.Lock()
		downloaded := t.Downloaded
		t.mu.Unlock()
		if !downloaded {
			s.updateNextTrackStatusIfNeeded(t)
		}

		if err := t.Wait(s.cancelCtx); err != nil {
			LogVoice("Skipping track %s due to error: %v", t.URL, err)
			continue
		}

		s.lockQueue()
		if len(s.queue) > 0 {
			s.queue[0].Priority = 1
			s.scheduleDownload(s.queue[0])
		}
		s.unlockQueue()
		if err := s.WaitJoined(s.cancelCtx); err != nil {
			LogVoice("Skipping track %s: failed to wait for join: %v", t.URL, err)
			continue
		}

		ctx, cancel := context.WithCancel(s.cancelCtx)
		t.cancel = cancel

		safeGo(func() {
			select {
			case <-t.MetadataReady:
			case <-ctx.Done():
			case <-s.cancelCtx.Done():
			case <-time.After(15 * time.Second):
			}

			t.mu.Lock()
			title, channel, url, duration := t.Title, t.Channel, t.URL, t.Duration
			t.mu.Unlock()
			select {
			case <-t.PlaybackStarted:
				if title == "" || strings.HasPrefix(title, "http") {
					if id := extractVideoID(url); id != "" {
						title = "YouTube Track (" + id + ")"
					} else {
						title = "Music Track"
					}
				}
				LogVoice("Playing track: %s · %s (%s) [%v]", title, channel, url, duration)
				s.RefreshStatus()
			case <-ctx.Done():
				LogVoice("Track skipped/finished: %s", url)
			}
			s.addToHistory(url, title, channel)
		})

		s.lockQueue()
		autoplay := s.Autoplay
		s.unlockQueue()
		if autoplay {
			safeGo(func() {
				func(url string) {
					select {
					case <-t.MetadataReady:
					case <-s.cancelCtx.Done():
						return
					case <-time.After(10 * time.Second):
					}

					next, err := s.fetchRelated(url, t.Title, t.Channel)
					if err == nil && next != "" {
						nt := NewTrack(next)
						// Check for autoplay pre-fetch
						s.lockQueue()
						if s.Autoplay && s.autoplayTrack == nil && s.currentTrack != nil && s.currentTrack.URL == url {
							if s.autoplayTrack != nil {
								s.autoplayTrack.Cleanup()
							}
							s.autoplayTrack = nt
							nt.Priority = 0
							s.scheduleDownload(nt)
						}
						s.unlockQueue()
					} else {
						LogVoice("Autoplay pre-fetch failed for %s: %v (Next: %s)", url, err, next)
					}
				}(t.URL)
			})
		}

		if t.LiveStream != nil {
			s.streamCommon(t.URL, t.URL, t.LiveStream)
		} else {
			s.streamFile(t.URL, t.Path)
		}

		s.setVoiceStatus("")

		s.lockQueue()

		loop := s.Looping && !s.skipLoop
		s.skipLoop = false
		if loop {
			s.queue = append([]*Track{t}, s.queue...)
			s.unlockQueue()
			continue
		}
		s.unlockQueue()

		t.Cleanup()

		s.lockQueue()

		if len(s.queue) == 0 && s.Autoplay {
			if s.autoplayTrack != nil {
				next := s.autoplayTrack
				s.autoplayTrack = nil
				s.queue = append(s.queue, next)
				select {
				case s.queueUpdate <- struct{}{}:
				default:
				}
				s.unlockQueue()
				continue
			} else {
				s.unlockQueue()
				next, err := s.fetchRelated(t.URL, t.Title, t.Channel)
				if err == nil && next != "" {
					_, _, _ = GetVoiceManager().Play(context.Background(), s.GuildID, next, "", 0)
				} else {
					LogVoice("Autoplay sync fetch failed for %s: %v", t.URL, err)
				}
				continue
			}
		}
		if len(s.queue) == 0 {
			s.currentTrack = nil
			s.autoplayTrack = nil
			s.unlockQueue()
		} else {
			s.unlockQueue()
		}
	}
}

// ===========================
// Track
// ===========================

func (t *Track) Cancel() {
	if t.cancel != nil {
		t.cancel()
	}
}

func (t *Track) Cleanup() {
	t.Cancel()
	if c, ok := t.LiveStream.(io.Closer); ok {
		c.Close()
	}
	if t.Path != "" {
		size := int64(0)
		if st, err := os.Stat(t.Path); err == nil {
			size = st.Size()
		}
		err := os.Remove(t.Path)
		if err != nil && !os.IsNotExist(err) {
			LogVoice("Failed to remove track file %s: %v", t.Path, err)
		} else if err == nil {
			LogVoice("Cleaned up track file: %s (Size: %d bytes)", t.Path, size)
		}

		ext := filepath.Ext(t.Path)
		if ext != "" {
			metaPath := strings.TrimSuffix(t.Path, ext) + ".meta"
			_ = os.Remove(metaPath)
			id := extractVideoID(t.URL)
			if id != "" {
				pattern := filepath.Join(".tracks", id+"_*")
				matches, _ := filepath.Glob(pattern)
				for _, m := range matches {
					_ = os.Remove(m)
				}
			}
		}
	}
}

func (t *Track) SafeCloseMetadata() {
	t.metadataOnce.Do(func() {
		close(t.MetadataReady)
	})
}

func (s *SignalWriter) Write(p []byte) (n int, err error) {
	n, err = s.w.Write(p)
	if n > 0 {
		select {
		case s.sig <- struct{}{}:
		default:
		}
	}
	return
}

func (s *TrackSignalWriter) Write(p []byte) (n int, err error) {
	n, err = s.w.Write(p)
	if n > 0 {
		s.onWrite(n)
	}
	return
}

func (r *TailingReader) SetPlayContext(ctx context.Context) {
	r.playCtx = ctx
}

func (r *TailingReader) SwitchFile(newPath string) error {
	newF, err := os.Open(newPath)
	if err != nil {
		return err
	}

	r.mu.Lock()
	oldF := r.f
	r.f = newF
	r.mu.Unlock()

	if oldF != nil {
		oldF.Close()
	}

	select {
	case r.sig <- struct{}{}:
	default:
	}
	return nil
}

func (r *TailingReader) Read(p []byte) (int, error) {
	for {
		r.mu.Lock()
		f := r.f
		r.mu.Unlock()

		n, err := f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err != io.EOF {
			return n, err
		}

		select {
		case <-r.done:
			r.mu.Lock()
			f2 := r.f
			r.mu.Unlock()
			n2, err2 := f2.Read(p)
			if n2 > 0 {
				return n2, nil
			}
			if err2 != nil && err2 != io.EOF {
				return n2, err2
			}
			return 0, io.EOF
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.sig:
			continue
		case <-r.playCtx.Done():
			return 0, r.playCtx.Err()
		}
	}
}

func (r *TailingReader) Close() error {
	return r.f.Close()
}

func (r *TailingReader) Seek(offset int64, whence int) (int64, error) {
	return r.f.Seek(offset, whence)
}

// NewTrack creates a new track with the given URL
func NewTrack(url string) *Track {
	t := &Track{
		URL:             url,
		Title:           "",
		done:            make(chan struct{}),
		MetadataReady:   make(chan struct{}),
		PlaybackStarted: make(chan struct{}),
	}
	if !strings.HasPrefix(url, "http") || (isLikelyMusicStreamingSite(url) && !isYouTubeURL(url)) {
		t.NeedsResolution = true
	}
	return t
}

// Wait waits for the track to be ready or error
func (t *Track) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		return t.Error
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MarkReady marks the track as ready for playback
func (t *Track) MarkReady(path, title, channel string, d time.Duration, s io.Reader) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Downloaded || t.Error != nil {
		return
	}
	t.Path, t.Title, t.Channel, t.Duration, t.Downloaded, t.LiveStream = path, title, channel, d, true, s
	t.SafeCloseMetadata() // Ensure MetadataReady is closed
	close(t.done)
}

// MarkError marks the track as failed with an error
func (t *Track) MarkError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Downloaded || t.Error != nil {
		return
	}
	t.Error = err
	t.SafeCloseMetadata() // Ensure MetadataReady is closed
	close(t.done)
}

// ===========================
// Transcoding & Helpers
// ===========================

func (s *VoiceSession) streamFile(url, path string) {
	s.streamCommon(url, path, nil)
}

func (s *VoiceSession) streamCommon(url, inputPath string, reader io.Reader) {
	s.lockQueue()
	if s.streamCancel != nil {
		s.streamCancel()
	}
	p := NewStreamProvider(s)
	s.provider = p
	done := make(chan struct{})
	p.OnFinish = func() {
		close(done)
	}
	ctx, cancel := context.WithCancel(s.cancelCtx)
	s.streamCancel = cancel
	p.SetContext(ctx)
	if tr, ok := reader.(*TailingReader); ok {
		tr.SetPlayContext(ctx)
	}
	s.unlockQueue()

	defer cancel()
	safeGo(func() {
		defer func() {
			// Always call OnFinish, even if pushing nil frame fails
			if p.OnFinish != nil {
				p.OnFinish()
			}
		}()
		defer p.PushFrame(nil) // Best effort to signal EOF
		t := NewAstiavTranscoder()
		t.volume = &s.Volume
		defer func() {
			s.lockQueue()
			if s.transcoder == t {
				s.transcoder = nil
			}
			s.unlockQueue()
		}()
		defer t.Close()
		if err := t.OpenInput(inputPath, reader); err != nil {
			LogVoice("Transcoder OpenInput failed: %v", err)
			return
		}

		s.lockQueue()
		s.transcoder = t
		s.unlockQueue()

		if err := t.SetupDecoder(); err != nil {
			LogVoice("Transcoder SetupDecoder failed: %v", err)
			return
		}
		if err := t.SetupEncoder(); err != nil {
			LogVoice("Transcoder SetupEncoder failed: %m", err)
			return
		}

		t.OnNearingEnd = func() {
			s.lockQueue()
			s.nearingEnd = true
			var next *Track
			if len(s.queue) > 0 {
				next = s.queue[0]
			} else if s.Autoplay {
				next = s.autoplayTrack
			}
			s.unlockQueue()

			if next != nil {
				s.updateNextTrackStatusIfNeeded(next)
			}
		}

		err := t.Transcode(ctx, p.PushFrame)
		if err != nil {
			LogVoice("Transcoder finished for: %s (Err: %v)", url, err)
		}
	})

	getMsg := func() string {
		s.lockQueue()
		defer s.unlockQueue()
		if s.currentTrack != nil && (s.currentTrack.Title != "" || s.currentTrack.Channel != "") {
			return fmt.Sprintf("%s · %s", s.currentTrack.Title, s.currentTrack.Channel)
		}
		return url
	}

	if s.Conn != nil {
		s.setOpusFrameProviderSafe(p)
		s.setSpeakingSafe(voice.SpeakingFlagMicrophone)

		s.lockQueue()
		if s.currentTrack != nil {
			s.currentTrack.onceStart.Do(func() {
				close(s.currentTrack.PlaybackStarted)
			})
		}
		s.unlockQueue()
	}
	select {
	case <-done:
		LogVoice("Playback finished: %s", getMsg())
	case <-ctx.Done():
		LogVoice("Playback stopped: %s", getMsg())
	case <-s.cancelCtx.Done():
		LogVoice("Global session canceled for: %s", getMsg())
		cancel()
	}
	if s.provider == p {
		s.setVoiceStatus("")
		if s.Conn != nil {
			s.setOpusFrameProviderSafe(nil)
			s.setSpeakingSafe(0)
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-s.cancelCtx.Done():
		}
	}
}

func NewStreamProvider(s *VoiceSession) *StreamProvider {
	return &StreamProvider{
		frames: make(chan []byte, 100),
		sess:   s,
	}
}

func (p *StreamProvider) SetContext(ctx context.Context) {
	p.ctx = ctx
}

func (p *StreamProvider) Close() {
	p.once.Do(func() {
		if p.OnFinish != nil {
			p.OnFinish()
		}
	})
}

func (p *StreamProvider) PushFrame(f []byte) {
	select {
	case p.frames <- f:
	case <-p.sess.cancelCtx.Done():
	case <-p.ctx.Done():
	case <-time.After(1 * time.Second):
	}
}

func (p *StreamProvider) ProvideOpusFrame() ([]byte, error) {
	p.sess.pauseMu.RLock()
	pauseChan := p.sess.pauseChan
	p.sess.pauseMu.RUnlock()

	select {
	case <-pauseChan:
	case <-p.sess.cancelCtx.Done():
		return nil, io.EOF
	case <-p.ctx.Done():
		return nil, io.EOF
	}

	if p.draining {
		target := int(SilenceDuration.Milliseconds() / 20)
		if p.silenceFrames < target {
			p.silenceFrames++
			return OpusSilence, nil
		}
		p.Close()
		return nil, io.EOF
	}

	select {
	case f := <-p.frames:
		if f == nil {
			p.draining = true
			return OpusSilence, nil
		}
		return f, nil
	case <-p.sess.cancelCtx.Done():
		p.Close()
		return nil, io.EOF
	case <-p.ctx.Done():
		p.Close()
		return nil, io.EOF
	case <-time.After(500 * time.Millisecond):
		return OpusSilence, nil
	}
}

func NewAstiavTranscoder() *AstiavTranscoder {
	return &AstiavTranscoder{
		packet:        astiav.AllocPacket(),
		frame:         astiav.AllocFrame(),
		resampleFrame: astiav.AllocFrame(),
		seekChan:      make(chan int64),
	}
}

func (t *AstiavTranscoder) Seek(offset int64, whence int) (int64, error) {
	if whence != 0 {
		return 0, errors.New("only absolute seek is supported")
	}
	select {
	case t.seekChan <- offset:
		return offset, nil
	case <-time.After(5 * time.Second):
		return 0, errors.New("transcoder busy (seek timed out)")
	}
}

func (t *AstiavTranscoder) GetTimestamp() int64 {
	return atomic.LoadInt64(&t.pts)
}

func (t *AstiavTranscoder) OpenInput(in string, r io.Reader) error {
	t.inputCtx = astiav.AllocFormatContext()
	if t.inputCtx == nil {
		return errors.New("failed to alloc ctx")
	}
	if r != nil {
		t.reader = r
		seekFunc := func(offset int64, whence int) (int64, error) {
			if whence == 2 {
				return -1, errors.New("seeking from end not supported during download")
			}
			if s, ok := r.(io.Seeker); ok {
				return s.Seek(offset, whence)
			}
			return 0, errors.New("seek not supported")
		}

		ioCtx, err := astiav.AllocIOContext(16*1024, false, func(b []byte) (int, error) {
			return t.reader.Read(b)
		}, seekFunc, nil)
		if err != nil {
			return err
		}
		t.inputCtx.SetPb(ioCtx)
		t.inputCtx.SetFlags(t.inputCtx.Flags().Add(astiav.FormatContextFlagCustomIo))

		opts := astiav.NewDictionary()
		defer opts.Free()
		opts.Set("probesize", "5000000", 0)
		opts.Set("analyzeduration", "5000000", 0)
		opts.Set("fflags", "nobuffer", 0)
		opts.Set("flags", "low_delay", 0)

		if err := t.inputCtx.OpenInput(in, nil, opts); err != nil {
			return err
		}
	} else {
		opts := astiav.NewDictionary()
		defer opts.Free()
		if strings.HasPrefix(in, "http") {
			opts.Set("reconnect", "1", 0)
			opts.Set("reconnect_at_eof", "1", 0)
			opts.Set("reconnect_streamed", "1", 0)
			opts.Set("reconnect_delay_max", "30", 0)
			opts.Set("timeout", "30000000", 0)
		}
		opts.Set("probesize", "5000000", 0)
		opts.Set("analyzeduration", "5000000", 0)
		if err := t.inputCtx.OpenInput(in, nil, opts); err != nil {
			return err
		}
	}
	if err := t.inputCtx.FindStreamInfo(nil); err != nil {
		return err
	}
	t.audioStreamIndex = -1
	for _, s := range t.inputCtx.Streams() {
		if s.CodecParameters().MediaType() == astiav.MediaTypeAudio {
			t.audioStreamIndex = s.Index()
			break
		}
	}
	if t.audioStreamIndex == -1 {
		return errors.New("no audio")
	}
	return nil
}

func (t *AstiavTranscoder) SetupDecoder() error {
	p := t.inputCtx.Streams()[t.audioStreamIndex].CodecParameters()
	d := astiav.FindDecoder(p.CodecID())
	if d == nil {
		return errors.New("no decoder")
	}
	t.decoderCtx = astiav.AllocCodecContext(d)
	_ = p.ToCodecContext(t.decoderCtx)
	return t.decoderCtx.Open(d, nil)
}

func (t *AstiavTranscoder) SetupEncoder() error {
	e := astiav.FindEncoderByName("libopus")
	if e == nil {
		e = astiav.FindEncoder(astiav.CodecIDOpus)
	}
	if e == nil {
		return errors.New("no encoder")
	}
	t.encoderCtx = astiav.AllocCodecContext(e)
	t.encoderCtx.SetBitRate(192000)
	t.encoderCtx.SetSampleRate(48000)
	t.encoderCtx.SetChannelLayout(astiav.ChannelLayoutStereo)
	t.encoderCtx.SetSampleFormat(astiav.SampleFormatS16)
	t.encoderCtx.SetTimeBase(astiav.NewRational(1, 48000))
	o := astiav.NewDictionary()
	defer o.Free()
	o.Set("vbr", "on", 0)
	o.Set("compression_level", "10", 0)
	o.Set("frame_size", "20", 0)
	if err := t.encoderCtx.Open(e, o); err != nil {
		return err
	}
	t.resampleCtx = astiav.AllocSoftwareResampleContext()
	if t.resampleCtx == nil {
		return errors.New("failed to allocate resampler")
	}
	return nil
}

func (t *AstiavTranscoder) Transcode(ctx context.Context, on func([]byte)) (err error) {
	// 1. Panic Recovery
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("transcoder panic: %v", r)
			LogVoice("CRITICAL: Transcoder panic recovered: %v", r)
		}
	}()

	// 2. Resource Cleanup
	defer t.packet.Unref()
	t.onFrame = on
	defer func() {
		if t.onFrame != nil {
			t.onFrame(nil)
		}
	}()

	fifoSize := 960 * 2
	t.fifo = astiav.AllocAudioFifo(t.encoderCtx.SampleFormat(), t.encoderCtx.ChannelLayout().Channels(), fifoSize)
	if t.fifo == nil {
		return errors.New("failed to alloc fifo")
	}
	defer func() {
		if t.fifo != nil {
			t.fifo.Free()
			t.fifo = nil
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ts := <-t.seekChan:
			if err := t.handleSeek(ts); err != nil {
				return err
			}
		default:
		}

		// 3. Reuse Packet (Unref at the end of loop or before read)
		t.packet.Unref()

		if err := t.inputCtx.ReadFrame(t.packet); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				break
			}
			return err
		}

		if t.packet.StreamIndex() != t.audioStreamIndex {
			continue
		}

		if err := t.decoderCtx.SendPacket(t.packet); err != nil {
			return err
		}

		for {
			if err := t.decoderCtx.ReceiveFrame(t.frame); err != nil {
				break
			}

			if err := t.pushToFifo(); err != nil {
				return err
			}

			t.frame.Unref()
		}

		if !t.nearingEndTriggered && t.inputCtx.Duration() > 0 {
			t.checkNearingEnd()
		}
	}

	// Flush Decoder
	if t.decoderCtx != nil {
		_ = t.decoderCtx.SendPacket(nil)
		for {
			if err := t.decoderCtx.ReceiveFrame(t.frame); err != nil {
				break
			}
			if err := t.pushToFifo(); err != nil {
				return err
			}
			t.frame.Unref()
		}
	}

	// Clear FIFO
	if err := t.processFifo(true); err != nil {
		return err
	}

	// Flush Encoder
	if t.encoderCtx != nil {
		_ = t.encoderCtx.SendFrame(nil)
		_ = t.receiveAndWrite()
	}
	return nil
}

func (t *AstiavTranscoder) receiveAndWrite() error {
	for {
		t.packet.Unref()
		if t.encoderCtx.ReceivePacket(t.packet) != nil {
			break
		}
		if t.onFrame != nil {
			d := t.packet.Data()
			fd := make([]byte, len(d))
			copy(fd, d)
			t.onFrame(fd)
		}
	}
	return nil
}

func (t *AstiavTranscoder) handleSeek(ts int64) error {
	streamTb := t.inputCtx.Streams()[t.audioStreamIndex].TimeBase()
	streamTs := astiav.RescaleQ(ts, astiav.NewRational(1, 48000), streamTb)

	var err error
	err = t.inputCtx.SeekFrame(t.audioStreamIndex, streamTs, astiav.SeekFlags(astiav.SeekFlagBackward))
	if err != nil && ts == 0 {
		err = t.inputCtx.SeekFrame(-1, 0, astiav.SeekFlags(astiav.SeekFlagBackward))
	}

	if err != nil {
		LogVoice("SeekFrame failed: %v", err)
	} else {
		if t.decoderCtx != nil {
			t.decoderCtx.Free()
		}
		if t.encoderCtx != nil {
			t.encoderCtx.Free()
		}
		if t.resampleCtx != nil {
			t.resampleCtx.Free()
		}

		if err := t.SetupDecoder(); err != nil {
			return err
		}
		if err := t.SetupEncoder(); err != nil {
			return err
		}

		if t.fifo != nil {
			t.fifo.Free()
			t.fifo = astiav.AllocAudioFifo(t.encoderCtx.SampleFormat(), t.encoderCtx.ChannelLayout().Channels(), 960*2)
		}
		atomic.StoreInt64(&t.pts, ts)
	}
	return nil
}

func (t *AstiavTranscoder) checkNearingEnd() {
	totalSecs := float64(t.inputCtx.Duration()) / 1000000.0
	currentSecs := float64(atomic.LoadInt64(&t.pts)) / 48000.0
	threshold := math.Max(7, math.Min(totalSecs*0.1, 20))
	if currentSecs > totalSecs-threshold {
		t.nearingEndTriggered = true
		if t.OnNearingEnd != nil {
			t.OnNearingEnd()
		}
	}
}

func (t *AstiavTranscoder) encodeAndWrite(f *astiav.Frame) error {
	if err := t.encoderCtx.SendFrame(f); err != nil {
		return err
	}
	return t.receiveAndWrite()
}

func (t *AstiavTranscoder) pushToFifo() error {
	t.resampleFrame.Unref()
	t.resampleFrame.SetChannelLayout(t.encoderCtx.ChannelLayout())
	t.resampleFrame.SetSampleFormat(t.encoderCtx.SampleFormat())
	t.resampleFrame.SetSampleRate(t.encoderCtx.SampleRate())
	nb := int(astiav.RescaleQ(int64(t.frame.NbSamples()), astiav.NewRational(1, t.frame.SampleRate()), astiav.NewRational(1, t.encoderCtx.SampleRate())))
	if nb > 0 {
		t.resampleFrame.SetNbSamples(nb)
		_ = t.resampleFrame.AllocBuffer(0)
		if t.resampleCtx.ConvertFrame(t.frame, t.resampleFrame) == nil {
			_, _ = t.fifo.Write(t.resampleFrame)
			return t.processFifo(false)
		}
	}
	return nil
}

func (t *AstiavTranscoder) processFifo(drain bool) error {
	if t.fifo == nil {
		return nil
	}
	for {
		sz := 960
		if t.fifo.Size() < sz {
			if !drain || t.fifo.Size() == 0 {
				return nil
			}
			sz = t.fifo.Size()
		}
		t.resampleFrame.Unref()
		t.resampleFrame.SetNbSamples(sz)
		t.resampleFrame.SetChannelLayout(t.encoderCtx.ChannelLayout())
		t.resampleFrame.SetSampleFormat(t.encoderCtx.SampleFormat())
		t.resampleFrame.SetSampleRate(t.encoderCtx.SampleRate())
		_ = t.resampleFrame.AllocBuffer(0)
		_, _ = t.fifo.Read(t.resampleFrame)

		t.frameCount++

		if t.volume != nil {
			vol := t.volume.Load()
			if vol != 100 {
				data, _ := t.resampleFrame.Data().Bytes(1)
				limit := sz * 4
				if limit > len(data) {
					limit = len(data)
				}
				for i := 0; i < limit; i += 2 {
					sample := int16(data[i]) | int16(data[i+1])<<8
					scaled := int64(sample) * int64(vol) / 100
					if scaled > 32767 {
						scaled = 32767
					} else if scaled < -32768 {
						scaled = -32768
					}
					data[i] = byte(scaled)
					data[i+1] = byte(scaled >> 8)
				}
				_ = t.resampleFrame.Data().SetBytes(data, 1)
			}
		}

		t.resampleFrame.SetPts(atomic.LoadInt64(&t.pts))
		atomic.AddInt64(&t.pts, int64(sz))
		if err := t.encodeAndWrite(t.resampleFrame); err != nil {
			return err
		}
	}
}

func (t *AstiavTranscoder) Close() {
	if t.resampleCtx != nil {
		t.resampleCtx.Free()
	}
	if t.resampleFrame != nil {
		t.resampleFrame.Free()
	}
	if t.packet != nil {
		t.packet.Free()
	}
	if t.frame != nil {
		t.frame.Free()
	}
	if t.decoderCtx != nil {
		t.decoderCtx.Free()
	}
	if t.encoderCtx != nil {
		t.encoderCtx.Free()
	}
	if t.inputCtx != nil {
		t.inputCtx.CloseInput()
		t.inputCtx.Free()
	}
}

// ===========================
// YT-DLP & Autocomplete
// ===========================

const (
	YoutubePrefix = "[YT]"
	YTMusicPrefix = "[YTM]"
)

func getYoutubePrefix() string {
	return YoutubePrefix
}

func getYTMusicPrefix() string {
	return YTMusicPrefix
}

func (vs *VoiceSystem) Search(q string) ([]SearchResult, error) {
	// 1. Check Cache
	vs.cache.RLock()
	if item, ok := vs.cache.items[q]; ok {
		if time.Now().Before(item.expiresAt) {
			vs.cache.RUnlock()
			return item.results, nil
		}
	}
	vs.cache.RUnlock()

	src, query := "ytmusic", q
	ytp, ytmp := getYoutubePrefix(), getYTMusicPrefix()
	if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytp)) {
		src, query = "youtube", strings.TrimSpace(q[len(ytp):])
	} else if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytmp)) {
		query = strings.TrimSpace(q[len(ytmp):])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2600*time.Millisecond)
	defer cancel()
	resMu := sync.Mutex{}
	var ytm, yt []SearchResult
	seen := make(map[string]bool)
	wg := sync.WaitGroup{}
	wg.Add(2)
	safeGo(func() {
		defer wg.Done()
		s := ytmusic.TrackSearch(query)
		r, _ := s.Next()
		for _, v := range r.Tracks {
			if v.VideoID == "" {
				continue
			}
			art := ""
			if len(v.Artists) > 0 {
				art = " - " + v.Artists[0].Name
			}
			resMu.Lock()
			if !seen[v.VideoID] {
				seen[v.VideoID] = true
				ytm = append(ytm, SearchResult{URL: "https://music.youtube.com/watch?v=" + v.VideoID, Title: TruncateWithPreserve(v.Title, 100, "[YTM] ", art)})
			}
			resMu.Unlock()
		}
	})
	safeGo(func() {
		defer wg.Done()
		c := ytsearch.NewClient(nil)
		r, _ := c.Search(ctx, query)
		for _, v := range r.Results {
			resMu.Lock()
			if !seen[v.VideoID] {
				seen[v.VideoID] = true
				yt = append(yt, SearchResult{URL: "https://www.youtube.com/watch?v=" + v.VideoID, Title: TruncateWithPreserve(v.Title, 100, "[YT] ", "")})
			}
			resMu.Unlock()
		}
	})
	d := make(chan struct{})
	safeGo(func() {
		wg.Wait()
		close(d)
	})
	select {
	case <-d:
	case <-time.After(2300 * time.Millisecond):
	}
	resMu.Lock()
	defer resMu.Unlock()
	var fin []SearchResult
	if src == "youtube" {
		fin = append(yt, ytm...)
	} else {
		fin = append(ytm, yt...)
	}
	if len(fin) > 25 {
		fin = fin[:25]
	}

	// 2. Update Cache (TTL 1 hour)
	if len(fin) > 0 {
		vs.cache.Lock()
		vs.cache.items[q] = cachedItem{results: fin, expiresAt: time.Now().Add(1 * time.Hour)}
		vs.cache.Unlock()
	}

	return fin, nil
}

func (vs *VoiceSystem) SearchPlaylist(q string) ([]ytdlpSearchResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ytRs, ytmRs []ytdlpSearchResult
	var ytErr, ytmErr error
	wg := sync.WaitGroup{}
	wg.Add(2)

	safeGo(func() {
		defer wg.Done()
		ytRs, ytErr = ytdlpSearchPlaylist(ctx, q, 10)
	})
	safeGo(func() {
		defer wg.Done()
		ytmRs, ytmErr = ytdlpSearchPlaylistYTM(ctx, q, 10)
	})
	wg.Wait()

	if ytErr != nil && ytmErr != nil {
		return nil, fmt.Errorf("YouTube: %v, YTM: %v", ytErr, ytmErr)
	}

	var res []ytdlpSearchResult
	seen := make(map[string]bool)
	for _, r := range ytmRs {
		if seen[r.URL] {
			continue
		}
		res = append(res, ytdlpSearchResult{Title: "[PL] " + r.Title, Uploader: r.Uploader, URL: r.URL})
		seen[r.URL] = true
	}
	for _, r := range ytRs {
		if seen[r.URL] {
			continue
		}
		res = append(res, ytdlpSearchResult{Title: "[PL] " + r.Title, Uploader: r.Uploader, URL: r.URL})
		seen[r.URL] = true
	}

	return res, nil
}

func (s *VoiceSession) resolveTrackMetadata(ctx context.Context, t *Track) error {
	if !t.NeedsResolution {
		return nil
	}

	needsSearch := !strings.HasPrefix(t.URL, "http")
	var targetDuration time.Duration

	if !needsSearch && !isYouTubeURL(t.URL) {
		likelyDRMSite := isLikelyMusicStreamingSite(t.URL)

		resultChan := make(chan metadataResult, 2)

		safeGo(func() {
			timeout := 10 * time.Second
			if likelyDRMSite {
				timeout = 3 * time.Second
			}

			ytdlpCtx, ytdlpCancel := context.WithTimeout(ctx, timeout)
			defer ytdlpCancel()

			title, uploader, id, dur, sz, err := ytdlpResolveMetadata(ytdlpCtx, t.URL)
			if err == nil {
				t.mu.Lock()
				t.TotalSize = sz
				t.mu.Unlock()
			}
			if id != "" {
				t.mu.Lock()
				if !strings.HasPrefix(t.URL, "http") {
					t.URL = "https://www.youtube.com/watch?v=" + id
				}
				t.mu.Unlock()
			}
			resultChan <- metadataResult{title, uploader, dur, "yt-dlp", err}
		})

		if likelyDRMSite {
			safeGo(func() {
				scrapeCtx, scrapeCancel := context.WithTimeout(ctx, 5*time.Second)
				defer scrapeCancel()

				title, artist, err := extractMetadataFromDRMSite(scrapeCtx, t.URL)
				resultChan <- metadataResult{title, artist, 0, "scraper", err}
			})
		}

		var ytdlpResult, scraperResult *metadataResult
		resultsReceived := 0
		expectedResults := 1
		if likelyDRMSite {
			expectedResults = 2
		}

	waitLoop:
		for resultsReceived < expectedResults {
			select {
			case res := <-resultChan:
				resultsReceived++
				if res.source == "yt-dlp" {
					ytdlpResult = &res
				} else {
					scraperResult = &res
				}

				if res.err == nil && res.title != "" {
					t.Title = res.title
					t.Channel = res.artist
					targetDuration = res.duration
					if res.artist != "" {
						t.URL = res.title + " " + res.artist
					} else {
						t.URL = res.title
					}
					needsSearch = true
					break waitLoop
				}
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1 * time.Second):
				if resultsReceived > 0 {
					break waitLoop
				}
			}
		}

		safeGo(func() {
			for resultsReceived < expectedResults {
				select {
				case <-resultChan:
					resultsReceived++
				case <-time.After(5 * time.Second):
					return
				}
			}
		})

		if !needsSearch {
			if scraperResult != nil && scraperResult.err == nil && scraperResult.title != "" {
				t.Title = scraperResult.title
				t.Channel = scraperResult.artist
				if scraperResult.artist != "" {
					t.URL = scraperResult.title + " " + scraperResult.artist
				} else {
					t.URL = scraperResult.title
				}
				needsSearch = true
			} else if ytdlpResult != nil && ytdlpResult.err != nil {
				if strings.Contains(ytdlpResult.err.Error(), "DRM") {
					LogVoice("DRM detected for %s, but scraping also failed", t.URL)
					return fmt.Errorf("DRM-protected content not supported: %s", t.URL)
				}
			}
		}
	}

	if needsSearch {
		q := t.URL
		ytp, ytmp := getYoutubePrefix(), getYTMusicPrefix()
		if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytp)) {
			q = strings.TrimSpace(q[len(ytp):])
		} else if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytmp)) {
			q = strings.TrimSpace(q[len(ytmp):])
		}
		ch := make(chan prioritizedSearchResult, 2)
		safeGo(func() {
			r, _ := ytdlpSearchYTM(ctx, q, 5)
			ch <- prioritizedSearchResult{r, 0}
		})
		safeGo(func() {
			r, _ := ytdlpSearch(ctx, q, 5)
			ch <- prioritizedSearchResult{r, 1}
		})

		var combined []ytdlpSearchResult
		resList := make([][]ytdlpSearchResult, 2)
		for range 2 {
			r := <-ch
			resList[r.prio] = r.res
		}
		combined = append(resList[0], resList[1]...)

		if len(combined) > 0 {
			best := s.SelectBestTrack(combined, t.Title, t.Channel, targetDuration)
			if strings.Contains(best.URL, "http") {
				t.URL, t.Title, t.Channel, t.Duration = best.URL, best.Title, best.Uploader, best.Duration
				s.updateNextTrackStatusIfNeeded(t)
			}
		}
	}

	if !strings.HasPrefix(t.URL, "http") {
		return errors.New("no song found")
	}
	return nil
}

func (s *VoiceSession) processTrackFile(ctx context.Context, t *Track) {
	videoID := extractVideoID(t.URL)
	isYouTube := isYouTubeURL(t.URL)

	if videoID != "" && isYouTube {
		filename := filepath.Join(AudioCacheDir, videoID+".webm")

		if t.Title == "" {
			if cm := readMetadataCache(videoID); cm != nil {
				t.mu.Lock()
				t.Title, t.Channel, t.Duration = cm.Title, cm.Channel, cm.Duration
				t.mu.Unlock()
				t.SafeCloseMetadata()
				s.updateNextTrackStatusIfNeeded(t)
			}
		}

		if t.Title == "" {
			safeGo(func() {
				// 1. Fast Path: Native Go Library (DISABLED due to hangs/crashes)
				// fastCtx, fastCancel := context.WithTimeout(ctx, 2*time.Second)
				// defer fastCancel()
				// title, uploader, dur, err := fastResolveMetadata(fastCtx, videoID)
				var err error = errors.New("fast metadata disabled")
				var title, uploader string
				var dur time.Duration

				// 2. Slow Path: yt-dlp process
				if err != nil {
					var dur2 time.Duration
					var sz2 int64
					title, uploader, _, dur2, sz2, err = ytdlpResolveMetadata(ctx, t.URL)
					if err == nil {
						t.mu.Lock()
						t.TotalSize = sz2
						t.mu.Unlock()
					}
					dur = dur2
				}

				if err == nil {
					t.mu.Lock()
					t.Title = title
					t.Channel = uploader
					t.Duration = dur
					t.mu.Unlock()
					writeMetadataCache(videoID, title, uploader, dur)
					t.SafeCloseMetadata()
					s.updateNextTrackStatusIfNeeded(t)
				} else {
					LogVoice("Background metadata fetch failed for %s: %v", t.URL, err)
					t.SafeCloseMetadata()
				}
			})
		} else {
			select {
			case <-t.MetadataReady:
			default:
				t.SafeCloseMetadata()
			}
			safeGo(func() { writeMetadataCache(videoID, t.Title, t.Channel, t.Duration) })
		}

		if _, err := os.Stat(filename); err == nil {
			t.MarkReady(filename, t.Title, t.Channel, t.Duration, nil)
			return
		}

		s.downloadAndCache(ctx, t, filename, t.URL)

		safeGo(func() {
			t.mu.Lock()
			title, ch, d := t.Title, t.Channel, t.Duration
			t.mu.Unlock()
			if title != "" {
				writeMetadataCache(videoID, title, ch, d)
			}
		})
		return
	}

	meta, err := ytdlpExtractMetadata(ctx, t.URL) // Use ctx
	if err != nil {
		t.MarkError(err)
		return
	}

	t.Title, t.Channel, t.Duration = meta.Title, meta.Uploader, meta.Duration
	s.updateNextTrackStatusIfNeeded(t)

	select {
	case <-t.MetadataReady:
	default:
		t.SafeCloseMetadata()
	}

	if meta.ID != "" {
		if strings.Contains(t.URL, "music.youtube.com") {
			t.URL = "https://music.youtube.com/watch?v=" + meta.ID
		} else {
			t.URL = "https://www.youtube.com/watch?v=" + meta.ID
		}
	}

	if _, err := os.Stat(meta.Filename); err == nil {
		t.MarkReady(meta.Filename, meta.Title, meta.Uploader, meta.Duration, nil)
		return
	}

	s.downloadAndCache(ctx, t, meta.Filename, t.URL)
}

func (s *VoiceSession) downloadAndCache(ctx context.Context, t *Track, filename, url string) {
	downloadDone := make(chan struct{})
	writeSig := make(chan struct{}, 1)
	readySig := make(chan struct{})
	errorSig := make(chan error, 1)
	onceReady := sync.Once{}
	onceError := sync.Once{}

	t.mu.Lock()
	t.FileCreated = make(chan struct{})
	t.mu.Unlock()

	// Use a cancelable context to ensure we kill yt-dlp if we time out
	ctx, dcancel := context.WithCancel(ctx)
	defer dcancel()

	safeGo(func() {
		defer close(downloadDone)
		defer func() {
			if r := recover(); r != nil {
				LogVoice("CRITICAL: downloader safeGo panic recovered: %v", r)
				onceError.Do(func() { errorSig <- fmt.Errorf("panic: %v", r) })
			}
		}()
		ensureAudioCacheDir()
		partFilename := filename + ".part"

		t.mu.Lock()
		ss := t.SeekOffset
		t.mu.Unlock()

		thresh := int64(1024 * 1024)
		if ss > 0 {
			thresh = 128 * 1024 // 128KB is enough to start transcoding a fragment
		}
		cacheFile, err := os.Create(partFilename)

		t.mu.Lock()
		if t.FileCreated != nil {
			close(t.FileCreated)
		}
		t.mu.Unlock()

		if err != nil {
			LogVoice("Failed to create cache file: %v", err)
			return
		}

		sw := &TrackSignalWriter{
			w: cacheFile,
			onWrite: func(n int) {
				t.mu.Lock()
				t.WrittenBytes += int64(n)
				wb := t.WrittenBytes
				t.mu.Unlock()
				if wb >= thresh {
					onceReady.Do(func() { close(readySig) })
				}
				select {
				case writeSig <- struct{}{}:
				default:
				}
			},
		}

		// Context is now managed by parent scope

		t.mu.Lock()
		t.downloadCancel = dcancel
		t.mu.Unlock()

		_, err = ytdlpStream(ctx, url, ss, sw)
		if err != nil {
			onceError.Do(func() { errorSig <- err })
		}
		cacheFile.Close()

		if err != nil {
			LogVoice("Stream/Cache failed for %s: %v", url, err)
			os.Remove(partFilename)
			return
		}

		onceReady.Do(func() { close(readySig) })

		if err := os.Rename(partFilename, filename); err != nil {
			LogVoice("Failed to rename cache file for %s: %v", url, err)
			os.Remove(partFilename)
		} else {
			t.mu.Lock()
			wb := t.WrittenBytes
			t.mu.Unlock()
			LogVoice("Downloaded track file: %s (Size: %d bytes)", filename, wb)
		}
	})

	totalTimer := time.NewTimer(maxTotal)
	defer totalTimer.Stop()

	stallTimer := time.NewTimer(maxConnWait)
	defer stallTimer.Stop()

loop:
	for {
		select {
		case <-readySig:
			break loop
		case <-ctx.Done():
			t.MarkError(ctx.Err())
			return
		case <-totalTimer.C:
			t.MarkError(errors.New("timeout: download too slow (max total time exceeded)"))
			return
		case <-stallTimer.C:
			t.MarkError(errors.New("timeout: download stalled or failed to start"))
			return
		case err := <-errorSig:
			t.MarkError(err)
			return
		case <-downloadDone:
			t.MarkError(errors.New("timeout: download process exited unexpectedly without data"))
			return
		case <-writeSig:
			if !stallTimer.Stop() {
				select {
				case <-stallTimer.C:
				default:
				}
			}
			stallTimer.Reset(maxStall)
		}
	}

	partFilename := filename + ".part"
	readFile, err := os.Open(partFilename)
	if err != nil {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			t.MarkError(ctx.Err())
			return
		}
		readFile, err = os.Open(partFilename)
		if err != nil {
			t.MarkError(fmt.Errorf("failed to open cache file for tailing: %w", err))
			return
		}
	}

	tr := &TailingReader{
		f:    readFile,
		done: downloadDone,
		ctx:  ctx,
		sig:  writeSig,
	}

	t.MarkReady(filename, t.Title, t.Channel, t.Duration, tr)
}

func (s *VoiceSession) addToHistory(url, title, author string) {
	s.lockQueue()
	defer s.unlockQueue()
	if title == "" {
		return
	}
	n := normalizeTitle(title, author)
	tokens := tokenize(n)

	id := extractVideoID(url)
	if id == "" {
		return
	}
	n = normalizeTitle(title, author)
	tokens = tokenize(n)

	if !slices.Contains(s.History, id) {
		s.History = append(s.History, id)
		if len(s.History) > 50 {
			s.History = s.History[1:]
		}
	}
	if n != "" {
		if !s.checkSimilarity(tokens) {
			s.HistoryTitles = append(s.HistoryTitles, n)
			s.HistoryAuthors = append(s.HistoryAuthors, author)

			uniqueTokens := make([]string, 0, len(tokens))
			seen := make(map[string]bool)
			for _, t := range tokens {
				if !seen[t] {
					seen[t] = true
					uniqueTokens = append(uniqueTokens, t)
				}
			}
			s.HistoryTokens = append(s.HistoryTokens, uniqueTokens)
			s.updateIDF(uniqueTokens, true)

			if len(s.HistoryTitles) > 50 {
				s.HistoryTitles = s.HistoryTitles[1:]
				s.HistoryAuthors = s.HistoryAuthors[1:]
				oldTokens := s.HistoryTokens[0]
				s.HistoryTokens = s.HistoryTokens[1:]
				s.updateIDF(oldTokens, false)
			}
		}
	}
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

// checkSimilarity checks if the candidate tokens are similar to any history item using cached IDF stats
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

func (s *VoiceSession) fetchRelated(url, title, artist string) (string, error) {
	id := extractVideoID(url)
	if id == "" {
		return "", errors.New("id")
	}

	ch := make(chan recResult, 2)
	safeGo(func() {
		es, _ := ytdlpExtractPlaylist(s.cancelCtx, "https://music.youtube.com/watch?v="+id+"&list=RDAMVM"+id, 20)
		ch <- recResult{es, 0}
	})
	safeGo(func() {
		es, _ := ytdlpExtractPlaylist(s.cancelCtx, "https://www.youtube.com/watch?v="+id+"&list=RD"+id, 20)
		ch <- recResult{es, 1}
	})

	var es []ytdlpPlaylistEntry
	resList := make([][]ytdlpPlaylistEntry, 2)
	for range 2 {
		r := <-ch
		resList[r.prio] = r.es
	}
	es = append(resList[0], resList[1]...)

	// Fallback: Native Search if no results
	if len(es) == 0 {
		LogVoice("Autoplay: yt-dlp returned 0 results, trying native search fallback for '%s %s'", title, artist)
		query := title
		if artist != "" {
			query += " " + artist
		}
		c := ytsearch.NewClient(nil)
		res, err := c.Search(s.cancelCtx, query)
		if err == nil && len(res.Results) > 0 {
			for _, r := range res.Results {
				vid := r.VideoID
				if vid != "" && vid != id {
					es = append(es, ytdlpPlaylistEntry{
						URL:      "https://www.youtube.com/watch?v=" + vid,
						Title:    r.Title,
						Uploader: r.Channel,
					})
				}
			}
		}
	}

	curID := extractVideoID(url)
	curTitle := curID
	if title != "" {
		curTitle = title
	}
	LogVoice("Autoplay: Found %d related tracks for %s", len(es), curTitle)

	// Get last 5 tracks for context
	s.lockQueue()
	count := len(s.HistoryTitles)
	if count > 5 {
		count = 5
	}
	historyTitles := make([]string, count)
	copy(historyTitles, s.HistoryTitles[len(s.HistoryTitles)-count:])
	idfCopy := make(map[string]int, len(s.IDFStats))
	maps.Copy(idfCopy, s.IDFStats)

	htTokens := make([][]string, len(s.HistoryTokens))
	copy(htTokens, s.HistoryTokens)
	s.unlockQueue()

	for _, e := range es {
		u := strings.TrimSpace(e.URL)
		nid := ""
		if strings.Contains(u, "watch?v=") {
			nid = extractVideoID(u)
		}

		nti, nup := strings.TrimSpace(e.Title), strings.TrimSpace(e.Uploader)
		n := normalizeTitle(nti, nup)
		tokens := tokenize(n)

		if nid == "" || nid == curID {
			continue
		}
		found := slices.Contains(s.History, nid) // Corrected from historyTitles to s.History
		if found {
			continue
		}

		if checkSimilarityAgainst(tokens, htTokens, idfCopy) {
			found = true
		}
		if found {
			continue
		}
		return u, nil
	}
	if len(es) > 1 {
		LogVoice("Autoplay: Strict filtering failed, trying fallback... %s", curTitle)
		for _, e := range es {
			u := strings.TrimSpace(e.URL)
			nid := ""
			if strings.Contains(u, "watch?v=") {
				nid = extractVideoID(u)
			}
			if nid != "" && nid != curID {
				return u, nil
			}
		}
	} else {
		LogVoice("Autoplay: Not enough tracks for fallback (Count: %d)", len(es))
	}
	return "", errors.New("none")
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

// ===========================
// yt-dlp
// ===========================

// newYtdlp returns a new yt-dlp command with a modern user agent and reliable player client
func newYtdlp() (*ytdlp.Command, func()) {
	cmd := ytdlp.New().
		Quiet().
		NoWarnings()

	if proxy := os.Getenv("YOUTUBE_PROXY"); proxy != "" {
		cmd.Proxy(proxy)
	}

	return cmd, func() {}
}

// buildYtdlpArgs returns common args for yt-dlp commands
func buildYtdlpArgs() []string {
	jsOnce.Do(func() {
		for _, rt := range []string{"node", "deno", "quickjs"} {
			if path, err := exec.LookPath(rt); err == nil {
				cachedJSArgs = append(cachedJSArgs, "--js-runtimes", rt+":"+path)
				break
			}
		}
	})

	args := append([]string(nil), cachedJSArgs...)
	args = append(args,
		"--no-playlist",
		"--no-check-certificates",
		"--no-warnings",
		"--extractor-args", "youtube:player_client=ios,android,web",
		"--prefer-free-formats",
		"--socket-timeout", "30",
		"--retries", "20",
		"--fragment-retries", "20",
	)

	if _, err := os.Stat("cookies.txt"); err == nil {
		args = append(args, "--cookies", "cookies.txt")
	} else if c := os.Getenv("YOUTUBE_COOKIES"); c != "" {
		if _, err := os.Stat(c); err == nil {
			args = append(args, "--cookies", c)
		}
	}

	return args
}

func ytdlpSearch(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		PreferFreeFormats().
		Run(ctx, append(args, "ytsearch"+fmt.Sprintf("%d", m)+":"+q)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		u := ps[0]
		if extractVideoID(u) != "" {
			rs = append(rs, ytdlpSearchResult{u, ps[1], ps[2], d})
		}
	}
	return rs, nil
}
func ytdlpSearchYTM(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, fmt.Sprintf("ytmsearch%d:%s", m, q))...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		u := ps[0]
		if extractVideoID(u) != "" {
			rs = append(rs, ytdlpSearchResult{URL: u, Title: ps[1], Uploader: ps[2], Duration: d})
		}
	}
	return rs, nil
}

func ytdlpSearchPlaylist(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	searchURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&sp=EgIQAw%%253D%%253D", url.QueryEscape(q))

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, searchURL)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 || ps[1] == "" || ps[1] == "NA" {
			continue
		}
		rs = append(rs, ytdlpSearchResult{URL: ps[0], Title: ps[1], Uploader: ps[2]})
	}
	return rs, nil
}

func ytdlpSearchPlaylistYTM(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	searchURL := fmt.Sprintf("https://music.youtube.com/search?q=%s&filter=playlists", url.QueryEscape(q))

	args := buildYtdlpArgs()
	res, err := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, searchURL)...)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	rs := make([]ytdlpSearchResult, 0, len(ls))
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 || ps[1] == "" || ps[1] == "NA" {
			continue
		}
		rs = append(rs, ytdlpSearchResult{URL: ps[0], Title: ps[1], Uploader: ps[2]})
	}
	return rs, nil
}

func ytdlpExtractMetadata(ctx context.Context, u string) (*ytdlpMetadata, error) {
	u = strings.Replace(u, "music.youtube.com", "www.youtube.com", 1)

	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	args = append(args, "-f", "bestaudio[ext=webm]/bestaudio[ext=m4a]/bestaudio/best")
	res, err := cmd.
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s\t%(id)s\t%(filename)s").
		Output(filepath.Join(AudioCacheDir, "%(id)s.%(ext)s")).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, append(args, "--skip-download", u)...)

	if err != nil {
		LogVoice("yt-dlp metadata failed: %v, stderr: %s (URL: %s)", err, res.Stderr, u)
		return nil, err
	}

	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 6 {
			continue
		}
		d, _ := time.ParseDuration(ps[3] + "s")
		return &ytdlpMetadata{URL: ps[0], Title: ps[1], Uploader: ps[2], Duration: d, ID: ps[4], Filename: ps[5]}, nil
	}
	return nil, errors.New("failed to parse metadata")
}

// isLikelyMusicStreamingSite detects music streaming sites abstractly without hardcoding specific domains
func isLikelyMusicStreamingSite(url string) bool {
	lowerURL := strings.ToLower(url)

	musicPathPatterns := []string{
		"/track/", "/tracks/",
		"/album/", "/albums/",
		"/song/", "/songs/",
		"/playlist/", "/playlists/",
		"/artist/", "/artists/",
		"/music/",
	}

	for _, pattern := range musicPathPatterns {
		if strings.Contains(lowerURL, pattern) {
			return true
		}
	}

	musicSubdomains := []string{
		"music.", "play.", "listen.", "stream.",
	}

	for _, subdomain := range musicSubdomains {
		if strings.Contains(lowerURL, "://"+subdomain) || strings.Contains(lowerURL, "://www."+subdomain) {
			return true
		}
	}

	return false
}

func ytdlpStream(ctx context.Context, u string, ss time.Duration, out io.Writer) (*ytdlpMetadata, error) {
	u = strings.Replace(u, "music.youtube.com", "www.youtube.com", 1)

	cmd, cleanup := newYtdlp()
	defer cleanup()

	proxy := os.Getenv("YOUTUBE_PROXY")

	args := buildYtdlpArgs()
	args = append(args, "--ignore-config")
	if ss > 0 {
		args = append(args, "--ss", fmt.Sprintf("%.3f", ss.Seconds()))
	}
	execCmd := cmd.
		Format("bestaudio[ext=webm]/bestaudio[ext=m4a]/bestaudio/best").
		Output("-").
		NoSimulate().
		NoPart().
		NoPlaylist().
		NoCheckCertificates().
		BuildCommand(ctx, append(args, u)...)

	execCmd.Stdout = out
	execCmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	if proxy != "" {
		execCmd.Env = append(execCmd.Env, "http_proxy="+proxy, "https_proxy="+proxy, "all_proxy="+proxy)
	}
	execCmd.WaitDelay = 0

	var stderr bytes.Buffer
	execCmd.Stderr = &stderr

	if err := execCmd.Start(); err != nil {
		return nil, err
	}

	if err := execCmd.Wait(); err != nil {
		msg := strings.ToLower(err.Error() + stderr.String())
		if strings.Contains(msg, "broken pipe") || strings.Contains(msg, "signal: killed") {
			return &ytdlpMetadata{}, nil
		}
		LogVoice("yt-dlp stream failed: %v, stderr: %s", err, stderr.String())
		LogVoice("yt-dlp exited with error: %v", err)
		return nil, err
	}

	return &ytdlpMetadata{}, nil
}

func ytdlpResolveMetadata(ctx context.Context, u string) (string, string, string, time.Duration, int64, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := append(buildYtdlpArgs(), "--skip-download")
	res, err := cmd.
		Print("%(title)s\t%(uploader)s\t%(duration)s\t%(id)s\t%(filesize,filesize_approx)s").
		NoSimulate().
		IgnoreConfig().
		NoWarnings().
		Run(ctx, append(args, u)...)

	if err != nil {
		stderr := strings.ToLower(res.Stderr)
		if strings.Contains(stderr, "drm") {
			return "", "", "", 0, 0, fmt.Errorf("DRM: %w", err)
		}
		return "", "", "", 0, 0, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[2] + "s")
		sz := int64(0)
		if len(ps) >= 5 {
			fmt.Sscanf(ps[4], "%d", &sz)
		}
		return ps[0], ps[1], ps[3], d, sz, nil
	}
	return "", "", "", 0, 0, errors.New("failed to resolve metadata")
}

func (s *VoiceSession) enrichTrackMetadata(ctx context.Context, t *Track) {
	t.mu.Lock()
	if t.Enriched || t.URL == "" {
		t.mu.Unlock()
		return
	}
	u := t.URL
	t.mu.Unlock()

	ectx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := append(buildYtdlpArgs(), "--skip-download", "--get-thumbnail")
	res, err := cmd.Run(ectx, append(args, u)...)
	if err != nil {
		return
	}

	thumb := strings.TrimSpace(res.Stdout)
	if thumb != "" {
		t.mu.Lock()
		t.ArtworkURL = thumb
		t.Enriched = true
		t.mu.Unlock()
	}
}

func ytdlpExtractPlaylist(ctx context.Context, u string, m int) ([]ytdlpPlaylistEntry, error) {
	cmd, cleanup := newYtdlp()
	defer cleanup()

	args := buildYtdlpArgs()
	res := cmd.
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(id)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		BuildCommand(ctx, append(args, u, "--yes-playlist")...)

	var stdout, stderr bytes.Buffer
	res.Stdout = &stdout
	res.Stderr = &stderr

	if err := res.Run(); err != nil {
		return nil, fmt.Errorf("yt-dlp playlist failed: %w, stderr: %s", err, stderr.String())
	}

	rawOutput := strings.TrimSpace(stdout.String())
	ls := strings.Split(rawOutput, "\n")

	es := make([]ytdlpPlaylistEntry, 0)
	isYouTube := isYouTubeURL(u) || strings.Contains(u, "music.youtube.com")

	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 {
			continue
		}
		url := ps[0]
		title := ps[1]
		uploader := ps[2]

		if isYouTube && len(ps) >= 4 {
			id := ps[3]
			if id != "" && id != "NA" {
				url = "https://www.youtube.com/watch?v=" + id
			}
		}

		es = append(es, ytdlpPlaylistEntry{URL: url, Title: title, Uploader: uploader})
	}
	return es, nil
}

// extractMetadataFromDRMSite attempts to scrape metadata from DRM-protected sites
func extractMetadataFromDRMSite(ctx context.Context, url string) (title, artist string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body := new(strings.Builder)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	linesRead := 0
	for scanner.Scan() && linesRead < 500 {
		body.WriteString(scanner.Text())
		body.WriteString(" ")
		linesRead++
		if strings.Contains(scanner.Text(), "</head>") {
			break
		}
	}

	htmlContent := body.String()

	titleRegex := regexp.MustCompile(`<meta[^>]*property=["']og:title["'][^>]*content=["']([^"']+)["']`)
	if matches := titleRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		title = matches[1]
		if idx := strings.Index(title, " - song and lyrics by"); idx != -1 {
			title = title[:idx]
		}
		if idx := strings.Index(title, " | Spotify"); idx != -1 {
			title = title[:idx]
		}
	}

	descRegex := regexp.MustCompile(`<meta[^>]*property=["']og:description["'][^>]*content=["']([^"']+)["']`)
	if matches := descRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		desc := matches[1]
		if strings.Contains(strings.ToLower(url), "spotify") {
			parts := strings.Split(desc, " · ")
			if len(parts) >= 1 {
				artist = strings.TrimSpace(parts[0])
			}
		}
	}

	if title == "" {
		return "", "", errors.New("could not extract metadata")
	}

	return title, artist, nil
}

// ===========================
// Command Handlers
// ===========================

// handleMusicPlay handles play interactions for music commands.
func handleMusicPlay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	q, m, p, a, l := parsePlayArguments(data)

	_ = event.DeferCreateMessage(false)
	if strings.HasPrefix(strings.ToUpper(q), "[PL]") {
		qBody := strings.TrimSpace(q[4:])
		if qBody != "" && !strings.Contains(qBody, "http") {
			rs, err := GetVoiceManager().SearchPlaylist(qBody)
			if err == nil && len(rs) > 0 {
				q = rs[0].URL
			}
		}
	}

	if err := startPlayback(event, q, m, a, l, p); err != nil {
		_ = EditInteractionV2(*event.Client(), event, "Failed: "+err.Error())
	}
}

// parsePlayArguments parses the arguments for the play command.
func parsePlayArguments(data discord.SlashCommandInteractionData) (q, m string, p int, a, l bool) {
	q, _ = data.OptString("query")
	qv, _ := data.OptString("queue")
	a, _ = data.OptBool("autoplay")
	l, _ = data.OptBool("loop")

	if qv == "now" {
		m = "now"
	} else if qv == "next" {
		m = "next"
	} else if qv != "" {
		p, _ = strconv.Atoi(qv)
	}
	return
}

// handleMusicStop handles stop interactions for music commands.
func handleMusicStop(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	LogVoice("User %s (%s) stopped playback in guild %s", event.User().Username, event.User().ID, *event.GuildID())
	GetVoiceManager().Leave(context.Background(), *event.GuildID())
	_ = RespondInteractionV2(*event.Client(), event, "🛑 Stopped and disconnected.", false)
}

// handleMusicQueue handles queue interactions for music commands.
func handleMusicQueue(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	_ = event.DeferCreateMessage(true)

	s := GetVoiceManager().GetSession(*event.GuildID())
	if s == nil {
		_ = EditInteractionV2(*event.Client(), event, "Not playing anything.")
		return
	}

	s.lockQueue()
	defer s.unlockQueue()

	var components []any

	if s.currentTrack != nil {
		s.currentTrack.mu.Lock()
		title, url, channel, art := s.currentTrack.Title, s.currentTrack.URL, s.currentTrack.Channel, s.currentTrack.ArtworkURL
		s.currentTrack.mu.Unlock()

		components = append(components, NewTextDisplay("**Now Playing:**"))
		components = append(components, NewTextDisplay(fmt.Sprintf("[%s](%s) · %s", title, url, channel)))
		if art != "" {
			components = append(components, NewMediaGallery(art))
		}
		components = append(components, NewSeparator(true))
	}

	components = append(components, NewTextDisplay("**Queue:**"))
	if len(s.queue) == 0 {
		if s.Autoplay && s.autoplayTrack != nil {
			components = append(components, NewTextDisplay("_Empty (Autoplay Ready)_"))
		} else {
			components = append(components, NewTextDisplay("_Empty_"))
		}
	} else {
		var qList strings.Builder
		for i, t := range s.queue {
			if i >= 10 {
				qList.WriteString(fmt.Sprintf("\n*...and %d more*", len(s.queue)-10))
				break
			}
			qList.WriteString(fmt.Sprintf("`%d.` [%s](%s)\n", i+1, t.Title, t.URL))
		}
		components = append(components, NewTextDisplay(qList.String()))
	}

	if s.Autoplay {
		components = append(components, NewSeparator(true))
		if s.autoplayTrack != nil {
			s.autoplayTrack.mu.Lock()
			atitle, aurl, achannel, aart := s.autoplayTrack.Title, s.autoplayTrack.URL, s.autoplayTrack.Channel, s.autoplayTrack.ArtworkURL
			s.autoplayTrack.mu.Unlock()

			components = append(components, NewTextDisplay("**Autoplay:** Enabled (Ready)"))
			components = append(components, NewTextDisplay(fmt.Sprintf("**Next Up:** [%s](%s) · %s", atitle, aurl, achannel)))
			if aart != "" {
				components = append(components, NewMediaGallery(aart))
			}
		} else {
			components = append(components, NewTextDisplay("**Autoplay:** Enabled"))
		}
	}

	if err := EditInteractionContainerV2(*event.Client(), event, NewV2Container(components...)); err != nil {
		LogWarn("Failed to edit interaction: %v", err)
	}
}

// handleVoicePanel handles the /voice panel command
func handleVoicePanel(event *events.ApplicationCommandInteractionCreate) {
	userID := event.User().ID
	guildID := event.GuildID()
	if guildID == nil {
		_ = RespondInteractionV2(*event.Client(), event, "Not in a guild.", true)
		return
	}

	VoicePanelsMu.Lock()
	if p, ok := VoicePanels[userID]; ok {
		if time.Now().Before(p.ExpiresAt) {
			VoicePanelsMu.Unlock()
			_ = RespondInteractionV2(*event.Client(), event, "You already have an active panel! Dismiss it or wait for it to expire.", true)
			return
		}
		delete(VoicePanels, userID)
	}
	VoicePanelsMu.Unlock()

	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = RespondInteractionV2(*event.Client(), event, "Not playing anything.", true)
		return
	}

	_ = event.DeferCreateMessage(true)

	VoicePanelsMu.Lock()
	VoicePanels[userID] = &VoicePanel{
		UserID:    userID,
		GuildID:   *guildID,
		Token:     event.Token(),
		AppID:     event.ApplicationID(),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}
	VoicePanelsMu.Unlock()

	UpdateVoicePanels(*guildID, *event.Client())
}

func BuildVoicePanelContainer(s *VoiceSession) Container {
	s.lockQueue()
	defer s.unlockQueue()

	var components []any

	if s.currentTrack != nil {
		s.currentTrack.mu.Lock()
		title, url, channel, art := s.currentTrack.Title, s.currentTrack.URL, s.currentTrack.Channel, s.currentTrack.ArtworkURL
		s.currentTrack.mu.Unlock()

		if title == "" {
			title = "Track"
		}

		components = append(components, NewTextDisplay("**Now Playing:**"))
		components = append(components, NewTextDisplay(fmt.Sprintf("[%s](%s) · %s", title, url, channel)))
		if art != "" {
			components = append(components, NewMediaGallery(art))
		}
	} else {
		components = append(components, NewTextDisplay("**Nothing is currently playing.**"))
	}

	paused := false
	s.pauseMu.RLock()
	select {
	case <-s.pauseChan:
		// Not paused
	default:
		paused = true
	}
	s.pauseMu.RUnlock()

	statusEmoji := "⏸️"
	statusText := "Playing"
	if paused {
		statusEmoji = "▶️"
		statusText = "Paused"
	}

	options := ""
	if s.Looping {
		options += " 🔄 Loop"
	}
	if s.Autoplay {
		options += " 🔀 Autoplay"
	}

	components = append(components, NewTextDisplay(fmt.Sprintf("**Status:** %s %s | **Volume:** %d%% %s", statusEmoji, statusText, s.Volume.Load(), options)))
	components = append(components, NewTextDisplay(fmt.Sprintf("**Queue:** %d tracks remaining", len(s.queue))))

	components = append(components, NewSeparator(true))

	// Control Buttons
	playPauseEmoji := "⏸️ Pause"
	if paused {
		playPauseEmoji = "▶️ Resume"
	}

	row1 := discord.NewActionRow(
		discord.NewButton(discord.ButtonStyleSecondary, playPauseEmoji, "voice:panel:playpause", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "⏭️ Skip", "voice:panel:skip", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "⏹️ Stop", "voice:panel:stop", "", 0),
		discord.NewButton(discord.ButtonStyleDanger, "❌ Close", "voice:panel:close", "", 0),
	)

	row2 := discord.NewActionRow(
		discord.NewButton(discord.ButtonStylePrimary, "🔄 Loop", "voice:panel:loop", "", 0),
		discord.NewButton(discord.ButtonStylePrimary, "🔀 Autoplay", "voice:panel:autoplay", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "➖ Vol", "voice:panel:voldown", "", 0),
		discord.NewButton(discord.ButtonStyleSecondary, "➕ Vol", "voice:panel:volup", "", 0),
	)

	components = append(components, row1, row2)

	return NewV2Container(components...)
}

func UpdateVoicePanels(guildID snowflake.ID, cl bot.Client) {
	VoicePanelsMu.Lock()
	defer VoicePanelsMu.Unlock()

	s := GetVoiceManager().GetSession(guildID)

	var container Container
	if s == nil {
		container = NewV2Container(NewTextDisplay("The music session has ended."), discord.NewActionRow(discord.NewButton(discord.ButtonStyleDanger, "❌ Close", "voice:panel:close", "", 0)))
		// Fallback to finding at least one panel to get AppID/Client if needed?
		// But usually we have a client.
	} else {
		s.SetClient(cl) // Update the session's client if it's still active
		container = BuildVoicePanelContainer(s)
	}

	now := time.Now()
	for userID, panel := range VoicePanels {
		if panel.GuildID != guildID {
			continue
		}
		if now.After(panel.ExpiresAt) {
			delete(VoicePanels, userID)
			continue
		}

		// Use the client passed to the function, which is guaranteed to be valid
		// or the one from the session if it exists.
		safeGo(func() {
			func(token string, appID snowflake.ID, c Container, client bot.Client) {
				_ = EditInteractionContainerV2ByToken(client, appID, token, c)
			}(panel.Token, panel.AppID, container, cl)
		}) // Pass cl directly

		if s == nil {
			delete(VoicePanels, userID)
		}
	}
}

func handleVoiceComponent(event *events.ComponentInteractionCreate) {
	customID := event.Data.CustomID()
	if !strings.HasPrefix(customID, "voice:panel:") {
		return
	}

	action := strings.TrimPrefix(customID, "voice:panel:")
	guildID := event.GuildID()
	if guildID == nil {
		return
	}

	switch action {
	case "close":
		VoicePanelsMu.Lock()
		delete(VoicePanels, event.User().ID)
		VoicePanelsMu.Unlock()
		_ = event.UpdateMessage(discord.NewMessageUpdate().WithContent("Panel closed.").ClearComponents())
		return
	}

	s := GetVoiceManager().GetSession(*guildID)
	if s == nil {
		_ = event.CreateMessage(discord.NewMessageCreate().WithContent("Music session is no longer active.").WithEphemeral(true))
		return
	}

	switch action {
	case "playpause":
		s.pauseMu.Lock()
		isClosed := false
		select {
		case <-s.pauseChan:
			isClosed = true
		default:
		}
		if isClosed {
			s.pauseChan = make(chan struct{})
		} else {
			close(s.pauseChan)
		}
		s.pauseMu.Unlock()
		s.RefreshStatus()
	case "skip":
		_, _ = s.Skip()
	case "stop":
		GetVoiceManager().Leave(context.Background(), *guildID)
	case "loop":
		s.lockQueue()
		s.Looping = !s.Looping
		s.unlockQueue()
	case "autoplay":
		s.lockQueue()
		s.Autoplay = !s.Autoplay
		s.unlockQueue()
	case "volup":
		v := s.Volume.Load()
		if v < 200 {
			s.Volume.Store(v + 10)
		}
	case "voldown":
		v := s.Volume.Load()
		if v > 0 {
			s.Volume.Store(v - 10)
		}
	}

	UpdateVoicePanels(*guildID, *event.Client())
	_ = event.DeferUpdateMessage()
}

// handleMusicAutocomplete handles autocomplete interactions for music commands.
func handleMusicAutocomplete(event *events.AutocompleteInteractionCreate) {
	f := event.Data.Focused()
	if f.Name == "queue" {
		v, cs := f.String(), []discord.AutocompleteChoice{discord.AutocompleteChoiceString{Name: "Play Now", Value: "now"}, discord.AutocompleteChoiceString{Name: "Play Next", Value: "next"}}
		if v != "" {
			if _, err := strconv.Atoi(v); err == nil {
				cs = append([]discord.AutocompleteChoice{discord.AutocompleteChoiceString{Name: "Position: " + v, Value: v}}, cs...)
			}
		}
		_ = event.AutocompleteResult(cs)
		return
	}
	if f.Name != "query" {
		return
	}
	q := f.String()
	if q == "" {
		q = getRandomRecommendation(event.GuildID())
	} else if strings.Contains(q, "http") {
		_ = event.AutocompleteResult(nil)
		return
	}
	var rs []SearchResult
	var err error
	if strings.HasPrefix(strings.ToUpper(q), "[PL]") {
		qBody := strings.TrimSpace(q[4:])
		if qBody != "" {
			ytdlpRS, searchErr := GetVoiceManager().SearchPlaylist(qBody)
			if searchErr == nil {
				for _, entry := range ytdlpRS {
					rs = append(rs, SearchResult{
						Title:       entry.Title,
						ChannelName: entry.Uploader,
						URL:         entry.URL,
					})
				}
			} else {
				err = searchErr
			}
		}
	} else {
		rs, err = GetVoiceManager().Search(q)
	}
	if err != nil {
		_ = event.AutocompleteResult(nil)
		return
	}
	var cs []discord.AutocompleteChoice
	for i, r := range rs {
		if i >= 25 {
			break
		}
		n := r.Title
		if len(n) > 100 {
			n = n[:97] + "..."
		}
		v := r.URL
		if len(v) > 100 {
			v = r.Title
			if len(v) > 100 {
				v = v[:100]
			}
		}
		cs = append(cs, discord.AutocompleteChoiceString{Name: n, Value: v})
	}
	_ = event.AutocompleteResult(cs)
}

// getRandomRecommendation gets a random recommendation from the guild's history.
func getRandomRecommendation(guildID *snowflake.ID) string {
	if guildID != nil {
		if s := GetVoiceManager().GetSession(*guildID); s != nil {
			s.lockQueue()
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
						s.unlockQueue()
						return "Mix - " + author
					}
				}

				title := s.HistoryTitles[idx]
				s.unlockQueue()
				return "Mix - " + title
			}
			s.unlockQueue()
		}
	}

	return "Trending Music"
}

// startPlayback initiates voice playback for a user's query
func startPlayback(ev *events.ApplicationCommandInteractionCreate, q, m string, a, l bool, p int) error {
	LogVoice("User %s (%s) requested playback: %s", ev.User().Username, ev.User().ID, q)
	vs, ok := ev.Client().Caches.VoiceState(*ev.GuildID(), ev.User().ID)
	if !ok || vs.ChannelID == nil {
		return errors.New("user not in a voice channel")
	}
	vm := GetVoiceManager()
	s := vm.Prepare(*ev.Client(), *ev.GuildID(), *vs.ChannelID)
	s.lockQueue()
	s.Autoplay, s.Looping = a, l
	s.unlockQueue()
	je := make(chan error, 1)
	safeGo(func() { je <- vm.Join(context.Background(), *ev.Client(), *ev.GuildID(), *vs.ChannelID) })
	t, count, err := vm.Play(context.Background(), *ev.GuildID(), q, m, p)
	if err != nil {
		return err
	}
	if err := <-je; err != nil {
		return err
	}

	err = finishPlaybackResponse(ev, t, m, s.Autoplay, s.Looping, p, count)
	UpdateVoicePanels(*ev.GuildID(), *ev.Client())
	return err
}

// finishPlaybackResponse sends the final response message after playback starts
func finishPlaybackResponse(ev *events.ApplicationCommandInteractionCreate, t *Track, m string, a, l bool, p int, count int) error {
	t.mu.Lock()
	title := t.Title
	url := t.URL
	channel := t.Channel
	t.mu.Unlock()

	if title == "" || strings.HasPrefix(title, "http") {
		if id := extractVideoID(url); id != "" {
			title = "YouTube Track (" + id + ")"
		} else {
			title = "Music Track"
		}
	}

	pr := "Added to queue:"
	if count > 1 {
		pr = fmt.Sprintf("📂 Added **%d** tracks from playlist to queue:", count)
		switch m {
		case "now":
			pr = fmt.Sprintf("▶️ Playing Now (Cleared queue and added **%d** tracks from playlist):", count)
		case "next":
			pr = fmt.Sprintf("⏭️ Added **%d** tracks to play next:", count)
		}
	} else {
		switch m {
		case "next":
			pr = "⏭️ Next up:"
		case "now":
			pr = "▶️ Playing Now (Skipped Current):"
		}
		if p > 0 {
			pr = "Added to queue at position " + strconv.Itoa(p) + ":"
		}
	}
	var ss []string
	if a {
		ss = append(ss, "Autoplay")
	}
	if l {
		ss = append(ss, "Looping")
	}
	s := ""
	if len(ss) > 0 {
		s = " (" + strings.Join(ss, ", ") + ": Enabled)"
	}
	c := pr + " [" + title + "](" + url + ")"
	if channel != "" && channel != "NA" {
		c += " · " + channel
	}
	c += s

	t.mu.Lock()
	art := t.ArtworkURL
	t.mu.Unlock()

	if art != "" {
		return EditInteractionContainerV2(*ev.Client(), ev, NewV2Container(NewTextDisplay(c), NewMediaGallery(art)))
	}
	return EditInteractionV2(*ev.Client(), ev, c)
}

// SelectBestTrack scoring system to pick official audios from search results
func (s *VoiceSession) SelectBestTrack(results []ytdlpSearchResult, targetTitle, targetChannel string, targetDuration time.Duration) ytdlpSearchResult {
	if len(results) == 0 {
		return ytdlpSearchResult{}
	}
	best := results[0]
	maxScore := -100.0
	var corpus []string
	corpus = append(corpus, targetTitle)
	for _, res := range results {
		corpus = append(corpus, normalizeTitle(res.Title, ""))
	}
	weights := calculateTFIDF(corpus)

	for _, res := range results {
		score := 0.0
		if targetDuration > 0 && res.Duration > 0 {
			diff := math.Abs(float64(targetDuration - res.Duration))
			if diff < 2.5*float64(time.Second) {
				score += 100
			} else if diff < 6*float64(time.Second) {
				score += 40
			}
		}
		lowCh := strings.ToLower(res.Uploader)
		targetCh := strings.ToLower(targetChannel)
		if targetCh != "" {
			if lowCh == targetCh {
				score += 80
			} else if strings.Contains(lowCh, targetCh) {
				score += 30
			}
		}
		if targetTitle != "" {
			if weightedSimilarity(normalizeTitle(res.Title, ""), normalizeTitle(targetTitle, ""), weights) {
				score += 50
			}
		}

		if score > maxScore {
			maxScore = score
			best = res
		}
	}
	return best
}

// extractVideoID extracts the video ID from a YouTube-related URL.
func extractVideoID(u string) string {
	u = strings.TrimSpace(u)
	// Handle basic Youtu.be (shortener)
	if strings.Contains(u, "youtu.be/") {
		parts := strings.Split(u, "youtu.be/")
		if len(parts) >= 2 {
			// Trim query parameters
			return strings.Split(parts[1], "?")[0]
		}
	}

	// Handle standard URL via net/url
	// We prepend https:// if missing to ensure parsing works
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err == nil {
		// Standard v=ID
		if v := parsed.Query().Get("v"); v != "" {
			return v
		}
		// id=ID
		if id := parsed.Query().Get("id"); id != "" {
			return id
		}
		// Shorts/Embed/V
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
	// Fallback to hashing if no ID found or ID looks invalid
	return ""
}

// isYouTubeURL checks if a URL is a YouTube URL.
func isYouTubeURL(u string) bool {
	return extractVideoID(u) != "" || strings.Contains(u, "youtube.com") || strings.Contains(u, "youtu.be") || strings.Contains(u, "google.com/url")
}

// normalizeTitle normalizes a title by removing metadata blocks and converting to lowercase.
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

// calculateTFIDF calculates the TF-IDF weights for a corpus of strings.
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

// weightedSimilarity checks if two strings are similar using TF-IDF weights.
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

// ===========================
// Priority Queue for Downloads
// ===========================

type PriorityQueue []*Track

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

// ===========================
// Download Scheduler
// ===========================

func (s *VoiceSession) scheduleDownload(t *Track) {
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()

	if t.Downloaded || t.Started || t.index != 0 {
		return
	}

	heap.Push(s.pendingDownloads, t)
	s.downloadCond.Signal()
}

func (s *VoiceSession) downloadLoop() {
	defer func() {
		if r := recover(); r != nil {
			LogVoice("CRITICAL: downloadLoop panic recovered: %v", r)
		}
	}()
	maxConcurrent := 3
	for {
		s.downloadMu.Lock()
		for s.pendingDownloads.Len() == 0 || s.activeDownloads >= maxConcurrent {
			select {
			case <-s.cancelCtx.Done():
				s.downloadMu.Unlock()
				return
			default:
			}
			s.downloadCond.Wait()
		}

		item := heap.Pop(s.pendingDownloads)
		t := item.(*Track)
		s.activeDownloads++
		s.downloadMu.Unlock()
		safeGo(func() {
			func(track *Track) {
				defer func() {
					s.downloadMu.Lock()
					s.activeDownloads--
					s.downloadCond.Signal()
					s.downloadMu.Unlock()
				}()

				track.mu.Lock()
				if track.Started {
					track.mu.Unlock()
					return
				}
				track.Started = true
				track.mu.Unlock()

				ctx, cancel := context.WithCancel(s.cancelCtx)
				track.cancel = cancel

				if err := s.resolveTrackMetadata(ctx, track); err != nil {
					track.MarkError(err)
					return
				}

				safeGo(func() { s.enrichTrackMetadata(s.cancelCtx, track) })

				s.processTrackFile(ctx, track)
			}(t)
		})
	}
}

func (s *VoiceSession) GetClient() bot.Client {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.client
}

func (s *VoiceSession) SetClient(cl bot.Client) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	s.client = cl
}
