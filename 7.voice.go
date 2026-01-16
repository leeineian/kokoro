package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	// 1. Cleanup old cache on startup
	if err := os.RemoveAll(AudioCacheDir); err != nil {
		fmt.Printf("Failed to clean audio cache: %v\n", err)
	}
	// 2. Ensure cache directory exists
	if err := os.MkdirAll(AudioCacheDir, 0755); err != nil {
		fmt.Printf("Failed to create audio cache dir: %v\n", err)
	}

	astiav.SetLogLevel(astiav.LogLevelFatal)

	OnClientReady(func(ctx context.Context, client *bot.Client) {
		RegisterDaemon(LogVoice, func(ctx context.Context) (bool, func(), func()) {
			return true, func() {}, func() {
				if VoiceManager != nil {
					LogVoice("Shutting down voice manager...")
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
		},
	}, handleVoice)

	RegisterAutocompleteHandler("voice", handleMusicAutocomplete)
}

// ===========================
// Constants
// ===========================

const AudioCacheDir = ".tracks"

var (
	VoiceManager *VoiceSystem
	OnceVoice    sync.Once

	metadataBlockRegex = regexp.MustCompile(`[\(\[\{].*?[\)\]\}]`)
	camelCaseRegex     = regexp.MustCompile(`([a-z])([A-Z])`)
)

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
	}
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
		_ = event.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("Seek failed: %v", err)})
		return
	}
	action := "Forwarded"
	if factor < 0 {
		action = "Rewound"
	}
	_ = event.CreateMessage(discord.MessageCreate{Content: fmt.Sprintf("%s %v", action, d)})
}

// ... existing code ...

// ===========================
// Voice Manager
// ===========================

// VoiceSystem manages all voice sessions across guilds
type VoiceSystem struct {
	mu       sync.Mutex
	sessions map[snowflake.ID]*VoiceSession
}

// GetVoiceManager returns the singleton VoiceSystem instance
func GetVoiceManager() *VoiceSystem {
	OnceVoice.Do(func() {
		VoiceManager = &VoiceSystem{sessions: make(map[snowflake.ID]*VoiceSession)}
	})
	return VoiceManager
}

// GetSession retrieves the voice session for a guild
func (vs *VoiceSystem) GetSession(guildID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.sessions[guildID]
}

// Prepare creates or retrieves a voice session for a guild
func (vs *VoiceSystem) Prepare(client *bot.Client, guildID, channelID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if sess, ok := vs.sessions[guildID]; ok {
		sess.channelMu.RLock()
		currentChannelID := sess.ChannelID
		sess.channelMu.RUnlock()
		if currentChannelID == channelID {
			return sess
		}
		// Clear status on old channel before stopping/replacing session
		route := rest.NewEndpoint(http.MethodPut, "/channels/"+currentChannelID.String()+"/voice-status")
		_ = client.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
		sess.Stop()

	}
	ctx, cancel := context.WithCancel(context.Background())
	sess := &VoiceSession{
		GuildID:     guildID,
		ChannelID:   channelID,
		Conn:        client.VoiceManager.CreateConn(guildID),
		cancelCtx:   ctx,
		cancelFunc:  cancel,
		queue:       make([]*Track, 0),
		downloadSem: make(chan struct{}, 3),
		client:      client,
		statusChan:  make(chan string, 10),
	}
	sess.queueCond = sync.NewCond(&sess.queueMu)
	sess.joinedCond = sync.NewCond(&sess.joinedMu)
	sess.pausedCond = sync.NewCond(&sess.pausedMu)
	// Track goroutines for cleanup
	sess.goroutineWg.Add(1)
	go func() {
		defer sess.goroutineWg.Done()
		sess.statusManager()
	}()
	vs.sessions[guildID] = sess
	return sess
}

// Join connects the bot to a voice channel
func (vs *VoiceSystem) Join(ctx context.Context, client *bot.Client, guildID, channelID snowflake.ID) error {

	LogVoice("Joining channel %s in guild %s", channelID, guildID)
	sess := vs.Prepare(client, guildID, channelID)
	sess.joinedMu.Lock()
	if sess.joined {
		sess.joinedMu.Unlock()
		return nil
	}
	sess.joinedMu.Unlock()
	if err := sess.Conn.Open(ctx, channelID, false, false); err != nil {
		LogVoice("Failed to connect to voice in guild %s: %v", guildID, err)
		// Clear status on failure
		route := rest.NewEndpoint(http.MethodPut, "/channels/"+channelID.String()+"/voice-status")
		_ = client.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)

		sess.Conn.Close(ctx)
		vs.mu.Lock()
		delete(vs.sessions, guildID)
		vs.mu.Unlock()
		sess.cancelFunc()
		return err
	}
	sess.joinedMu.Lock()
	sess.joined = true

	sess.joinedCond.Broadcast()
	sess.joinedMu.Unlock()
	// Track processQueue goroutine
	sess.goroutineWg.Add(1)
	go func() {
		defer sess.goroutineWg.Done()
		sess.processQueue()
	}()
	return nil
}

// Leave disconnects the bot from a voice channel
func (vs *VoiceSystem) Leave(ctx context.Context, guildID snowflake.ID) {
	vs.mu.Lock()
	sess, ok := vs.sessions[guildID]
	if !ok {
		vs.mu.Unlock()
		return
	}
	delete(vs.sessions, guildID)
	vs.mu.Unlock()

	sess.channelMu.RLock()
	channelID := sess.ChannelID
	sess.channelMu.RUnlock()

	route := rest.NewEndpoint(http.MethodPut, "/channels/"+channelID.String()+"/voice-status")
	_ = sess.client.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)

	sess.Stop()
	if sess.Conn != nil {
		sess.Conn.Close(ctx)
	}
}

// Shutdown gracefully stops all voice sessions and clears their status
func (vs *VoiceSystem) Shutdown(ctx context.Context) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	var wg sync.WaitGroup
	for id, sess := range vs.sessions {
		wg.Add(1)
		go func(s *VoiceSession) {
			defer wg.Done()
			s.channelMu.RLock()
			channelID := s.ChannelID
			s.channelMu.RUnlock()

			route := rest.NewEndpoint(http.MethodPut, "/channels/"+channelID.String()+"/voice-status")
			_ = s.client.Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
			s.Stop()
		}(sess)
		delete(vs.sessions, id)
	}
	wg.Wait()
}

// Play adds a track to the queue and starts playback
func (vs *VoiceSystem) Play(ctx context.Context, guildID snowflake.ID, url, mode string, pos int) (*Track, error) {
	s := vs.GetSession(guildID)
	if s == nil {
		return nil, errors.New("not connected to voice")
	}
	t := NewTrack(url)
	LogVoice("Queuing track in guild %s: %s", guildID, url)

	s.queueMu.Lock()
	if mode == "now" {
		for _, qt := range s.queue {
			qt.Cleanup()
		}
		s.queue = []*Track{t}
		s.skipLoop = true
		if s.currentTrack != nil {
			s.currentTrack.Cleanup()
		}
		s.currentTrack = nil
		if s.autoplayTrack != nil {
			s.autoplayTrack.Cleanup()
			s.autoplayTrack = nil
		}
		if s.streamCancel != nil {
			s.streamCancel()
		}
	} else if mode == "next" {
		s.queue = append([]*Track{t}, s.queue...)
	} else if pos > 0 {
		idx := pos - 1
		if idx >= len(s.queue) {
			s.queue = append(s.queue, t)
		} else {
			s.queue = append(s.queue, nil)
			copy(s.queue[idx+1:], s.queue[idx:])
			s.queue[idx] = t
		}
	} else {
		s.queue = append(s.queue, t)
	}
	s.queueCond.Signal()
	s.queueMu.Unlock()
	go s.downloadTrack(t)
	s.addToHistory(url, "", "")
	return t, nil
}

