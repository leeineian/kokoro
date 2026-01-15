package proc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/kkdai/youtube/v2"
	"github.com/leeineian/minder/sys"
	"github.com/ppalone/ytsearch"
	"github.com/raitonoberu/ytmusic"
)

// ============================================================================
// CONSTANTS
// ============================================================================

const AudioCacheDir = ".cache"

var (
	VoiceManager *VoiceSystem
	OnceVoice    sync.Once
)

var (
	metadataBlockRegex = regexp.MustCompile(`[\(\[\{].*?[\)\]\}]`)
	flavorKeywords     = []string{"cover", "remix", "acoustic", "instrumental", "fan", "parody", "mashup", "edit", "live", "demo"}
)

// ============================================================================
// INITIALIZATION
// ============================================================================

func init() {
	sys.OnClientReady(func(ctx context.Context, client *bot.Client) {
		vm := GetVoiceManager()
		sys.RegisterVoiceStateUpdateHandler(vm.onVoiceStateUpdate)
	})
}

// ============================================================================
// YT-DLP HELPERS
// ============================================================================

// ytdlpSearchResult represents a single search result from yt-dlp
type ytdlpSearchResult struct {
	URL      string
	Title    string
	Uploader string
	Duration time.Duration
}

// ytdlpSearch performs a YouTube search using yt-dlp and returns structured results
func ytdlpSearch(ctx context.Context, query string, maxResults int) ([]ytdlpSearchResult, error) {
	searchQuery := "ytsearch" + fmt.Sprintf("%d", maxResults) + ":" + query

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--print", "%(url)s\t%(title)s\t%(uploader)s\t%(duration)s",
		"--playlist-items", fmt.Sprintf("1-%d", maxResults),
		"--no-warnings",
		"--ignore-config",
		"--prefer-free-formats",
		searchQuery,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp search failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	results := make([]ytdlpSearchResult, 0, len(lines))

	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}

		duration, _ := time.ParseDuration(parts[3] + "s")
		results = append(results, ytdlpSearchResult{
			URL:      parts[0],
			Title:    parts[1],
			Uploader: parts[2],
			Duration: duration,
		})
	}

	return results, nil
}

// ytdlpSearchYTM performs a YouTube Music search
func ytdlpSearchYTM(ctx context.Context, query string, maxResults int) ([]ytdlpSearchResult, error) {
	searchURL := "https://music.youtube.com/search?q=" + strings.ReplaceAll(query, " ", "+")

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--print", "%(url)s\t%(title)s\t%(uploader)s\t%(duration)s",
		"--playlist-items", fmt.Sprintf("1-%d", maxResults),
		"--no-warnings",
		"--ignore-config",
		searchURL,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp ytm search failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	results := make([]ytdlpSearchResult, 0, len(lines))

	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}

		duration, _ := time.ParseDuration(parts[3] + "s")
		results = append(results, ytdlpSearchResult{
			URL:      parts[0],
			Title:    parts[1],
			Uploader: parts[2],
			Duration: duration,
		})
	}

	return results, nil
}

// ytdlpMetadata represents metadata extracted from a URL
type ytdlpMetadata struct {
	URL      string
	Title    string
	Uploader string
	Duration time.Duration
	Filename string
}

// ytdlpExtractMetadata extracts metadata from a URL without downloading
func ytdlpExtractMetadata(ctx context.Context, url string) (*ytdlpMetadata, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--print", "%(url)s",
		"--print", "%(title)s",
		"--print", "%(uploader)s",
		"--print", "%(duration)s",
		"-f", "bestaudio",
		"--youtube-skip-dash-manifest",
		"--youtube-skip-hls-manifest",
		"--no-check-formats",
		"--no-warnings",
		"--ignore-config",
		url,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp metadata extraction failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 4 {
		return nil, errors.New("insufficient metadata lines")
	}

	duration, _ := time.ParseDuration(lines[3] + "s")

	return &ytdlpMetadata{
		URL:      lines[0],
		Title:    lines[1],
		Uploader: lines[2],
		Duration: duration,
	}, nil
}

// ytdlpDownload downloads a track to the specified output template
func ytdlpDownload(ctx context.Context, url, outputTemplate string) (*ytdlpMetadata, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-f", "bestaudio",
		"-o", outputTemplate,
		"--print", "%(filename)s\t%(title)s\t%(uploader)s\t%(duration)s",
		"--no-simulate",
		"--no-overwrites",
		"--ignore-config",
		"--no-warnings",
		url,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp download failed: %w", err)
	}

	outputStr := strings.TrimSpace(string(out))
	if outputStr == "" {
		return nil, errors.New("no output from ytdlp")
	}

	lines := strings.Split(outputStr, "\n")
	parts := strings.Split(lines[len(lines)-1], "\t")

	if len(parts) < 4 {
		return nil, errors.New("insufficient metadata in output")
	}

	duration, _ := time.ParseDuration(parts[3] + "s")

	return &ytdlpMetadata{
		Filename: parts[0],
		Title:    parts[1],
		Uploader: parts[2],
		Duration: duration,
	}, nil
}

