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
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/kkdai/youtube/v2"
	"github.com/leeineian/minder/sys"
	"github.com/raitonoberu/ytsearch"
)

const AudioCacheDir = "cache"

var (
	VoiceManager *VoiceSystem
	OnceVoice    sync.Once
)

// --- 1. SYSTEM MANAGER ---

type VoiceSystem struct {
	mu       sync.Mutex
	sessions map[snowflake.ID]*VoiceSession
}

// GetVoiceManager returns the singleton VoiceSystem instance.
func GetVoiceManager() *VoiceSystem {
	OnceVoice.Do(func() {
		if err := os.MkdirAll(AudioCacheDir, 0755); err != nil {
			sys.LogError("Failed to create audio cache dir: %v", err)
		}

		VoiceManager = &VoiceSystem{
			sessions: make(map[snowflake.ID]*VoiceSession),
		}
	})
	return VoiceManager
}

// Join connects the bot to a voice channel.
func (vs *VoiceSystem) Join(ctx context.Context, client *bot.Client, guildID, channelID snowflake.ID) error {
	sess := vs.Prepare(client, guildID, channelID)

	sess.joinedMu.Lock()
	if sess.joined {
		sess.joinedMu.Unlock()
		return nil
	}
	sess.joinedMu.Unlock()

	if err := client.UpdateVoiceState(ctx, guildID, &channelID, false, false); err != nil {
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

	go sess.processQueue()
	return nil
}

// Prepare ensures a session exists for the guild and channel, creating it if necessary.
// It returns instantly and does NOT perform the actual voice connection.
func (vs *VoiceSystem) Prepare(client *bot.Client, guildID, channelID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if sess, ok := vs.sessions[guildID]; ok {
		if sess.ChannelID == channelID {
			return sess
		}
		// If on a different channel, stop and recreate
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

	vs.sessions[guildID] = sess
	return sess
}

// WaitJoined blocks until the session is successfully connected to voice.
func (s *VoiceSession) WaitJoined(ctx context.Context) error {
	s.joinedMu.Lock()
	defer s.joinedMu.Unlock()

	for !s.joined {
		// Respect context cancellation while waiting
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

// Leave disconnects the bot from voice.
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

// Play enqueues a track for playback.
func (vs *VoiceSystem) Play(ctx context.Context, guildID snowflake.ID, url string, now bool) (*Track, error) {
	sess := vs.GetSession(guildID)
	if sess == nil {
		return nil, errors.New("not connected to voice")
	}

	track := NewTrack(url)

	sess.queueMu.Lock()
	if now {
		track.IsLive = true
		// Prepend to queue
		sess.queue = append([]*Track{track}, sess.queue...)
		// Kill current playback if any
		if sess.Cmd != nil && sess.Cmd.Process != nil {
			sess.Cmd.Process.Kill()
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
	sess.addToHistory(url)

	return track, nil
}

func (vs *VoiceSystem) GetSession(guildID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.sessions[guildID]
}

type SearchResult struct {
	Title       string
	ChannelName string
	URL         string
}

// Search returns a list of matching tracks from YouTube.
func (vs *VoiceSystem) Search(query string) ([]SearchResult, error) {
	sys.LogStatusRotator("Search starting for: %s", query)
	// Add "Official Audio" for better results if not present
	searchQuery := query
	if !strings.Contains(strings.ToLower(query), "official") {
		searchQuery += " Official Audio"
	}

	search := ytsearch.VideoSearch(searchQuery)
	results, err := search.Next()
	if err != nil {
		sys.LogError("Search failed for %s: %v", query, err)
		return nil, err
	}

	sys.LogStatusRotator("Search found %d results for %s", len(results.Videos), query)

	var out []SearchResult
	for i, v := range results.Videos {
		if i >= 25 { // Discord limit for select menu
			break
		}
		channelTitle := "Unknown Channel"
		if v.Channel.Title != "" {
			channelTitle = v.Channel.Title
		}

		out = append(out, SearchResult{
			Title:       v.Title,
			ChannelName: channelTitle,
			URL:         "https://www.youtube.com/watch?v=" + v.ID,
		})
	}
	return out, nil
}

// --- 2. SESSION & STATE ---

// VoiceSession handles voice connection and playback queue for a guild.
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

	Autoplay bool
	History  []string // List of track URLs to avoid repeats

	Cmd      *exec.Cmd
	provider *StreamProvider
}

// Stop terminates current playback and clears the queue.
func (s *VoiceSession) Stop() {
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
	s.queue = nil
	s.queueMu.Unlock()
}

// --- 3. TRACK DEFINITION ---

// Track represents a single audio track in the queue.
type Track struct {
	URL        string
	Path       string
	Title      string
	Downloaded bool
	Error      error

	// Live streaming support allows playing immediately while caching in background.
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

// Wait blocks until the track is ready for playback.
func (t *Track) Wait() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for !t.Downloaded && t.Error == nil {
		t.cond.Wait()
	}
	return t.Error
}

func (t *Track) MarkReady(path, title string, stream io.Reader) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Path = path
	t.Title = title
	t.LiveStream = stream
	t.Downloaded = true
	t.cond.Broadcast()
}

func (t *Track) MarkError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Error = err
	t.cond.Broadcast()
}

// --- 4. PLAYBACK WORKERS ---

func (s *VoiceSession) downloadTrack(t *Track) {
	s.downloadSem <- struct{}{}
	defer func() { <-s.downloadSem }()

	select {
	case <-s.cancelCtx.Done():
		t.MarkError(errors.New("cancelled"))
		return
	default:
	}

	// 1. Live Mode: Fetch stream and play immediately while caching.
	if t.IsLive {
		sys.LogStatusRotator("Fetching stream (Go): %s", t.URL)

		var streamURL string
		var title string

		// Check if it's a Spotify URL
		if strings.Contains(t.URL, "spotify.com") {
			sys.LogStatusRotator("Resolving Spotify: %s", t.URL)
			resp, err := http.Get("https://open.spotify.com/oembed?url=" + t.URL)
			if err == nil && resp.StatusCode == 200 {
				var data struct {
					Title string `json:"title"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && data.Title != "" {
					// Spotify OEmbed title is usually "[Track] by [Artist]"
					t.Title = data.Title

					// Refine query for high precision: remove "by" and append "Official Audio"
					query := strings.Replace(data.Title, " by ", " ", 1)
					t.URL = query + " Official Audio Topic"
				}
				resp.Body.Close()
			}
		}

		// Search Logic (Absolute Modern: YouTube Music Song focus)
		if !strings.HasPrefix(t.URL, "http") {
			query := t.URL
			sys.LogStatusRotator("Searching YTM (Deep Discovery): %s", query)

			// Step 1: Force YouTube Music search to find the Official Song version (best for lyrics)
			searchURL := "https://music.youtube.com/search?q=" + strings.ReplaceAll(query, " ", "+")
			cmd := exec.CommandContext(s.cancelCtx, "yt-dlp",
				"--flat-playlist",
				"--print", "url",
				"--print", "title",
				"--playlist-items", "1",
				"--no-warnings",
				"--ignore-config",
				"-4",
				searchURL,
			)

			out, err := cmd.Output()
			if err == nil {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				if len(lines) >= 2 && strings.Contains(lines[0], "http") {
					t.URL = lines[0]
					t.Title = lines[1]
				}
			}

			// Step 2: Fallback to standard search if YTM found nothing
			if !strings.HasPrefix(t.URL, "http") {
				sys.LogStatusRotator("Fallback to Standard Search: %s", query)
				searchQuery := query
				if !strings.Contains(strings.ToLower(query), "official") {
					searchQuery += " Official Audio"
				}
				search := ytsearch.VideoSearch(searchQuery)
				results, err := search.Next()
				if err == nil && len(results.Videos) > 0 {
					t.URL = "https://www.youtube.com/watch?v=" + results.Videos[0].ID
					t.Title = results.Videos[0].Title
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
				// Prioritize Opus-encoded WebM streams (passthrough capable)
				formats := video.Formats.WithAudioChannels()
				formats = formats.Type("audio")

				var bestFormat *youtube.Format
				// Seek ITAG 251 (Opus 160kbps) first - the gold standard
				for _, f := range formats {
					if f.ItagNo == 251 {
						bestFormat = &f
						t.IsOpus = true
						break
					}
				}
				// Look for any other Opus
				if bestFormat == nil {
					for _, f := range formats {
						if strings.Contains(f.MimeType, "opus") {
							bestFormat = &f
							t.IsOpus = true
							break
						}
					}
				}
				// Fallback to best audio
				if bestFormat == nil && len(formats) > 0 {
					formats.Sort()
					bestFormat = &formats[0]
				}

				if bestFormat != nil {
					streamURL, _ = client.GetStreamURL(video, bestFormat)
					title = video.Title
				}
			}
		}

		// Fallback to yt-dlp (without node) if Go library fails or not a YT URL
		if streamURL == "" {
			sys.LogStatusRotator("Fallback to yt-dlp: %s", t.URL)
			cmd := exec.CommandContext(s.cancelCtx, "yt-dlp",
				"--print", "url",
				"--print", "title",
				"-f", "bestaudio",
				"--no-warnings",
				"--ignore-config",
				"--youtube-skip-dash-manifest",
				"--youtube-skip-hls-manifest",
				"--no-check-formats",
				"-4",
				t.URL,
			)

			stdout, _ := cmd.StdoutPipe()
			if err := cmd.Start(); err == nil {
				reader := bufio.NewReader(stdout)
				urlLine, _ := reader.ReadString('\n')
				streamURL = strings.TrimSpace(urlLine)
				titleLine, _ := reader.ReadString('\n')
				if titleLine != "" {
					title = strings.TrimSpace(titleLine)
				}
				go cmd.Wait()
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
			sys.LogStatusRotator("Title found: %s", title)
		}

		// Start background caching
		cachePath := filepath.Join(AudioCacheDir, "live_"+fmt.Sprintf("%d", time.Now().UnixNano())+".opus")
		go func() {
			dlCmd := exec.CommandContext(context.Background(), "yt-dlp",
				"-f", "bestaudio",
				"-o", cachePath,
				"--ignore-config",
				"--no-warnings",
				"-4",
				t.URL,
			)
			_ = dlCmd.Run()
		}()

		t.MarkReady(cachePath, t.Title, strings.NewReader(streamURL))
		return
	}

	// 2. Normal Mode: Download to cache file first.
	sys.LogStatusRotator("Fetching metadata: %s", t.URL)

	// Normal Mode: Download to cache file.
	templatePath := filepath.Join(AudioCacheDir, "%(id)s.%(ext)s")
	cmd := exec.CommandContext(s.cancelCtx, "yt-dlp",
		"-f", "bestaudio",
		"-o", templatePath,
		"--print", "%(filename)s\t%(title)s",
		"--no-simulate",
		"--no-overwrites",
		"--ignore-config",
		"--no-warnings",
		"-4",
		t.URL,
	)

	out, err := cmd.Output()
	if err != nil {
		sys.LogError("Download failed: %v", err)
		t.MarkError(err)
		return
	}

	outputStr := strings.TrimSpace(string(out))
	if outputStr == "" {
		t.MarkError(errors.New("no output"))
		return
	}
	lines := strings.Split(outputStr, "\n")
	parts := strings.Split(lines[len(lines)-1], "\t")

	finalPath := parts[0]
	if len(parts) > 1 {
		t.Title = parts[1]
	}

	sys.LogStatusRotator("Ready: %s", t.Title)
	t.MarkReady(finalPath, t.Title, nil)
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
		s.queueMu.Unlock()

		sys.LogStatusRotator("Waiting for track: %s", track.URL)
		if err := track.Wait(); err != nil {
			sys.LogError("Skipping track due to error: %v", err)
			continue
		}

		sys.LogStatusRotator("Playing: %s", track.Title)

		if track.LiveStream != nil {
			// In our updated logic, LiveStream contains the stream URL
			buf := new(strings.Builder)
			_, _ = io.Copy(buf, track.LiveStream)
			s.streamURL(buf.String(), track.IsOpus)
		} else {
			s.streamFile(track.Path, track.IsOpus)
		}

		os.Remove(track.Path)

		// After track ends, if queue is empty and autoplay is on, fetch next
		s.queueMu.Lock()
		if len(s.queue) == 0 && s.Autoplay {
			s.queueMu.Unlock()
			sys.LogStatusRotator("Queue empty, finding related song for: %s", track.URL)
			nextURL, err := s.fetchRelated(track.URL)
			if err == nil && nextURL != "" {
				// Avoid immediate duplicates from history if possible (fetchRelated already does this)
				_, _ = GetVoiceManager().Play(context.Background(), s.GuildID, nextURL, false)
			}
		} else {
			s.queueMu.Unlock()
		}
	}
}

func (s *VoiceSession) addToHistory(url string) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()

	// Check if already in history
	for _, h := range s.History {
		if h == url {
			return
		}
	}

	s.History = append(s.History, url)
	if len(s.History) > 50 {
		s.History = s.History[1:]
	}
}

func (s *VoiceSession) fetchRelated(url string) (string, error) {
	// Use yt-dlp to get recommended videos
	// --get-id --flat-playlist --playlist-items 1-5 "https://www.youtube.com/watch?v=ID&list=RDID"
	// Actually, just using the 'related' feature:
	// yt-dlp --print id --flat-playlist --playlist-items 10 "https://www.youtube.com/watch?v=ID"
	// But yt-dlp doesn't directly expose "related" as a playlist unless we use the 'RD' list.

	id := ""
	if strings.Contains(url, "v=") {
		id = strings.Split(strings.Split(url, "v=")[1], "&")[0]
	} else if strings.Contains(url, "youtu.be/") {
		id = strings.Split(strings.Split(url, "youtu.be/")[1], "?")[0]
	}

	if id == "" {
		return "", errors.New("invalid id")
	}

	// YouTube "Mix" playlist for the video
	mixURL := "https://www.youtube.com/watch?v=" + id + "&list=RD" + id

	cmd := exec.Command("yt-dlp",
		"--flat-playlist",
		"--print", "url",
		"--playlist-items", "1-10",
		"--no-warnings",
		"--ignore-config",
		"-4",
		mixURL,
	)

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	urls := strings.Split(strings.TrimSpace(string(out)), "\n")

	s.queueMu.Lock()
	history := s.History
	s.queueMu.Unlock()

	for _, next := range urls {
		next = strings.TrimSpace(next)
		if next == "" || next == url {
			continue
		}

		// Check history
		found := false
		for _, h := range history {
			if h == next {
				found = true
				break
			}
		}
		if !found {
			return next, nil
		}
	}

	// If all were in history, return the first one that isn't the current song
	if len(urls) > 1 {
		for _, next := range urls {
			if strings.TrimSpace(next) != url {
				return strings.TrimSpace(next), nil
			}
		}
	}

	return "", errors.New("no new related found")
}

// --- 5. STREAMING ENGINE ---

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

	// Audio Codec Selection: Use 'copy' for Opus streams, 'libopus' for everything else.
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
		// Optimize input for network streams
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
			sys.LogStatusRotator("FFmpeg: %s", scanner.Text())
		}
	}()

	s.provider = NewStreamProvider(stdout)
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

// --- 6. LOW-LEVEL AUDIO PROVIDER ---

// StreamProvider implements voice.OpusFrameProvider to parse Ogg/Opus packets.
type StreamProvider struct {
	reader    *bufio.Reader
	header    []byte
	segBuf    []byte
	packetBuf bytes.Buffer
	queue     [][]byte
	OnFinish  func()
	once      sync.Once
}

func NewStreamProvider(r io.Reader) *StreamProvider {
	return &StreamProvider{
		reader: bufio.NewReaderSize(r, 16384),
		header: make([]byte, 27),
		segBuf: make([]byte, 255),
	}
}

func (p *StreamProvider) Close() {
	// No-op
}

func (p *StreamProvider) triggerFinish() {
	p.once.Do(func() {
		if p.OnFinish != nil {
			p.OnFinish()
		}
	})
}

// ProvideOpusFrame parses the next Opus packet from the Ogg stream.
func (p *StreamProvider) ProvideOpusFrame() ([]byte, error) {
	// 1. Return queued packets if any
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

				// Skip Metadata packets (OpusHead/OpusTags).
				if len(frame) > 8 && (string(frame[:8]) == "OpusHead" || string(frame[:8]) == "OpusTags") {
					continue
				}

				p.queue = append(p.queue, frame)
			}
		}

		// If we found any frames in this page, return the first one.
		if len(p.queue) > 0 {
			frame := p.queue[0]
			p.queue = p.queue[1:]
			return frame, nil
		}
	}
}