// onVoiceStateUpdate handles voice state changes and auto-disconnect
func (vs *VoiceSystem) onVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	vs.mu.Lock()
	s, ok := vs.sessions[event.VoiceState.GuildID]
	vs.mu.Unlock()

	// Handle bot moves/disconnects
	if event.VoiceState.UserID == event.Client().ID() {
		if event.VoiceState.ChannelID == nil {
			if ok {
				LogVoice("Bot disconnected by external event in guild %s", event.VoiceState.GuildID)
				vs.Leave(context.Background(), event.VoiceState.GuildID)
			}
			return
		}

		if !ok {
			return
		}

		s.channelMu.RLock()
		currentChannelID := s.ChannelID
		s.channelMu.RUnlock()

		if currentChannelID == 0 || *event.VoiceState.ChannelID != currentChannelID {
			oldChannelID := currentChannelID
			LogVoice("Bot moved from %s to %s in guild %s", oldChannelID, *event.VoiceState.ChannelID, event.VoiceState.GuildID)

			// Clear old status if it existed
			if oldChannelID != 0 {
				route := rest.NewEndpoint(http.MethodPut, "/channels/"+oldChannelID.String()+"/voice-status")
				_ = event.Client().Rest.Do(route.Compile(nil), map[string]string{"status": ""}, nil)
			}

			// Update session and state
			s.channelMu.Lock()
			s.ChannelID = *event.VoiceState.ChannelID
			s.channelMu.Unlock()

			// Update status in new channel
			s.statusMu.Lock()
			status := s.lastStatus
			s.statusMu.Unlock()
			s.setVoiceStatus(status)
			return
		}
		return
	}

	if !ok {
		return
	}

	s.channelMu.RLock()
	currentChannelID := s.ChannelID
	s.channelMu.RUnlock()

	if currentChannelID == 0 {
		return
	}
	humanCount := 0
	for state := range event.Client().Caches.VoiceStates(event.VoiceState.GuildID) {
		if state.ChannelID != nil && *state.ChannelID == currentChannelID && state.UserID != event.Client().ID() {
			if m, ok := event.Client().Caches.Member(event.VoiceState.GuildID, state.UserID); !ok || !m.User.Bot {
				humanCount++
			}
		}
	}
	s.pausedMu.Lock()
	paused := s.paused
	s.pausedMu.Unlock()
	if humanCount == 0 && !paused {
		LogVoice("Pausing playback in guild %s (No humans)", event.VoiceState.GuildID)
		s.pausedMu.Lock()
		s.paused = true
		s.pausedCond.Broadcast()
		s.pausedMu.Unlock()
		s.statusMu.Lock()
		status := s.lastStatus
		s.statusMu.Unlock()
		if status != "" {
			if strings.HasPrefix(status, "🎶 ") {
				status = "⏸️ " + status[len("🎶 "):]
			} else if strings.HasPrefix(status, "⏩ ") {
				status = "⏸️ " + status[len("⏩ "):]
			} else {
				status = "⏸️ " + status
			}
			s.setVoiceStatus(status)
		} else {
			s.setVoiceStatus("⏸️ Paused")
		}
	} else if humanCount > 0 && paused {
		LogVoice("Resuming playback in guild %s", event.VoiceState.GuildID)
		s.pausedMu.Lock()
		s.paused = false
		s.pausedCond.Broadcast()
		s.pausedMu.Unlock()
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

// VoiceSession represents an active voice connection for a guild
type VoiceSession struct {
	GuildID                snowflake.ID
	ChannelID              snowflake.ID
	channelMu              sync.RWMutex
	Conn                   voice.Conn
	queue                  []*Track
	queueMu                sync.Mutex
	queueCond              *sync.Cond
	joined                 bool
	joinedMu               sync.Mutex
	joinedCond             *sync.Cond
	downloadSem            chan struct{}
	cancelCtx              context.Context
	cancelFunc             context.CancelFunc
	Autoplay, Looping      bool
	History, HistoryTitles []string
	streamCancel           context.CancelFunc
	provider               *StreamProvider
	client                 *bot.Client
	currentTrack           *Track
	lastStatus             string
	paused                 bool
	pausedMu               sync.Mutex
	pausedCond             *sync.Cond
	skipLoop               bool
	autoplayTrack          *Track
	statusMu               sync.Mutex
	statusChan             chan string
	goroutineWg            sync.WaitGroup    // Tracks active goroutines for cleanup
	nearingEnd             bool              // True if current track is nearing finish
	transcoder             *AstiavTranscoder // Active transcoder for seeking
}

// ... existing WaitJoined and Stop methods ...

// Seek seeks the current track to a relative offset
func (s *VoiceSession) Seek(duration time.Duration) error {
	s.queueMu.Lock()
	if s.transcoder == nil {
		s.queueMu.Unlock()
		return errors.New("not playing or transcoding")
	}
	t := s.transcoder
	var trackDuration time.Duration
	if s.currentTrack != nil {
		trackDuration = s.currentTrack.Duration
	}
	s.queueMu.Unlock()

	current := t.GetTimestamp() // This is in 48kHz samples
	// Convert duration to 48kHz samples
	offset := int64(duration.Milliseconds()) * 48
	target := current + offset

	if trackDuration > 0 {
		maxSamples := int64(trackDuration.Seconds() * 48000)
		if target > maxSamples {
			target = maxSamples
		}
	}

	if target < 0 {
		target = 0
	}
	// We send absolute 48kHz timestamp to transcoder
	// The transcoder loop will handle conversion to stream timebase
	_, err := t.Seek(target, 0)
	return err
}

// WaitJoined waits for the bot to join the voice channel
func (s *VoiceSession) WaitJoined(ctx context.Context) error {
	// Start a goroutine to broadcast when context is canceled
	// This prevents deadlock when Wait() is blocking and context gets canceled
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.joinedCond.Broadcast()
		case <-s.cancelCtx.Done():
			s.joinedCond.Broadcast()
		case <-done:
		}
	}()

	s.joinedMu.Lock()
	defer s.joinedMu.Unlock()
	for !s.joined {
		// Check context before waiting
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.cancelCtx.Done():
			return errors.New("session closed")
		default:
		}
		s.joinedCond.Wait()
		// Check context after waking up
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.cancelCtx.Done():
			return errors.New("session closed")
		default:
		}
	}
	return nil
}

// Stop stops playback and clears the queue
func (s *VoiceSession) Stop() {
	s.skipLoop = true
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
	if s.streamCancel != nil {
		s.streamCancel()
	}
	if s.Conn != nil {
		s.setOpusFrameProviderSafe(nil)
		s.Conn.SetSpeaking(context.TODO(), 0)
	}
	s.queueMu.Lock()
	for _, t := range s.queue {
		t.Cleanup()
	}
	s.queue = nil
	if s.currentTrack != nil {
		s.currentTrack.Cleanup()
	}
	s.currentTrack = nil
	if s.autoplayTrack != nil {
		s.autoplayTrack.Cleanup()
	}
	s.autoplayTrack = nil

	// Wake up any waiting goroutines
	s.queueCond.Broadcast()
	s.joinedCond.Broadcast()
	s.pausedCond.Broadcast()
	s.queueMu.Unlock()

	s.setVoiceStatus("")
}

// WaitForCleanup waits for all session goroutines to exit
func (s *VoiceSession) WaitForCleanup() {
	s.goroutineWg.Wait()
}