// ytdlpPlaylistEntry represents a single entry in a playlist
type ytdlpPlaylistEntry struct {
	URL      string
	Title    string
	Uploader string
}

// ytdlpExtractPlaylist extracts playlist entries (for autoplay/related)
func ytdlpExtractPlaylist(ctx context.Context, playlistURL string, maxItems int) ([]ytdlpPlaylistEntry, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--flat-playlist",
		"--print", "%(url)s",
		"--print", "%(title)s",
		"--print", "%(uploader)s",
		"--playlist-items", fmt.Sprintf("1-%d", maxItems),
		"--no-warnings",
		"--ignore-config",
		playlistURL,
	)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ytdlp playlist extraction failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	entries := make([]ytdlpPlaylistEntry, 0)

	for i := 0; i+2 < len(lines); i += 3 {
		entries = append(entries, ytdlpPlaylistEntry{
			URL:      strings.TrimSpace(lines[i]),
			Title:    strings.TrimSpace(lines[i+1]),
			Uploader: strings.TrimSpace(lines[i+2]),
		})
	}

	return entries, nil
}

// ============================================================================
// VOICE SYSTEM MANAGER
// ============================================================================

type VoiceSystem struct {
	mu       sync.Mutex
	sessions map[snowflake.ID]*VoiceSession
}

func GetVoiceManager() *VoiceSystem {
	OnceVoice.Do(func() {
		if _, err := os.Stat(AudioCacheDir); err == nil {
			sys.LogInfo("Purging audio cache...")
			_ = os.RemoveAll(AudioCacheDir)
		}

		if err := os.MkdirAll(AudioCacheDir, 0755); err != nil {
			sys.LogError("Failed to create audio cache dir: %v", err)
		}

		VoiceManager = &VoiceSystem{
			sessions: make(map[snowflake.ID]*VoiceSession),
		}
	})
	return VoiceManager
}

func (vs *VoiceSystem) GetSession(guildID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.sessions[guildID]
}

func (vs *VoiceSystem) Prepare(client *bot.Client, guildID, channelID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if sess, ok := vs.sessions[guildID]; ok {
		if sess.ChannelID == channelID {
			return sess
		}
		sess.Stop()
	}

	conn := client.VoiceManager.CreateConn(guildID)
	ctx, cancel := context.WithCancel(context.Background())
	sess := &VoiceSession{
		GuildID:     guildID,
		ChannelID:   channelID,
		Conn:        conn,
		cancelCtx:   ctx,
		cancelFunc:  cancel,
		queue:       make([]*Track, 0),
		downloadSem: make(chan struct{}, 3),
	}
	sess.queueCond = sync.NewCond(&sess.queueMu)
	sess.joinedCond = sync.NewCond(&sess.joinedMu)
	sess.pausedCond = sync.NewCond(&sess.pausedMu)
	sess.client = client

	vs.sessions[guildID] = sess
	return sess
}

func (vs *VoiceSystem) Join(ctx context.Context, client *bot.Client, guildID, channelID snowflake.ID) error {
	sess := vs.Prepare(client, guildID, channelID)

	sess.joinedMu.Lock()
	if sess.joined {
		sess.joinedMu.Unlock()
		return nil
	}
	sess.joinedMu.Unlock()

	if err := sess.Conn.Open(ctx, channelID, false, false); err != nil {
		sess.Conn.Close(ctx)
		vs.mu.Lock()
		delete(vs.sessions, guildID)
		vs.mu.Unlock()
		sess.cancelFunc()
		return fmt.Errorf("failed to open voice connection: %w", err)
	}

	sess.joinedMu.Lock()
	sess.joined = true
	sess.joinedCond.Broadcast()
	sess.joinedMu.Unlock()

	go sess.processQueue()
	return nil
}

func (vs *VoiceSystem) Leave(ctx context.Context, guildID snowflake.ID) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if sess, ok := vs.sessions[guildID]; ok {
		sess.Stop()
		if sess.Conn != nil {
			sess.Conn.Close(ctx)
		}
		delete(vs.sessions, guildID)
	}
}

func (vs *VoiceSystem) Play(ctx context.Context, guildID snowflake.ID, url string, mode string, position int) (*Track, error) {
	sess := vs.GetSession(guildID)
	if sess == nil {
		return nil, errors.New("not connected to voice")
	}

	track := NewTrack(url)

	sess.queueMu.Lock()
	if mode == "now" {
		track.IsLive = true
		sess.queue = append([]*Track{track}, sess.queue...)
		sess.skipLoop = true
		sess.autoplayTrack = nil
		if sess.Cmd != nil && sess.Cmd.Process != nil {
			sess.Cmd.Process.Kill()
		}
	} else if mode == "next" {
		sess.queue = append([]*Track{track}, sess.queue...)
	} else if position > 0 {
		idx := position - 1
		if idx >= len(sess.queue) {
			sess.queue = append(sess.queue, track)
		} else {
			sess.queue = append(sess.queue, nil)
			copy(sess.queue[idx+1:], sess.queue[idx:])
			sess.queue[idx] = track
		}
	} else {
		if len(sess.queue) == 0 {
			track.IsLive = true
		}
		sess.queue = append(sess.queue, track)
	}
	sess.queueCond.Signal()
	sess.queueMu.Unlock()

	go sess.downloadTrack(track)
	sess.addToHistory(url, "")

	return track, nil
}

// ============================================================================
// SEARCH
// ============================================================================

type SearchResult struct {
	Title       string
	ChannelName string
	URL         string
}

func (vs *VoiceSystem) Search(query string) ([]SearchResult, error) {
	sys.LogVoice("Search starting for: %s", query)

	primarySource := "ytmusic"
	searchQuery := query

	upperQuery := strings.ToUpper(query)
	if strings.HasPrefix(upperQuery, "[YT]") {
		primarySource = "youtube"
		searchQuery = strings.TrimSpace(query[4:])
	} else if strings.HasPrefix(upperQuery, "[YTM]") {
		primarySource = "ytmusic"
		searchQuery = strings.TrimSpace(query[5:])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2600*time.Millisecond)
	defer cancel()

	resultsMu := sync.Mutex{}
	var ytmResults []SearchResult
	var ytResults []SearchResult
	seenIDs := make(map[string]bool)

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		defer wg.Done()
		search := ytmusic.TrackSearch(searchQuery)
		results, err := search.Next()
		if err == nil {
			for _, track := range results.Tracks {
				if track.VideoID == "" {
					continue
				}
				u := "https://music.youtube.com/watch?v=" + track.VideoID
				id := track.VideoID

				artistName := ""
				if len(track.Artists) > 0 {
					artistName = " - " + track.Artists[0].Name
				}

				resultsMu.Lock()
				if !seenIDs[id] {
					seenIDs[id] = true
					fullTitle := vs.truncateWithPreserve(track.Title, 100, "[YTM] ", artistName)
					ytmResults = append(ytmResults, SearchResult{URL: u, Title: fullTitle})
				}
				resultsMu.Unlock()
			}
		}
	}()

	go func() {
		defer wg.Done()
		client := ytsearch.NewClient(nil)
		searchResult, err := client.Search(ctx, searchQuery)
		if err == nil {
			for _, v := range searchResult.Results {
				u := "https://www.youtube.com/watch?v=" + v.VideoID
				id := v.VideoID
				resultsMu.Lock()
				if !seenIDs[id] {
					seenIDs[id] = true
					fullTitle := vs.truncateWithPreserve(v.Title, 100, "[YT] ", "")
					ytResults = append(ytResults, SearchResult{URL: u, Title: fullTitle})
				}
				resultsMu.Unlock()
			}
		}
	}()

	resultsDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultsDone)
	}()

	select {
	case <-resultsDone:
	case <-time.After(2300 * time.Millisecond):
		sys.LogVoice("Search partially slow for %s, returning whatever found", searchQuery)
	case <-ctx.Done():
	}

	resultsMu.Lock()
	var finalResults []SearchResult
	if primarySource == "youtube" {
		finalResults = append(ytResults, ytmResults...)
	} else {
		finalResults = append(ytmResults, ytResults...)
	}

	if len(finalResults) > 25 {
		finalResults = finalResults[:25]
	}
	resultsMu.Unlock()

	if len(finalResults) == 0 && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	sys.LogVoice("Search found %d unique results for %s", len(finalResults), searchQuery)
	return finalResults, nil
}

func (vs *VoiceSystem) truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	keep := (limit - 3) / 2
	prefix := string(runes[:keep])
	suffix := string(runes[len(runes)-keep:])
	return prefix + "..." + suffix
}

func (vs *VoiceSystem) truncateWithPreserve(title string, limit int, prefix, suffix string) string {
	runesPrefix := []rune(prefix)
	runesSuffix := []rune(suffix)
	fixedLen := len(runesPrefix) + len(runesSuffix)

	if fixedLen >= limit-10 {
		return vs.truncate(prefix+title+suffix, limit)
	}

	titleLimit := limit - fixedLen
	return prefix + vs.truncate(title, titleLimit) + suffix
}

// ============================================================================
// VOICE SESSION
// ============================================================================

type VoiceSession struct {
	GuildID   snowflake.ID
	ChannelID snowflake.ID
	Conn      voice.Conn

	queue     []*Track
	queueMu   sync.Mutex
	queueCond *sync.Cond

	joined     bool
	joinedMu   sync.Mutex
	joinedCond *sync.Cond

	downloadSem chan struct{}

	cancelCtx  context.Context
	cancelFunc context.CancelFunc

	Autoplay      bool
	Looping       bool
	History       []string
	HistoryTitles []string

	Cmd          *exec.Cmd
	provider     *StreamProvider
	client       *bot.Client
	currentTrack *Track
	lastStatus   string

	paused     bool
	pausedMu   sync.Mutex
	pausedCond *sync.Cond

	skipLoop bool

	autoplayTrack *Track
}