// setVoiceStatus updates the voice channel status message
func (s *VoiceSession) setVoiceStatus(status string) {
	select {
	case s.statusChan <- status:
	default:
	}
}

// statusManager manages the voice channel status updates
func (s *VoiceSession) statusManager() {
	var cur string
	next := ""
	hasNext := false
	t := time.NewTimer(0)
	if !t.Stop() {
		<-t.C
	}
	for {
		select {
		case <-s.cancelCtx.Done():
			return
		case n := <-s.statusChan:
			next = n
			hasNext = true
		drain:
			for {
				select {
				case n := <-s.statusChan:
					next = n
				default:
					break drain
				}
			}
			if next == cur {
				hasNext = false
				continue
			}
			t.Reset(500 * time.Millisecond)
		case <-t.C:
			if !hasNext {
				continue
			}
			s.statusMu.Lock()
			target := next
			if len([]rune(target)) > 128 {
				target = TruncateCenter(target, 128)
			}
			if target != "" && !strings.HasPrefix(target, "⏸️") {
				s.lastStatus = target
			}
			s.channelMu.RLock()
			channelID := s.ChannelID
			s.channelMu.RUnlock()
			err := s.client.Rest.Do(rest.NewEndpoint(http.MethodPut, "/channels/"+channelID.String()+"/voice-status").Compile(nil), map[string]string{"status": target}, nil)
			if err == nil {
				cur = next
				hasNext = false
			} else {
				// Failed! Retry after a delay
				LogVoice("Failed to update status for %s: %v (retrying...)", channelID, err)
				t.Reset(1 * time.Second)
			}
			s.statusMu.Unlock()
		}
	}
}

func (s *VoiceSession) updateNextTrackStatusIfNeeded(t *Track) {
	s.queueMu.Lock()
	isCurrent := s.currentTrack == t
	isNext := false
	if len(s.queue) > 0 && s.queue[0] == t {
		isNext = true
	} else if s.Autoplay && s.autoplayTrack == t {
		isNext = true
	}
	nearing := s.nearingEnd
	looping := s.Looping
	s.queueMu.Unlock()

	if (isCurrent || (isNext && nearing)) && !looping && t.Title != "" {
		sep := ""
		if t.Channel != "" && t.Channel != "NA" {
			sep = " · "
		}
		if t.Title != "Loading..." {
			if isNext && nearing {
				LogVoice("Next Track: %s%s%s (%s)", t.Title, sep, t.Channel, t.URL)
			}
			s.setVoiceStatus(TruncateWithPreserve(t.Title, 128, "⏩ ", sep+t.Channel))
		} else {
			s.setVoiceStatus(TruncateWithPreserve(t.Title, 128, "⏩ ", ""))
		}
	}
}

// setOpusFrameProviderSafe sets the opus frame provider safely, recovering from any potential panics
func (s *VoiceSession) setOpusFrameProviderSafe(provider voice.OpusFrameProvider) {
	if s.Conn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			LogVoice("Recovered from panic in SetOpusFrameProvider: %v", r)
		}
	}()
	s.Conn.SetOpusFrameProvider(provider)
}

// processQueue processes tracks from the queue and handles playback
func (s *VoiceSession) processQueue() {
	// Start a goroutine to broadcast when context is canceled
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-s.cancelCtx.Done():
			s.queueCond.Broadcast()
		case <-done:
		}
	}()

	for {
		s.queueMu.Lock()
		for len(s.queue) == 0 {
			// Check if canceled before waiting
			select {
			case <-s.cancelCtx.Done():
				s.queueMu.Unlock()
				return
			default:
			}
			s.queueCond.Wait()
			// Check if canceled after waking up
			select {
			case <-s.cancelCtx.Done():
				s.queueMu.Unlock()
				return
			default:
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
		s.queueMu.Unlock()

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
		LogVoice("Playing track: %s · %s (%s)", t.Title, t.Channel, t.URL)
		sep := ""
		if t.Channel != "" {
			sep = " · "
		}
		s.setVoiceStatus(TruncateWithPreserve(t.Title, 128, "🎶 ", sep+t.Channel))
		s.addToHistory(t.URL, t.Title, t.Channel)

		s.queueMu.Lock()
		autoplay := s.Autoplay
		s.queueMu.Unlock()
		if autoplay {
			go func(url string) {

				next, err := s.fetchRelated(url)
				if err == nil && next != "" {
					nt := NewTrack(next)
					shouldDownload := false
					s.queueMu.Lock()
					if s.Autoplay && s.currentTrack != nil && s.currentTrack.URL == url {
						if s.autoplayTrack != nil {
							s.autoplayTrack.Cleanup()
						}
						s.autoplayTrack = nt
						shouldDownload = true
					}
					s.queueMu.Unlock()
					if shouldDownload && nt != nil {
						s.downloadTrack(nt)
					}
				}
			}(t.URL)
		}

		if t.LiveStream != nil {
			s.streamCommon(t.Title, t.LiveStream)
		} else {
			s.streamFile(t.Path)
		}

		s.setVoiceStatus("") // Clear status as soon as playback ends

		s.queueMu.Lock()
		loop := s.Looping && !s.skipLoop
		s.skipLoop = false
		if loop {
			s.queue = append([]*Track{t}, s.queue...)
			s.queueMu.Unlock()
			continue
		}
		s.queueMu.Unlock()
		if t.Path != "" {
			os.Remove(t.Path)
		}

		s.queueMu.Lock()
		if len(s.queue) == 0 && s.Autoplay {
			if s.autoplayTrack != nil {
				next := s.autoplayTrack
				s.autoplayTrack = nil
				s.queue = append(s.queue, next)
				s.queueMu.Unlock()
				s.queueCond.Signal()
				continue
			} else {
				s.queueMu.Unlock()
				// This call blocks and could take seconds; status is cleared above
				next, err := s.fetchRelated(t.URL)
				if err == nil && next != "" {
					_, _ = GetVoiceManager().Play(context.Background(), s.GuildID, next, "", 0)
				}
				continue
			}
		}
		if len(s.queue) == 0 {
			s.currentTrack = nil
			s.autoplayTrack = nil
			s.queueMu.Unlock()
			// Status already cleared at top of cleanup
		} else {
			s.queueMu.Unlock()
		}
	}
}

// ===========================
// Track
// ===========================

// Track represents a music track in the queue
type Track struct {
	URL, Path, Title, Channel string
	Duration                  time.Duration
	Downloaded                bool
	Error                     error
	NeedsResolution           bool      // True if URL needs to be resolved/searched (not a direct link)
	LiveStream                io.Reader // Non-nil for streaming (not cached)
	done                      chan struct{}
	mu                        sync.Mutex
	cancel                    context.CancelFunc
}

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
		_ = os.Remove(t.Path)
	}
}