func (s *VoiceSession) WaitJoined(ctx context.Context) error {
	s.joinedMu.Lock()
	defer s.joinedMu.Unlock()

	for !s.joined {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.cancelCtx.Done():
			return errors.New("session closed")
		default:
			s.joinedCond.Wait()
		}
	}
	return nil
}

func (s *VoiceSession) Stop() {
	s.skipLoop = true
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
	}
	if s.Conn != nil {
		s.Conn.SetOpusFrameProvider(nil)
		s.Conn.SetSpeaking(context.TODO(), 0)
	}

	s.queueMu.Lock()
	for _, t := range s.queue {
		if t.Path != "" {
			_ = os.Remove(t.Path)
		}
	}
	s.queue = nil
	s.currentTrack = nil
	s.autoplayTrack = nil
	s.queueMu.Unlock()

	s.setVoiceStatus("")
}

func (s *VoiceSession) setVoiceStatus(status string) {
	if s.client == nil {
		return
	}

	runes := []rune(status)
	if len(runes) > 128 {
		status = VoiceManager.truncate(status, 128)
	}

	if status != "" && !strings.HasPrefix(status, "⏸️") {
		s.lastStatus = status
	}

	endpoint := rest.NewEndpoint(http.MethodPut, "/channels/{channel.id}/voice-status")
	compiled := endpoint.Compile(nil, s.ChannelID)
	_ = s.client.Rest.Do(compiled, map[string]string{"status": status}, nil)
}

func (vs *VoiceSystem) onVoiceStateUpdate(event *events.GuildVoiceStateUpdate) {
	guildID := event.VoiceState.GuildID
	vs.mu.Lock()
	sess, ok := vs.sessions[guildID]
	vs.mu.Unlock()

	if !ok {
		return
	}

	if sess.ChannelID == 0 {
		return
	}

	states := event.Client().Caches.VoiceStates(guildID)
	humanCount := 0
	for state := range states {
		if state.ChannelID != nil && *state.ChannelID == sess.ChannelID {
			if state.UserID == event.Client().ID() {
				continue
			}

			isBot := false
			if member, ok := event.Client().Caches.Member(guildID, state.UserID); ok {
				isBot = member.User.Bot
			} else {
				sys.LogVoice("[%s] User %s not in Member cache, assuming human.", guildID, state.UserID)
			}

			if !isBot {
				humanCount++
			}
		}
	}

	sess.pausedMu.Lock()
	currentPaused := sess.paused
	sess.pausedMu.Unlock()

	if humanCount == 0 && !currentPaused {
		sys.LogVoice("[%s] Channel empty. Pausing playback...", event.VoiceState.GuildID)
		sess.pausedMu.Lock()
		sess.paused = true
		sess.pausedCond.Broadcast()
		sess.pausedMu.Unlock()
		sess.setVoiceStatus("⏸️ Paused")
	} else if humanCount > 0 && currentPaused {
		sys.LogVoice("[%s] Users returned. Resuming playback...", event.VoiceState.GuildID)
		sess.pausedMu.Lock()
		sess.paused = false
		sess.pausedCond.Broadcast()
		sess.pausedMu.Unlock()

		sess.queueMu.Lock()
		title := sess.lastStatus
		if title == "" {
			title = "Resuming..."
			if sess.currentTrack != nil {
				sep := ""
				if sess.currentTrack.Channel != "" {
					sep = " · "
				}
				title = VoiceManager.truncateWithPreserve(sess.currentTrack.Title, 128, "🎶 ", sep+sess.currentTrack.Channel)
			}
		}
		sess.queueMu.Unlock()
		sess.setVoiceStatus(title)
	}
}

// ============================================================================
// TRACK
// ============================================================================

type Track struct {
	URL        string
	Path       string
	Title      string
	Channel    string
	Duration   time.Duration
	Downloaded bool
	Error      error

	IsLive     bool
	IsOpus     bool
	LiveStream io.Reader

	cond *sync.Cond
	mu   sync.Mutex
}

func NewTrack(url string) *Track {
	t := &Track{URL: url, Title: "Loading..."}
	t.cond = sync.NewCond(&t.mu)
	return t
}

func (t *Track) Wait() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.Downloaded && t.Error == nil {
		t.cond.Wait()
	}
	return t.Error
}

func (t *Track) MarkReady(path, title, channel string, duration time.Duration, stream io.Reader) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Path = path
	t.Title = title
	t.Channel = channel
	t.Duration = duration
	t.Downloaded = true
	t.LiveStream = stream
	t.cond.Broadcast()
}