// SignalWriter wraps an io.Writer and signals a channel on every successful write
type SignalWriter struct {
	w   io.Writer
	sig chan struct{}
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

// TailingReader reads from a file that is being written to effectively decoupling download speed from playback speed
type TailingReader struct {
	f    *os.File
	done chan struct{}
	ctx  context.Context
	sig  chan struct{}
}

func (r *TailingReader) Read(p []byte) (int, error) {
	for {
		// Attempt to read available data
		n, err := r.f.Read(p)
		if n > 0 {
			return n, nil
		}
		// If real error (not EOF), return it
		if err != io.EOF {
			return n, err
		}

		// If EOF, check if we are done downloading
		select {
		case <-r.done:
			// Download is finished.
			// Perform one final read to ensure any data flushed just before close is consumed.
			n2, err2 := r.f.Read(p)
			if n2 > 0 {
				return n2, nil
			}
			if err2 != nil && err2 != io.EOF {
				return n2, err2
			}
			// Truly done
			return 0, io.EOF
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.sig:
			// New data available signal received, loop back to read
			continue
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
	t := &Track{URL: url, Title: "Loading...", done: make(chan struct{})}
	// Flag as needing resolution/search if not a direct YouTube link
	if !strings.HasPrefix(url, "http") || (!strings.Contains(url, "youtube.com") && !strings.Contains(url, "youtu.be")) {
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
	close(t.done)
}

// ===========================
// Transcoding & Helpers
// ===========================

func (s *VoiceSession) streamFile(path string) {
	s.streamCommon(path, nil)
}

func (s *VoiceSession) streamCommon(input string, reader io.Reader) {

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
	go func() {
		defer cancel()
		defer p.PushFrame(nil)
		t := NewAstiavTranscoder()
		defer func() {
			s.queueMu.Lock()
			if s.transcoder == t {
				s.transcoder = nil
			}
			s.queueMu.Unlock()
		}()
		defer t.Close()
		if err := t.OpenInput(input, reader); err != nil {
			LogVoice("Transcoder OpenInput failed: %v", err)
			return
		}

		s.queueMu.Lock()
		s.transcoder = t
		s.queueMu.Unlock()

		if err := t.SetupDecoder(); err != nil {
			LogVoice("Transcoder SetupDecoder failed: %v", err)
			return
		}
		if err := t.SetupEncoder(); err != nil {
			LogVoice("Transcoder SetupEncoder failed: %v", err)
			return
		}

		t.OnNearingEnd = func() {
			s.queueMu.Lock()
			s.nearingEnd = true
			var next *Track
			if len(s.queue) > 0 {
				next = s.queue[0]
			} else if s.Autoplay {
				next = s.autoplayTrack
			}
			s.queueMu.Unlock()

			if next != nil {
				s.updateNextTrackStatusIfNeeded(next)
			}
		}

		err := t.Transcode(ctx, p.PushFrame)
		if err != nil {
			LogVoice("Transcoder finished for: %s (Err: %v)", input, err)
		}
	}()
	s.queueMu.Lock()
	curT := s.currentTrack
	s.queueMu.Unlock()

	msg := input
	if curT != nil {
		msg = fmt.Sprintf("%s · %s", curT.Title, curT.Channel)
	}

	if s.Conn != nil {
		s.setOpusFrameProviderSafe(p)
		s.Conn.SetSpeaking(context.TODO(), voice.SpeakingFlagMicrophone)
	}
	select {
	case <-done:
		LogVoice("Playback finished: %s", msg)
	case <-ctx.Done():
		LogVoice("Playback stopped: %s", msg)
	case <-s.cancelCtx.Done():
		LogVoice("Global session canceled for: %s", msg)
		cancel()
	}
	if s.provider == p {
		s.setVoiceStatus("")
		if s.Conn != nil {
			s.setOpusFrameProviderSafe(nil)
			s.Conn.SetSpeaking(context.TODO(), 0)
		}
	}
}

type StreamProvider struct {
	frames   chan []byte
	OnFinish func()
	once     sync.Once
	sess     *VoiceSession
}

func NewStreamProvider(s *VoiceSession) *StreamProvider {
	return &StreamProvider{frames: make(chan []byte, 100), sess: s}
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
	}
}

func (p *StreamProvider) ProvideOpusFrame() ([]byte, error) {
	// Start a goroutine to broadcast when context is canceled
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-p.sess.cancelCtx.Done():
			p.sess.pausedCond.Broadcast()
		case <-done:
		}
	}()

	p.sess.pausedMu.Lock()
	for p.sess.paused {
		// Check if canceled before waiting
		select {
		case <-p.sess.cancelCtx.Done():
			p.sess.pausedMu.Unlock()
			return nil, io.EOF
		default:
		}
		p.sess.pausedCond.Wait()
		// Check if canceled after waking up
		select {
		case <-p.sess.cancelCtx.Done():
			p.sess.pausedMu.Unlock()
			return nil, io.EOF
		default:
		}
	}
	p.sess.pausedMu.Unlock()
	select {
	case f := <-p.frames:
		if f == nil {

			p.Close()
			return nil, io.EOF
		}
		return f, nil
	case <-p.sess.cancelCtx.Done():
		p.Close()
		return nil, io.EOF
	case <-time.After(100 * time.Millisecond):
		return nil, nil // Silence
	}
}

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
}

func NewAstiavTranscoder() *AstiavTranscoder {
	return &AstiavTranscoder{packet: astiav.AllocPacket(), frame: astiav.AllocFrame(), resampleFrame: astiav.AllocFrame(), seekChan: make(chan int64)}
}

func (t *AstiavTranscoder) Seek(offset int64, whence int) (int64, error) {
	if whence != 0 {
		return 0, errors.New("only absolute seek is supported")
	}
	select {
	case t.seekChan <- offset:
		return offset, nil
	case <-time.After(5 * time.Second): // Wait up to 5 seconds for the transcoder loop to pick it up
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
			return 0, errors.New("seek not supported")
		}
		if s, ok := r.(io.Seeker); ok {
			seekFunc = s.Seek
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
		opts.Set("probesize", "10000000", 0)
		opts.Set("analyzeduration", "10000000", 0)

		if err := t.inputCtx.OpenInput("", nil, opts); err != nil {
			return err
		}
	} else {
		var opts *astiav.Dictionary
		if strings.HasPrefix(in, "http") {
			opts = astiav.NewDictionary()
			defer opts.Free()
			opts.Set("reconnect", "1", 0)
			opts.Set("reconnect_at_eof", "1", 0)
			opts.Set("reconnect_streamed", "1", 0)
			opts.Set("reconnect_delay_max", "30", 0)
			opts.Set("timeout", "30000000", 0)
		}
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
	// Initialize resampler context
	t.resampleCtx = astiav.AllocSoftwareResampleContext()
	if t.resampleCtx == nil {
		return errors.New("failed to allocate resampler")
	}
	// Note: Resampler will be initialized dynamically in Transcode() based on input frame properties
	// This is necessary because we don't know the input format until we receive the first frame
	return nil
}

func (t *AstiavTranscoder) Transcode(ctx context.Context, on func([]byte)) error {
	defer t.packet.Unref()
	t.onFrame = on
	defer func() {
		if t.onFrame != nil {
			t.onFrame(nil)
		}
	}()
	t.fifo = astiav.AllocAudioFifo(t.encoderCtx.SampleFormat(), t.encoderCtx.ChannelLayout().Channels(), 960*2)
	defer func() {
		if t.fifo != nil {
			t.fifo.Free()
			t.fifo = nil
		}
	}()
	for {
		// Check for seek request
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ts := <-t.seekChan:
			// ts is in 48kHz timebase. We need to rescale to input stream timebase.
			streamTb := t.inputCtx.Streams()[t.audioStreamIndex].TimeBase()
			streamTs := astiav.RescaleQ(ts, astiav.NewRational(1, 48000), streamTb)

			// Seek in input
			var err error
			err = t.inputCtx.SeekFrame(t.audioStreamIndex, streamTs, astiav.SeekFlags(astiav.SeekFlagBackward))
			if err != nil && ts == 0 {
				// Fallback seek to start of file if specific stream seek fails
				err = t.inputCtx.SeekFrame(-1, 0, astiav.SeekFlags(astiav.SeekFlagBackward))
			}

			if err != nil {
				LogVoice("SeekFrame failed: %v", err)
			} else {
				// Hard Reset: Free and Re-create codecs to clear internal buffers
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
					LogVoice("Seek recovery failed (decoder): %v", err)
					return err
				}
				if err := t.SetupEncoder(); err != nil {
					LogVoice("Seek recovery failed (encoder): %v", err)
					return err
				}

				// Clear FIFO
				if t.fifo != nil {
					t.fifo.Free()
					t.fifo = astiav.AllocAudioFifo(t.encoderCtx.SampleFormat(), t.encoderCtx.ChannelLayout().Channels(), 960*2)
				}

				// Update internal PTS to match new timestamp (ts is already 48kHz)
				atomic.StoreInt64(&t.pts, ts)
			}
		default:
		}

		if err := t.inputCtx.ReadFrame(t.packet); err != nil {
			if errors.Is(err, astiav.ErrEof) {
				break
			}
			return err
		}
		if t.packet.StreamIndex() != t.audioStreamIndex {
			t.packet.Unref()
			continue
		}
		if err := t.decoderCtx.SendPacket(t.packet); err != nil {
			t.packet.Unref()
			return err
		}
		t.packet.Unref()
		for {
			if err := t.decoderCtx.ReceiveFrame(t.frame); err != nil {
				break
			}
			// ConvertFrame automatically initializes the resampler based on input/output frame properties
			t.resampleFrame.Unref()
			t.resampleFrame.SetChannelLayout(t.encoderCtx.ChannelLayout())
			t.resampleFrame.SetSampleFormat(t.encoderCtx.SampleFormat())
			t.resampleFrame.SetSampleRate(t.encoderCtx.SampleRate())
			nb := int(astiav.RescaleQ(int64(t.frame.NbSamples()), astiav.NewRational(1, t.frame.SampleRate()), astiav.NewRational(1, t.encoderCtx.SampleRate())))
			if nb > 0 {
				t.resampleFrame.SetNbSamples(nb)
				_ = t.resampleFrame.AllocBuffer(0)
				_ = t.resampleCtx.ConvertFrame(t.frame, t.resampleFrame)
				_, _ = t.fifo.Write(t.resampleFrame)
				for t.fifo.Size() >= 960 {
					t.resampleFrame.Unref()
					t.resampleFrame.SetNbSamples(960)
					t.resampleFrame.SetChannelLayout(t.encoderCtx.ChannelLayout())
					t.resampleFrame.SetSampleFormat(t.encoderCtx.SampleFormat())
					t.resampleFrame.SetSampleRate(t.encoderCtx.SampleRate())
					_ = t.resampleFrame.AllocBuffer(0)
					_, _ = t.fifo.Read(t.resampleFrame)
					t.resampleFrame.SetPts(atomic.LoadInt64(&t.pts))
					atomic.AddInt64(&t.pts, 960)
					_ = t.encodeAndWrite(t.resampleFrame)
				}
			}
			t.frame.Unref()
		}

		// Nearing end detection
		if !t.nearingEndTriggered && t.inputCtx.Duration() > 0 {
			totalSecs := float64(t.inputCtx.Duration()) / 1000000.0
			currentSecs := float64(atomic.LoadInt64(&t.pts)) / 48000.0
			// Trigger at 10% of track, min 7s, max 20s
			threshold := math.Max(7, math.Min(totalSecs*0.1, 20))
			if currentSecs > totalSecs-threshold {
				t.nearingEndTriggered = true
				if t.OnNearingEnd != nil {
					t.OnNearingEnd()
				}
			}
		}
	}

	// 1. Flush Decoder
	if t.decoderCtx != nil {
		_ = t.decoderCtx.SendPacket(nil)
		for {
			if err := t.decoderCtx.ReceiveFrame(t.frame); err != nil {
				break
			}
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
				}
			}
			t.frame.Unref()
		}
	}

	// 2. Clear FIFO
	if t.fifo != nil {
		for t.fifo.Size() > 0 {
			t.resampleFrame.Unref()
			sz := 960
			if t.fifo.Size() < sz {
				sz = t.fifo.Size()
			}
			t.resampleFrame.SetNbSamples(sz)
			t.resampleFrame.SetChannelLayout(t.encoderCtx.ChannelLayout())
			t.resampleFrame.SetSampleFormat(t.encoderCtx.SampleFormat())
			t.resampleFrame.SetSampleRate(t.encoderCtx.SampleRate())
			_ = t.resampleFrame.AllocBuffer(0)
			_, _ = t.fifo.Read(t.resampleFrame)
			t.resampleFrame.SetPts(atomic.LoadInt64(&t.pts))
			atomic.AddInt64(&t.pts, int64(sz))
			_ = t.encodeAndWrite(t.resampleFrame)
		}
	}

	// 3. Flush Encoder
	if t.encoderCtx != nil {
		_ = t.encoderCtx.SendFrame(nil)
		for {
			p := astiav.AllocPacket()
			if t.encoderCtx.ReceivePacket(p) != nil {
				p.Free()
				break
			}
			if t.onFrame != nil {
				d := p.Data()
				fd := make([]byte, len(d))
				copy(fd, d)
				t.onFrame(fd)
			}
			p.Free()
		}
	}
	return nil
}