func (t *Track) MarkError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Error = err
	t.cond.Broadcast()
}

// ============================================================================
// DOWNLOAD & QUEUE PROCESSING
// ============================================================================

func (s *VoiceSession) downloadTrack(t *Track) {
	s.downloadSem <- struct{}{}
	defer func() { <-s.downloadSem }()

	select {
	case <-s.cancelCtx.Done():
		t.MarkError(errors.New("cancelled"))
		return
	default:
	}

	if t.IsLive {
		sys.LogVoice("Fetching stream (Go): %s", t.URL)

		var streamURL string
		var title string

		if strings.Contains(t.URL, "spotify.com") {
			sys.LogVoice("Resolving Spotify: %s", t.URL)
			resp, err := http.Get("https://open.spotify.com/oembed?url=" + t.URL)
			if err == nil && resp.StatusCode == 200 {
				var data struct {
					Title string `json:"title"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && data.Title != "" {
					t.Title = data.Title
					query := strings.Replace(data.Title, " by ", " ", 1)
					t.URL = query + " Official Audio Topic"
				}
				resp.Body.Close()
			}
		}

		if !strings.HasPrefix(t.URL, "http") {
			query := t.URL
			primarySource := "ytmusic"

			upperQuery := strings.ToUpper(query)
			if strings.HasPrefix(upperQuery, "[YT]") {
				primarySource = "youtube"
				query = strings.TrimSpace(query[4:])
			} else if strings.HasPrefix(upperQuery, "[YTM]") {
				primarySource = "ytmusic"
				query = strings.TrimSpace(query[5:])
			}

			tryYTM := func(q string) bool {
				sys.LogVoice("Searching YTM: %s", q)
				results, err := ytdlpSearchYTM(s.cancelCtx, q, 1)
				if err == nil && len(results) > 0 && strings.Contains(results[0].URL, "http") {
					t.URL = results[0].URL
					t.Title = results[0].Title
					t.Channel = results[0].Uploader
					t.Duration = results[0].Duration
					return true
				}
				return false
			}

			tryYT := func(q string) bool {
				sys.LogVoice("Searching YT: %s", q)
				searchQuery := q
				if !strings.Contains(strings.ToLower(q), "official") {
					searchQuery += " Official Audio"
				}
				results, err := ytdlpSearch(s.cancelCtx, searchQuery, 1)
				if err == nil && len(results) > 0 && strings.Contains(results[0].URL, "http") {
					t.URL = results[0].URL
					t.Title = results[0].Title
					t.Channel = results[0].Uploader
					t.Duration = results[0].Duration
					return true
				}
				return false
			}

			if primarySource == "youtube" {
				if !tryYT(query) {
					tryYTM(query)
				}
			} else {
				if !tryYTM(query) {
					tryYT(query)
				}
			}
		}

		if !strings.HasPrefix(t.URL, "http") {
			t.MarkError(errors.New("could not find a matching song"))
			return
		}

		if strings.Contains(t.URL, "youtube.com") || strings.Contains(t.URL, "youtu.be") {
			client := youtube.Client{}
			video, err2 := client.GetVideo(t.URL)
			if err2 == nil {
				formats := video.Formats.WithAudioChannels()
				formats = formats.Type("audio")

				var bestFormat *youtube.Format
				for _, f := range formats {
					if f.ItagNo == 251 {
						bestFormat = &f
						t.IsOpus = true
						break
					}
				}
				if bestFormat == nil {
					for _, f := range formats {
						if strings.Contains(f.MimeType, "opus") {
							bestFormat = &f
							t.IsOpus = true
							break
						}
					}
				}
				if bestFormat == nil && len(formats) > 0 {
					formats.Sort()
					bestFormat = &formats[0]
				}

				if bestFormat != nil {
					streamURL, _ = client.GetStreamURL(video, bestFormat)
					title = video.Title
					t.Channel = video.Author
					t.Duration = video.Duration
				}
			}
		}

		if streamURL == "" {
			sys.LogVoice("Fallback to yt-dlp: %s", t.URL)
			meta, err := ytdlpExtractMetadata(s.cancelCtx, t.URL)
			if err == nil {
				streamURL = meta.URL
				title = meta.Title
				if title == "NA" {
					title = ""
				}
				t.Channel = meta.Uploader
				t.Duration = meta.Duration
			}
		}

		if streamURL == "" {
			t.MarkError(errors.New("failed to get stream URL"))
			return
		}

		if title != "" {
			t.mu.Lock()
			t.Title = title
			t.mu.Unlock()
			sys.LogVoice("Title found: %s by %s", title, t.Channel)
		}

		// Start background caching
		cachePath := filepath.Join(AudioCacheDir, "live_"+fmt.Sprintf("%d", time.Now().UnixNano())+".opus")
		go func() {
			_, _ = ytdlpDownload(context.Background(), t.URL, cachePath)
		}()

		t.MarkReady(cachePath, t.Title, t.Channel, t.Duration, strings.NewReader(streamURL))
		return
	}

	sys.LogVoice("Fetching metadata: %s", t.URL)

	templatePath := filepath.Join(AudioCacheDir, "%(id)s.%(ext)s")
	meta, err := ytdlpDownload(s.cancelCtx, t.URL, templatePath)
	if err != nil {
		sys.LogError("Download failed: %v", err)
		t.MarkError(err)
		return
	}

	sys.LogVoice("Ready: %s by %s (%v)", meta.Title, meta.Uploader, meta.Duration)
	t.MarkReady(meta.Filename, meta.Title, meta.Uploader, meta.Duration, nil)
}

func (s *VoiceSession) processQueue() {
	for {
		select {
		case <-s.cancelCtx.Done():
			return
		default:
		}

		s.queueMu.Lock()
		if len(s.queue) == 0 {
			s.queueCond.Wait()
		}
		if len(s.queue) == 0 {
			s.queueMu.Unlock()
			continue
		}

		track := s.queue[0]
		s.queue = s.queue[1:]
		s.currentTrack = track
		s.queueMu.Unlock()

		sys.LogVoice("Waiting for track: %s", track.URL)
		if err := track.Wait(); err != nil {
			sys.LogError("Skipping track due to error: %v", err)
			continue
		}

		sys.LogVoice("Playing: %s (by %s)", track.Title, track.Channel)
		sep := ""
		if track.Channel != "" {
			sep = " · "
		}
		status := VoiceManager.truncateWithPreserve(track.Title, 128, "🎶 ", sep+track.Channel)
		s.setVoiceStatus(status)
		s.addToHistory(track.URL, track.Title)

		if s.Autoplay {
			go func(currentURL string) {
				nextURL, err := s.fetchRelated(currentURL)
				if err == nil && nextURL != "" {
					t := NewTrack(nextURL)
					s.queueMu.Lock()
					if s.Autoplay && s.currentTrack != nil && s.currentTrack.URL == currentURL {
						s.autoplayTrack = t
					}
					s.queueMu.Unlock()
					if t != nil {
						s.downloadTrack(t)
					}
				}
			}(track.URL)
		}

		go func(curr *Track) {
			if curr.Duration <= 20*time.Second {
				return
			}

			select {
			case <-time.After(curr.Duration - 15*time.Second):
			case <-s.cancelCtx.Done():
				return
			}

			s.queueMu.Lock()
			if s.currentTrack != curr {
				s.queueMu.Unlock()
				return
			}

			var next *Track
			if len(s.queue) > 0 {
				next = s.queue[0]
			} else if s.Autoplay && s.autoplayTrack != nil {
				next = s.autoplayTrack
			}
			s.queueMu.Unlock()

			if next != nil {
				waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer waitCancel()

				done := make(chan struct{})
				go func() {
					_ = next.Wait()
					close(done)
				}()

				select {
				case <-done:
					sep := ""
					if next.Channel != "" {
						sep = " · "
					}
					status := VoiceManager.truncateWithPreserve(next.Title, 128, "⏭️ ", sep+next.Channel)
					s.setVoiceStatus(status)
				case <-waitCtx.Done():
				}
			}
		}(track)

		if track.LiveStream != nil {
			buf := new(strings.Builder)
			_, _ = io.Copy(buf, track.LiveStream)
			s.streamURL(buf.String(), track.IsOpus)
		} else {
			s.streamFile(track.Path, track.IsOpus)
		}

		s.queueMu.Lock()
		shouldLoop := s.Looping && !s.skipLoop
		s.skipLoop = false
		if shouldLoop {
			s.queue = append([]*Track{track}, s.queue...)
			s.queueMu.Unlock()
			continue
		}
		s.queueMu.Unlock()

		if track.Path != "" {
			os.Remove(track.Path)
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
				sys.LogVoice("Queue empty, finding related for: %s", track.URL)
				nextURL, err := s.fetchRelated(track.URL)
				if err == nil && nextURL != "" {
					_, _ = GetVoiceManager().Play(context.Background(), s.GuildID, nextURL, "", 0)
				}
				continue
			}
		}

		if len(s.queue) == 0 {
			s.currentTrack = nil
			s.autoplayTrack = nil
			s.queueMu.Unlock()
			s.setVoiceStatus("")
		} else {
			s.queueMu.Unlock()
		}
	}
}

// ============================================================================
// HISTORY & AUTOPLAY
// ============================================================================

func (s *VoiceSession) addToHistory(url, title string) {
	id := extractVideoID(url)
	if id == "" {
		return
	}

	norm := normalizeTitle(title, "")

	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	idFound := false
	for _, h := range s.History {
		if h == id {
			idFound = true
			break
		}
	}
	if !idFound {
		s.History = append(s.History, id)
		if len(s.History) > 50 {
			s.History = s.History[1:]
		}
	}

	if norm != "" {
		titleFound := false
		for _, t := range s.HistoryTitles {
			if isSimilar(t, norm) {
				titleFound = true
				break
			}
		}
		if !titleFound {
			s.HistoryTitles = append(s.HistoryTitles, norm)
			if len(s.HistoryTitles) > 50 {
				s.HistoryTitles = s.HistoryTitles[1:]
			}
		}
	}
}

func normalizeTitle(title string, channel string) string {
	if title == "" {
		return ""
	}

	t := strings.ToLower(title)
	c := strings.ToLower(channel)

	separators := []string{"|", "//", " ─ ", " - "}
	for _, sep := range separators {
		if strings.Contains(t, sep) {
			parts := strings.Split(t, sep)
			var newParts []string
			for _, p := range parts {
				pTrim := strings.TrimSpace(p)
				if pTrim != c && pTrim != strings.ReplaceAll(c, " ", "") {
					newParts = append(newParts, pTrim)
				}
			}
			if len(newParts) > 0 {
				t = strings.Join(newParts, " ")
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

	tokenMap := make(map[string]bool)
	for _, word := range strings.Fields(sb.String()) {
		if len(word) > 1 {
			tokenMap[word] = true
		}
	}

	var tokens []string
	for k := range tokenMap {
		tokens = append(tokens, k)
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

func isSimilar(normA, normB string) bool {
	if normA == "" || normB == "" {
		return false
	}
	if normA == normB {
		return true
	}

	wordsA := strings.Fields(normA)
	wordsB := strings.Fields(normB)

	setA := make(map[string]bool)
	flavorA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
		for _, fk := range flavorKeywords {
			if w == fk {
				flavorA[fk] = true
			}
		}
	}

	setB := make(map[string]bool)
	flavorB := make(map[string]bool)
	intersect := 0
	for _, w := range wordsB {
		setB[w] = true
		if setA[w] {
			intersect++
		}
		for _, fk := range flavorKeywords {
			if w == fk {
				flavorB[fk] = true
			}
		}
	}

	for _, fk := range flavorKeywords {
		if flavorA[fk] != flavorB[fk] {
			return false
		}
	}

	minWords := len(wordsA)
	if len(wordsB) < minWords {
		minWords = len(wordsB)
	}

	if minWords == 0 {
		return false
	}

	score := float64(intersect) / float64(minWords)
	return score >= 0.7
}

func (s *VoiceSession) fetchRelated(url string) (string, error) {
	id := extractVideoID(url)

	if id == "" {
		return "", errors.New("invalid id")
	}

	mixURL := "https://music.youtube.com/watch?v=" + id + "&list=RDAMVM" + id

	entries, err := ytdlpExtractPlaylist(s.cancelCtx, mixURL, 20)
	if err != nil || len(entries) == 0 {
		sys.LogVoice("YTM Radio empty, trying standard YouTube Mix...")
		mixURL = "https://www.youtube.com/watch?v=" + id + "&list=RD" + id
		entries, _ = ytdlpExtractPlaylist(s.cancelCtx, mixURL, 20)
	}

	s.queueMu.Lock()
	historyIDs := s.History
	historyTitles := s.HistoryTitles
	s.queueMu.Unlock()

	currentID := extractVideoID(url)

	for _, e := range entries {
		nextURL := strings.TrimSpace(e.URL)
		nextID := extractVideoID(nextURL)
		nextTitle := strings.TrimSpace(e.Title)
		nextUploader := strings.TrimSpace(e.Uploader)
		normTitle := normalizeTitle(nextTitle, nextUploader)

		if nextID == "" || nextID == currentID {
			continue
		}

		idFound := false
		for _, hID := range historyIDs {
			if hID == nextID {
				idFound = true
				break
			}
		}
		if idFound {
			continue
		}

		titleFound := false
		for _, hTitle := range historyTitles {
			if isSimilar(hTitle, normTitle) {
				titleFound = true
				break
			}
		}
		if titleFound {
			continue
		}

		return nextURL, nil
	}

	if len(entries) > 1 {
		for _, e := range entries {
			u := strings.TrimSpace(e.URL)
			if extractVideoID(u) != currentID {
				return u, nil
			}
		}
	}

	return "", errors.New("no new related found")
}

func extractVideoID(url string) string {
	if strings.Contains(url, "v=") {
		return strings.Split(strings.Split(url, "v=")[1], "&")[0]
	}
	if strings.Contains(url, "youtu.be/") {
		id := strings.Split(url, "youtu.be/")[1]
		return strings.Split(id, "?")[0]
	}
	if strings.Contains(url, "shorts/") {
		id := strings.Split(url, "shorts/")[1]
		return strings.Split(id, "?")[0]
	}
	return ""
}

// ============================================================================
// STREAMING ENGINE
// ============================================================================

func (s *VoiceSession) streamURL(url string, isOpus bool) {
	s.streamCommon(url, isOpus, nil)
}

func (s *VoiceSession) streamFile(path string, isOpus bool) {
	s.streamCommon(path, isOpus, nil)
}

func (s *VoiceSession) streamCommon(input string, isOpus bool, stdin io.Reader) {
	if s.Cmd != nil && s.Cmd.Process != nil {
		s.Cmd.Process.Kill()
	}

	codec := "libopus"
	if isOpus {
		codec = "copy"
	}

	args := []string{
		"-i", input,
		"-map", "0:a",
		"-acodec", codec,
		"-b:a", "128k",
		"-vbr", "on",
		"-compression_level", "10",
		"-analyzeduration", "0",
		"-probesize", "32",
		"-f", "opus",
		"pipe:1",
	}

	if strings.HasPrefix(input, "http") {
		args = append([]string{
			"-reconnect", "1",
			"-reconnect_at_eof", "1",
			"-reconnect_streamed", "1",
			"-reconnect_delay_max", "2",
			"-user_agent", "Mozilla/5.0",
			"-fflags", "nobuffer",
			"-flags", "low_delay",
		}, args...)
	}

	ffmpegCmd := exec.Command("ffmpeg", args...)

	if stdin != nil {
		ffmpegCmd.Stdin = stdin
	}

	stdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		sys.LogError("Stdout pipe error: %v", err)
		return
	}

	stderr, _ := ffmpegCmd.StderrPipe()

	if err := ffmpegCmd.Start(); err != nil {
		sys.LogError("FFmpeg start error: %v", err)
		return
	}
	s.Cmd = ffmpegCmd

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			sys.LogVoice("FFmpeg: %s", scanner.Text())
		}
	}()

	s.provider = NewStreamProvider(stdout, s)
	done := make(chan struct{})
	s.provider.OnFinish = func() {
		close(done)
	}

	s.Conn.SetOpusFrameProvider(s.provider)
	s.Conn.SetSpeaking(context.TODO(), voice.SpeakingFlagMicrophone)

	select {
	case <-done:
		time.Sleep(100 * time.Millisecond)
	case <-s.cancelCtx.Done():
	}

	s.Cmd.Process.Kill()
	s.Cmd.Wait()

	s.Conn.SetOpusFrameProvider(nil)
	s.Conn.SetSpeaking(context.TODO(), 0)
}

// ============================================================================
// OPUS FRAME PROVIDER
// ============================================================================

type StreamProvider struct {
	reader    *bufio.Reader
	header    []byte
	segBuf    []byte
	packetBuf bytes.Buffer
	queue     [][]byte
	OnFinish  func()
	once      sync.Once
	sess      *VoiceSession
}

func NewStreamProvider(r io.Reader, sess *VoiceSession) *StreamProvider {
	return &StreamProvider{
		reader: bufio.NewReaderSize(r, 16384),
		header: make([]byte, 27),
		segBuf: make([]byte, 255),
		sess:   sess,
	}
}

func (p *StreamProvider) Close() {
}

func (p *StreamProvider) triggerFinish() {
	p.once.Do(func() {
		if p.OnFinish != nil {
			p.OnFinish()
		}
	})
}

func (p *StreamProvider) ProvideOpusFrame() ([]byte, error) {
	p.sess.pausedMu.Lock()
	for p.sess.paused {
		p.sess.pausedCond.Wait()
		select {
		case <-p.sess.cancelCtx.Done():
			p.sess.pausedMu.Unlock()
			return nil, p.sess.cancelCtx.Err()
		default:
		}
	}
	p.sess.pausedMu.Unlock()

	if len(p.queue) > 0 {
		frame := p.queue[0]
		p.queue = p.queue[1:]
		return frame, nil
	}

scanLoop:
	for {
		sig, err := p.reader.Peek(4)
		if err != nil {
			p.triggerFinish()
			return nil, err
		}

		if string(sig) == "OggS" {
			_, err := io.ReadFull(p.reader, p.header)
			if err != nil {
				p.triggerFinish()
				return nil, err
			}
		} else {
			_, _ = p.reader.Discard(1)
			continue scanLoop
		}

		numSegs := int(p.header[26])
		segTable := p.segBuf[:numSegs]
		if _, err := io.ReadFull(p.reader, segTable); err != nil {
			p.triggerFinish()
			return nil, err
		}

		for _, segLen := range segTable {
			l := int(segLen)
			_, err := io.CopyN(&p.packetBuf, p.reader, int64(l))
			if err != nil {
				p.triggerFinish()
				return nil, err
			}

			if l < 255 {
				payload := p.packetBuf.Bytes()
				frame := make([]byte, len(payload))
				copy(frame, payload)
				p.packetBuf.Reset()

				if len(frame) > 8 && (string(frame[:8]) == "OpusHead" || string(frame[:8]) == "OpusTags") {
					continue
				}

				p.queue = append(p.queue, frame)
			}
		}

		if len(p.queue) > 0 {
			frame := p.queue[0]
			p.queue = p.queue[1:]
			return frame, nil
		}
	}
}