func (t *AstiavTranscoder) encodeAndWrite(f *astiav.Frame) error {
	if err := t.encoderCtx.SendFrame(f); err != nil {
		return err
	}
	for {
		p := astiav.AllocPacket()
		if t.encoderCtx.ReceivePacket(p) != nil {
			p.Free()
			break
		}
		if t.onFrame != nil {
			d := p.Data()
			fd := make([]byte, len(d))
			copy(fd, d)
			t.onFrame(fd)
		}
		p.Free()
	}
	return nil
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

func getYoutubePrefix() string {
	if GlobalConfig != nil && GlobalConfig.YoutubePrefix != "" {
		return GlobalConfig.YoutubePrefix
	}
	return "[YT]"
}

func getYTMusicPrefix() string {
	if GlobalConfig != nil && GlobalConfig.YTMusicPrefix != "" {
		return GlobalConfig.YTMusicPrefix
	}
	return "[YTM]"
}

func (vs *VoiceSystem) Search(q string) ([]SearchResult, error) {
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
	go func() {
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
	}()
	go func() {
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
	}()
	d := make(chan struct{})
	go func() {
		wg.Wait()
		close(d)
	}()
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
	return fin, nil
}

type SearchResult struct{ Title, ChannelName, URL string }

func (s *VoiceSession) downloadTrack(t *Track) {
	ctx, cancel := context.WithCancel(s.cancelCtx)
	t.cancel = cancel
	// Note: No defer cancel() here, context is owned by the track and canceled via t.Cancel()

	if t.NeedsResolution {
		needsSearch := !strings.HasPrefix(t.URL, "http")
		var targetDuration time.Duration
		if !needsSearch && !strings.Contains(t.URL, "youtu") {
			// Option 1: Detect likely DRM/music streaming sites abstractly
			// Check for common patterns: /track/, /album/, /playlist/, /song/, music subdomains, etc.
			likelyDRMSite := isLikelyMusicStreamingSite(t.URL)

			// Option 2 & 3: Parallel execution with timeout
			type metadataResult struct {
				title    string
				artist   string
				duration time.Duration
				source   string
				err      error
			}

			resultChan := make(chan metadataResult, 2)

			// Launch yt-dlp metadata resolution with timeout
			go func() {
				// Option 3: Shorter timeout for suspected DRM sites
				timeout := 10 * time.Second
				if likelyDRMSite {
					timeout = 3 * time.Second
				}

				ytdlpCtx, ytdlpCancel := context.WithTimeout(s.cancelCtx, timeout)
				defer ytdlpCancel()

				title, uploader, id, dur, err := ytdlpResolveMetadata(ytdlpCtx, t.URL)
				if id != "" {
					t.mu.Lock()
					if !strings.HasPrefix(t.URL, "http") {
						t.URL = "https://www.youtube.com/watch?v=" + id
					}
					t.mu.Unlock()
				}
				resultChan <- metadataResult{title, uploader, dur, "yt-dlp", err}
			}()

			// Launch page scraping in parallel (Option 2)
			if likelyDRMSite {
				go func() {
					scrapeCtx, scrapeCancel := context.WithTimeout(s.cancelCtx, 5*time.Second)
					defer scrapeCancel()

					title, artist, err := extractMetadataFromDRMSite(scrapeCtx, t.URL)
					resultChan <- metadataResult{title, artist, 0, "scraper", err}
				}()
			}

			// Wait for the first successful result or both to fail
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

					// If we got a successful result, use it immediately
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
				case <-s.cancelCtx.Done():
					return
				case <-time.After(1 * time.Second):
					// Don't wait too long if one branch is stuck but we have results
					if resultsReceived > 0 {
						break waitLoop
					}
				}
			}

			// Drain remaining results in background to avoid blocking providers
			go func() {
				for resultsReceived < expectedResults {
					select {
					case <-resultChan:
						resultsReceived++
					case <-time.After(5 * time.Second):
						return
					}
				}
			}()

			// If no successful result yet, check what we have
			if !needsSearch {
				// Check scraper result first (if available)
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
					// Check if yt-dlp failed with DRM error
					if strings.Contains(ytdlpResult.err.Error(), "DRM") {
						LogVoice("DRM detected for %s, but scraping also failed", t.URL)
						t.MarkError(fmt.Errorf("DRM-protected content not supported: %s", t.URL))
						return
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
			type searchResult struct {
				res  []ytdlpSearchResult
				prio int
			}
			ch := make(chan searchResult, 2)
			go func() {
				r, _ := ytdlpSearchYTM(s.cancelCtx, q, 5)
				ch <- searchResult{r, 0}
			}()
			go func() {
				r, _ := ytdlpSearch(s.cancelCtx, q, 5)
				ch <- searchResult{r, 1}
			}()

			var combined []ytdlpSearchResult
			// Collect both, prioritizing YTM by putting it first in the slice if available
			resList := make([][]ytdlpSearchResult, 2)
			for i := 0; i < 2; i++ {
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
			t.MarkError(errors.New("no song found"))
			return
		}
	}

	// Always download/verify local cache
	s.downloadSem <- struct{}{}
	defer func() { <-s.downloadSem }()

	// Skip the metadata resolution and go straight to streaming
	var meta *ytdlpMetadata
	skipMetadataResolution := t.Title != "" && t.Title != "Loading..." && strings.Contains(t.URL, "youtube.com")

	if skipMetadataResolution {
		// Extract video ID and construct cache filename
		videoID := extractVideoID(t.URL)
		if videoID == "" {
			t.MarkError(errors.New("failed to extract video ID"))
			return
		}

		meta = &ytdlpMetadata{
			Title:    t.Title,
			Uploader: t.Channel,
			Duration: t.Duration,
			ID:       videoID,
			Filename: filepath.Join(AudioCacheDir, videoID+".webm"),
		}
	} else {

		var err error
		meta, err = ytdlpExtractMetadata(s.cancelCtx, t.URL)
		if err != nil {
			t.MarkError(err)
			return
		}

		t.Title, t.Channel, t.Duration = meta.Title, meta.Uploader, meta.Duration
		s.updateNextTrackStatusIfNeeded(t)

		if meta.ID != "" {
			// Preserve music.youtube.com domain if originally present
			if strings.Contains(t.URL, "music.youtube.com") {
				t.URL = "https://music.youtube.com/watch?v=" + meta.ID
			} else {
				t.URL = "https://www.youtube.com/watch?v=" + meta.ID
			}
		}
	}

	// 2. Check cache first
	if _, err := os.Stat(meta.Filename); err == nil {

		t.MarkReady(meta.Filename, meta.Title, meta.Uploader, meta.Duration, nil)
		return
	}

	// 3. Stream while caching
	downloadDone := make(chan struct{})
	fileCreated := make(chan struct{}) // Signal when file is ready to be opened
	writeSig := make(chan struct{}, 1) // Signal when data is written

	go func() {
		defer close(downloadDone)

		partFilename := meta.Filename + ".part"

		// Create cache file to write to directly
		cacheFile, err := os.Create(partFilename)
		close(fileCreated) // Signal that file is created (or attempted)

		if err != nil {
			LogVoice("Failed to create cache file: %v", err)
			return
		}

		// Wrap cacheFile with SignalWriter to notify reader of progress
		sw := &SignalWriter{w: cacheFile, sig: writeSig}

		// Write DIRECTLY to cacheFile via SignalWriter
		// This decouples download speed from playback speed
		_, err = ytdlpStream(ctx, t.URL, sw)
		cacheFile.Close() // Close immediately to release handle for rename/remove

		if err != nil {
			LogVoice("Stream/Cache failed for %s: %v", t.URL, err)
			os.Remove(partFilename)
		} else {
			// Atomic rename on success
			if err := os.Rename(partFilename, meta.Filename); err != nil {
				LogVoice("Failed to rename cache file for %s: %v", t.URL, err)
				os.Remove(partFilename)
			}
		}
	}()

	// Wait for file creation signal
	select {
	case <-fileCreated:
	case <-ctx.Done():
		t.MarkError(ctx.Err())
		return
	}

	// Open the same file for reading (tailing)
	partFilename := meta.Filename + ".part"
	readFile, err := os.Open(partFilename)
	if err != nil {
		t.MarkError(fmt.Errorf("failed to open cache file for tailing: %w", err))
		return
	}

	tr := &TailingReader{
		f:    readFile,
		done: downloadDone,
		ctx:  ctx,
		sig:  writeSig,
	}

	// Metadata for the stream - we pass the tailing reader to the track
	t.MarkReady(meta.Filename, meta.Title, meta.Uploader, meta.Duration, tr)
}

func (s *VoiceSession) addToHistory(url, title, channel string) {
	id := extractVideoID(url)
	if id == "" {
		return
	}
	n := normalizeTitle(title, channel)
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	found := false
	for _, h := range s.History {
		if h == id {
			found = true
			break
		}
	}
	if !found {
		s.History = append(s.History, id)
		if len(s.History) > 50 {
			s.History = s.History[1:]
		}
	}
	if n != "" {
		found = false
		// Dynamic similarity check against history
		corpus := append([]string{n}, s.HistoryTitles...)
		weights := calculateTFIDF(corpus)
		for _, t := range s.HistoryTitles {
			if weightedSimilarity(t, n, weights) {
				found = true
				break
			}
		}
		if !found {
			s.HistoryTitles = append(s.HistoryTitles, n)
			if len(s.HistoryTitles) > 50 {
				s.HistoryTitles = s.HistoryTitles[1:]
			}
		}
	}
}

func (s *VoiceSession) fetchRelated(url string) (string, error) {
	id := extractVideoID(url)
	if id == "" {
		return "", errors.New("id")
	}
	type recResult struct {
		es   []ytdlpPlaylistEntry
		prio int
	}
	ch := make(chan recResult, 2)
	go func() {
		es, _ := ytdlpExtractPlaylist(s.cancelCtx, "https://music.youtube.com/watch?v="+id+"&list=RDAMVM"+id, 20)
		ch <- recResult{es, 0}
	}()
	go func() {
		es, _ := ytdlpExtractPlaylist(s.cancelCtx, "https://www.youtube.com/watch?v="+id+"&list=RD"+id, 20)
		ch <- recResult{es, 1}
	}()

	var es []ytdlpPlaylistEntry
	resList := make([][]ytdlpPlaylistEntry, 2)
	for i := 0; i < 2; i++ {
		r := <-ch
		resList[r.prio] = r.es
	}
	es = append(resList[0], resList[1]...)
	s.queueMu.Lock()
	hi := append([]string(nil), s.History...)
	ht := append([]string(nil), s.HistoryTitles...)
	cur := extractVideoID(url)
	s.queueMu.Unlock()

	// Build corpus for dynamic analysis: History + Candidates
	corpus := make([]string, 0, len(ht)+len(es))
	corpus = append(corpus, ht...)
	for _, e := range es {
		normalized := normalizeTitle(e.Title, e.Uploader)
		corpus = append(corpus, normalized)
	}
	weights := calculateTFIDF(corpus)

	for _, e := range es {
		u := strings.TrimSpace(e.URL)
		nid := extractVideoID(u)
		nti, nup := strings.TrimSpace(e.Title), strings.TrimSpace(e.Uploader)
		nor := normalizeTitle(nti, nup)
		if nid == "" || nid == cur {
			continue
		}
		found := false
		for _, i := range hi {
			if i == nid {
				found = true
				break
			}
		}
		if found {
			continue
		}
		for _, t := range ht {
			if weightedSimilarity(t, nor, weights) {
				found = true
				break
			}
		}
		if found {
			continue
		}
		return u, nil
	}
	if len(es) > 1 {
		for _, e := range es {
			u := strings.TrimSpace(e.URL)
			if extractVideoID(u) != cur {
				return u, nil
			}
		}
	}
	return "", errors.New("none")
}

func normalizeTitle(ti, ch string) string {
	if ti == "" {
		return ""
	}
	// camelCase splitting: "ArtistVEVO" -> "Artist VEVO"
	// This helps TF-IDF separate the artist from the common suffix
	tBuf := camelCaseRegex.ReplaceAllString(ti, "${1} ${2}")
	cBuf := camelCaseRegex.ReplaceAllString(ch, "${1} ${2}")

	t, c := strings.ToLower(tBuf), strings.ToLower(cBuf)

	for _, sep := range []string{"|", "//", " ─ ", " - "} {
		if strings.Contains(t, sep) {
			ps := strings.Split(t, sep)
			var nps []string
			for _, p := range ps {
				pt := strings.TrimSpace(p)
				// Dynamic stripping: check if part matches the channel name
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
	// Clean up any remaining channel name (including " - Topic" etc which are just words now)
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
		// IDF = log(1 + N/count)
		// Words appearing in few docs get high weight. Words in many get low weight.
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
				// Unseen word in corpus context? Treat as significant (high weight)
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

func extractVideoID(u string) string {
	if strings.Contains(u, "v=") {
		parts := strings.Split(u, "v=")
		if len(parts) >= 2 {
			vidParts := strings.Split(parts[1], "&")
			if len(vidParts) > 0 {
				return vidParts[0]
			}
		}
	}
	if strings.Contains(u, "youtu.be/") {
		parts := strings.Split(u, "youtu.be/")
		if len(parts) >= 2 {
			vidParts := strings.Split(parts[1], "?")
			if len(vidParts) > 0 {
				return vidParts[0]
			}
		}
	}
	if strings.Contains(u, "shorts/") {
		parts := strings.Split(u, "shorts/")
		if len(parts) >= 2 {
			vidParts := strings.Split(parts[1], "?")
			if len(vidParts) > 0 {
				return vidParts[0]
			}
		}
	}
	return ""
}

// ===========================
// yt-dlp low-level
// ===========================

type ytdlpSearchResult struct {
	URL, Title, Uploader string
	Duration             time.Duration
}

func ytdlpSearch(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	res, err := ytdlp.New().
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		PreferFreeFormats().
		Run(ctx, "ytsearch"+fmt.Sprintf("%d", m)+":"+q)

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
		rs = append(rs, ytdlpSearchResult{ps[0], ps[1], ps[2], d})
	}
	return rs, nil
}
func ytdlpSearchYTM(ctx context.Context, q string, m int) ([]ytdlpSearchResult, error) {
	res, err := ytdlp.New().
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, fmt.Sprintf("ytmsearch%d:%s", m, q))

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
		rs = append(rs, ytdlpSearchResult{URL: ps[0], Title: ps[1], Uploader: ps[2], Duration: d})
	}
	return rs, nil
}

type ytdlpMetadata struct {
	URL, Title, Uploader, Filename, ID string
	Duration                           time.Duration
}

func ytdlpExtractMetadata(ctx context.Context, u string) (*ytdlpMetadata, error) {
	res, err := ytdlp.New().
		Print("%(url)s\t%(title)s\t%(uploader)s\t%(duration)s\t%(id)s\t%(filename)s").
		Format("bestaudio[ext=webm]/bestaudio").
		Output(filepath.Join(AudioCacheDir, "%(id)s.%(ext)s")).
		NoCheckFormats().
		NoWarnings().
		IgnoreConfig().
		Run(ctx, "--skip-download", u)

	if err != nil {
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

	// Check for common music streaming URL patterns
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

	// Check for music-related subdomains
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

func ytdlpStream(ctx context.Context, u string, out io.Writer) (*ytdlpMetadata, error) {
	cmd := ytdlp.New().
		Format("bestaudio[ext=webm]/bestaudio").
		Output("-").
		NoSimulate().
		NoPart().
		NoPlaylist().
		NoCheckFormats().
		NoWarnings().
		IgnoreConfig().
		BuildCommand(ctx, u)

	cmd.Stdout = out
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	cmd.WaitDelay = 0 // Wait indefinitely for I/O to flush after process exit
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Wait for the process to exit
	if err := cmd.Wait(); err != nil {
		// Ignore broken pipe errors which are normal when the transcoder finishes reading first
		msg := strings.ToLower(stderr.String())
		if strings.Contains(err.Error(), "exit status 1") || strings.Contains(msg, "broken pipe") {
			return &ytdlpMetadata{}, nil
		}
		LogVoice("yt-dlp exited with error: %v, stderr: %s", err, stderr.String())
		return nil, err
	}

	return &ytdlpMetadata{}, nil
}

func ytdlpResolveMetadata(ctx context.Context, u string) (string, string, string, time.Duration, error) {
	res, err := ytdlp.New().
		Print("%(title)s\t%(uploader)s\t%(duration)s\t%(id)s").
		NoSimulate().
		IgnoreConfig().
		NoWarnings().
		Run(ctx, "--skip-download", u)

	if err != nil {
		// Check if this is a DRM error
		stderr := strings.ToLower(res.Stderr)
		if strings.Contains(stderr, "drm") {
			return "", "", "", 0, fmt.Errorf("DRM: %w", err)
		}
		return "", "", "", 0, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 4 {
			continue
		}
		d, _ := time.ParseDuration(ps[2] + "s")
		return ps[0], ps[1], ps[3], d, nil
	}
	return "", "", "", 0, errors.New("failed to resolve metadata")
}

type ytdlpPlaylistEntry struct{ URL, Title, Uploader string }

func ytdlpExtractPlaylist(ctx context.Context, u string, m int) ([]ytdlpPlaylistEntry, error) {
	res, err := ytdlp.New().
		FlatPlaylist().
		Print("%(url)s\t%(title)s\t%(uploader)s").
		PlaylistItems(fmt.Sprintf("1-%d", m)).
		NoWarnings().
		IgnoreConfig().
		Run(ctx, u)

	if err != nil {
		return nil, err
	}
	ls := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	es := make([]ytdlpPlaylistEntry, 0)
	for _, l := range ls {
		ps := strings.Split(l, "\t")
		if len(ps) < 3 {
			continue
		}
		es = append(es, ytdlpPlaylistEntry{URL: ps[0], Title: ps[1], Uploader: ps[2]})
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

	// Read the entire body to parse with regex
	body := new(strings.Builder)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	linesRead := 0
	for scanner.Scan() && linesRead < 500 { // Limit to first 500 lines (head section)
		body.WriteString(scanner.Text())
		body.WriteString(" ")
		linesRead++
		if strings.Contains(scanner.Text(), "</head>") {
			break
		}
	}

	htmlContent := body.String()

	// Use regex to extract Open Graph title
	titleRegex := regexp.MustCompile(`<meta[^>]*property=["']og:title["'][^>]*content=["']([^"']+)["']`)
	if matches := titleRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		title = matches[1]
		// Clean up title - remove " - song and lyrics by..." suffix
		if idx := strings.Index(title, " - song and lyrics by"); idx != -1 {
			title = title[:idx]
		}
		if idx := strings.Index(title, " | Spotify"); idx != -1 {
			title = title[:idx]
		}
	}

	// Use regex to extract Open Graph description for artist info
	descRegex := regexp.MustCompile(`<meta[^>]*property=["']og:description["'][^>]*content=["']([^"']+)["']`)
	if matches := descRegex.FindStringSubmatch(htmlContent); len(matches) > 1 {
		desc := matches[1]

		// Spotify format: "Artist · Album · Song · Year" or similar
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

func handleMusicPlay(event *events.ApplicationCommandInteractionCreate, data discord.SlashCommandInteractionData) {
	q, _ := data.OptString("query")
	qv, _ := data.OptString("queue")
	a, _ := data.OptBool("autoplay")
	l, _ := data.OptBool("loop")
	m, p := "", 0
	if qv == "now" {
		m = "now"
	} else if qv == "next" {
		m = "next"
	} else if qv != "" {
		p, _ = strconv.Atoi(qv)
	}
	_ = event.DeferCreateMessage(false)
	if err := startPlayback(event, q, m, a, l, p); err != nil {
		_, _ = event.Client().Rest.UpdateInteractionResponse(event.ApplicationID(), event.Token(), discord.NewMessageUpdateBuilder().SetContent("Failed: "+err.Error()).Build())
	}
}

func handleMusicStop(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	LogVoice("User %s (%s) stopped playback in guild %s", event.User().Username, event.User().ID, *event.GuildID())
	GetVoiceManager().Leave(context.Background(), *event.GuildID())
	_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("🛑 Stopped and disconnected.").Build())
}

func handleMusicQueue(event *events.ApplicationCommandInteractionCreate, _ discord.SlashCommandInteractionData) {
	s := GetVoiceManager().GetSession(*event.GuildID())
	if s == nil {
		_ = event.CreateMessage(discord.NewMessageCreateBuilder().SetContent("Not playing anything.").SetEphemeral(true).Build())
		return
	}

	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	var sb strings.Builder
	if s.currentTrack != nil {
		sb.WriteString("▶️ **Now Playing:**\n")
		sb.WriteString(fmt.Sprintf("[%s](%s) | %s\n\n", s.currentTrack.Title, s.currentTrack.URL, s.currentTrack.Channel))
	}

	sb.WriteString("**Queue:**\n")
	if len(s.queue) == 0 {
		sb.WriteString("_Empty_")
	} else {
		for i, t := range s.queue {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("\n*...and %d more*", len(s.queue)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("`%d.` [%s](%s)\n", i+1, t.Title, t.URL))
		}
	}

	if s.Autoplay {
		sb.WriteString("\n\n♾️ **Autoplay:** Enabled")
	}

	_ = event.CreateMessage(discord.NewMessageCreateBuilder().
		SetContent(sb.String()).
		SetEphemeral(true).
		Build())
}

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
	if q == "" || strings.Contains(q, "http") {
		_ = event.AutocompleteResult(nil)
		return
	}
	rs, err := GetVoiceManager().Search(q)
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

// startPlayback initiates voice playback for a user's query
func startPlayback(ev *events.ApplicationCommandInteractionCreate, q, m string, a, l bool, p int) error {
	LogVoice("User %s (%s) requested playback: %s", ev.User().Username, ev.User().ID, q)
	vs, ok := ev.Client().Caches.VoiceState(*ev.GuildID(), ev.User().ID)
	if !ok || vs.ChannelID == nil {
		return errors.New("user not in a voice channel")
	}
	vm := GetVoiceManager()
	s := vm.Prepare(ev.Client(), *ev.GuildID(), *vs.ChannelID)
	s.queueMu.Lock()
	s.Autoplay, s.Looping = a, l
	s.queueMu.Unlock()
	je := make(chan error, 1)
	go func() { je <- vm.Join(context.Background(), ev.Client(), *ev.GuildID(), *vs.ChannelID) }()
	t, err := vm.Play(context.Background(), *ev.GuildID(), q, m, p)
	if err != nil {
		return err
	}
	if err := <-je; err != nil {
		return err
	}
	if err := t.Wait(context.Background()); err != nil {
		return err
	}
	return finishPlaybackResponse(ev, t, m, s.Autoplay, s.Looping, p)
}

// finishPlaybackResponse sends the final response message after playback starts
func finishPlaybackResponse(ev *events.ApplicationCommandInteractionCreate, t *Track, m string, a, l bool, p int) error {
	pr := "✅ Added to queue:"
	if m == "next" {
		pr = "⏭️ Next up:"
	} else if m == "now" {
		pr = "🎶 Playing Now (Skipped Current):"
	} else if p > 0 {
		pr = "✅ Added to queue at position " + strconv.Itoa(p) + ":"
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
	c := pr + " [" + t.Title + "](" + t.URL + ")"
	if t.Channel != "" {
		c += " · " + t.Channel
	}
	c += s
	_, _ = ev.Client().Rest.UpdateInteractionResponse(ev.ApplicationID(), ev.Token(), discord.NewMessageUpdateBuilder().SetContent(c).Build())
	return nil
}

// ===========================
// Utilities
// ===========================

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
		// 1. Duration Match (Very strong signal)
		if targetDuration > 0 && res.Duration > 0 {
			diff := math.Abs(float64(targetDuration - res.Duration))
			if diff < 2.5*float64(time.Second) {
				score += 100
			} else if diff < 6*float64(time.Second) {
				score += 40
			}
		}
		// 2. Channel Match
		lowCh := strings.ToLower(res.Uploader)
		targetCh := strings.ToLower(targetChannel)
		if targetCh != "" {
			if lowCh == targetCh {
				score += 80
			} else if strings.Contains(lowCh, targetCh) {
				score += 30
			}
		}
		// 3. Title Match
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

// TruncateCenter truncates a string keeping both the start and end.
func TruncateCenter(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	k := (maxLen - 3) / 2
	return string(r[:k]) + "..." + string(r[len(r)-k:])
}

// TruncateWithPreserve truncates text while preserving a prefix and suffix.
func TruncateWithPreserve(text string, maxLen int, prefix, suffix string) string {
	rp, rs := []rune(prefix), []rune(suffix)
	fixedLen := len(rp) + len(rs)
	if fixedLen >= maxLen-10 {
		return TruncateCenter(prefix+text+suffix, maxLen)
	}
	return prefix + TruncateCenter(text, maxLen-fixedLen) + suffix
}
