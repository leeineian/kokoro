package voice

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ppalone/ytsearch"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

func (vs *VoiceSystem) startCacheGC(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
}

func (vs *VoiceSystem) GetSession(guildID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.sessions[guildID]
}

func (vs *VoiceSystem) Prepare(client bot.Client, guildID, channelID snowflake.ID) *VoiceSession {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if sess, ok := vs.sessions[guildID]; ok {
		if sess.cancelCtx.Err() != nil {
			delete(vs.sessions, guildID)
		} else {
			sess.channelMu.Lock()
			oldChannelID := sess.ChannelID
			if oldChannelID != channelID {
				sess.ChannelID = channelID
				sess.channelMu.Unlock()
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

	voiceSys.LogInfo("Joining channel %s in guild %s", channelID, guildID)

	var lastErr error
	for i := range 5 {
		if i > 0 {
			backoff := time.Duration(1<<uint(i-1)) * 1000 * time.Millisecond
			voiceSys.LogInfo("Retrying voice connection in %v (Attempt %d/5)", backoff, i+1)

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
			voiceSys.LogInfo("Join attempt %d failed for guild %s: %v", i+1, guildID, err)
			sess.Conn.Close(context.Background())
			continue
		}
		lastErr = nil
		break
	}

	if lastErr != nil {
		voiceSys.LogInfo("Failed to connect to voice in guild %s after 5 attempts: %v", guildID, lastErr)
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
			voiceSys.LogInfo("CRITICAL: monitorConnection panic recovered: %v", r)
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
				voiceSys.LogInfo("Detected disconnected voice state for guild %s, marking as not joined.", s.GuildID)
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

func (vs *VoiceSystem) cleanupSession(sess *VoiceSession) {
	if sess == nil {
		return
	}
	UpdateVoicePanels(sess.GuildID, sess.GetClient())

	safeGo(func() {
		s := sess
		s.Stop()

		s.channelMu.RLock()
		cid := s.ChannelID
		s.channelMu.RUnlock()

		if cid != 0 {
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
	cleanupAudioCache()
}

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
		s.queue = slices.Clone(tracks)
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
		s.queue = slices.Insert(s.queue, 0, tracks...)
	} else if pos > 0 {
		idx := pos - 1
		if idx >= len(s.queue) {
			s.queue = append(s.queue, tracks...)
		} else {
			s.queue = slices.Insert(s.queue, idx, tracks...)
		}
	} else {
		s.queue = append(s.queue, tracks...)
	}

	select {
	case s.queueUpdate <- struct{}{}:
	default:
	}
}

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
	if s == nil {
		return
	}

	if event.VoiceState.ChannelID == nil {
		voiceSys.LogInfo("Bot disconnected by external event in guild %s", event.VoiceState.GuildID)
		delete(vs.sessions, event.VoiceState.GuildID)
		safeGo(func() { vs.cleanupSession(s) })
		return
	}

	s.channelMu.RLock()
	currentChannelID := s.ChannelID
	s.channelMu.RUnlock()

	if currentChannelID == 0 || *event.VoiceState.ChannelID != currentChannelID {
		oldChannelID := currentChannelID
		voiceSys.LogInfo("Bot moved from %s to %s in guild %s", oldChannelID, *event.VoiceState.ChannelID, event.VoiceState.GuildID)

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
		voiceSys.LogInfo("Pausing playback in guild %s (No humans)", event.VoiceState.GuildID)
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
		voiceSys.LogInfo("Resuming playback in guild %s", event.VoiceState.GuildID)
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

func (s *VoiceSession) Seek(duration time.Duration) error {
	s.lockQueue()
	if s.currentTrack == nil {
		s.unlockQueue()
		return fmt.Errorf("no track currently playing")
	}

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
			voiceSys.LogInfo("Smart Seek: Target %v beyond buffer (~%vms). Restarting stream...", targetDuration, int64(bufferedMs))

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

func (s *VoiceSession) Skip() (string, error) {
	s.lockQueue()
	if s.currentTrack == nil && len(s.queue) == 0 {
		s.unlockQueue()
		return "", errors.New("nothing playing")
	}
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

func (s *VoiceSession) Stop() {
	s.skipLoop = true
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	s.downloadMu.Lock()
	if s.downloadCond != nil {
		s.downloadCond.Broadcast()
	}
	s.downloadMu.Unlock()

	s.lockQueue()
	if s.streamCancel != nil {
		s.streamCancel()
	}
	s.transcoder = nil
	if s.Conn != nil {
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

func (s *VoiceSession) WaitForCleanup() {
	s.goroutineWg.Wait()
}

func (s *VoiceSession) RefreshStatus() {
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
	s.setVoiceStatus(voiceSys.TruncateWithPreserve(title, 128, prefix, sep+channel))
}

func (s *VoiceSession) setVoiceStatus(status string) {
	select {
	case s.statusChan <- status:
	default:
	}
}

func (s *VoiceSession) statusManager() {
	defer func() {
		if r := recover(); r != nil {
			voiceSys.LogInfo("CRITICAL: statusManager panic recovered: %v", r)
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
				target = voiceSys.TruncateCenter(target, 128)
			}
			if target != "" && !strings.HasPrefix(target, "▶️") {
				s.lastStatus = target
			}
			s.channelMu.RLock()
			channelID := s.ChannelID
			s.channelMu.RUnlock()

			safeGo(func() {
				func(cid snowflake.ID, status string) {
					cl := s.GetClient()
					err := cl.Rest.Do(rest.NewEndpoint(http.MethodPut, "/channels/"+cid.String()+"/voice-status").Compile(nil), map[string]string{"status": status}, nil)
					if err != nil {
						voiceSys.LogInfo("Failed to update status for %s: %v", cid, err)
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
				voiceSys.LogInfo("Next Track: %s%s%s (%s) [%s]", t.Title, sep, t.Channel, t.URL, t.Duration.Round(time.Second))
				t.NextTrackLogged = true
			}
			t.mu.Unlock()
		}

		if isCurrent || (isNext && nearing) {
			prefix := "⏩ "
			if isCurrent {
				prefix = "⏸️ "
			}
			s.setVoiceStatus(voiceSys.TruncateWithPreserve(t.Title, 128, prefix, sep+t.Channel))
		}
	}
}

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
	voiceSys.LogInfo("Exhausted retries for SetOpusFrameProvider in guild %s", s.GuildID)
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
	voiceSys.LogInfo("Exhausted retries for SetSpeaking in guild %s", s.GuildID)
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

func (s *VoiceSession) processQueue() {
	defer func() {
		if r := recover(); r != nil {
			voiceSys.LogInfo("CRITICAL: processQueue panic recovered: %v", r)
		}
	}()

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
			voiceSys.LogInfo("Skipping track %s due to error: %v", t.URL, err)
			continue
		}

		s.lockQueue()
		if len(s.queue) > 0 {
			s.queue[0].Priority = 1
			s.scheduleDownload(s.queue[0])
		}
		s.unlockQueue()
		if err := s.WaitJoined(s.cancelCtx); err != nil {
			voiceSys.LogInfo("Skipping track %s: failed to wait for join: %v", t.URL, err)
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
				voiceSys.LogInfo("Playing track: %s · %s (%s) [%v]", title, channel, url, duration)
				s.RefreshStatus()
			case <-ctx.Done():
				voiceSys.LogInfo("Track skipped/finished: %s", url)
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
						voiceSys.LogInfo("Autoplay pre-fetch failed for %s: %v (Next: %s)", url, err, next)
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
					voiceSys.LogInfo("Autoplay sync fetch failed for %s: %v", t.URL, err)
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

func (s *VoiceSession) resolveTrackMetadata(ctx context.Context, t *Track) error {
	if !t.NeedsResolution {
		return nil
	}

	start := time.Now()
	origURL := t.URL
	defer func() {
		voiceSys.LogInfo("Metadata resolution for %s took %v", origURL, time.Since(start))
	}()

	needsSearch := !strings.HasPrefix(t.URL, "http")
	var targetDuration time.Duration

	if !needsSearch && !isYouTubeURL(t.URL) {
		likelyDRMSite := isLikelyMusicStreamingSite(t.URL)

		resultChan := make(chan metadataResult, 2)

		safeGo(func() {
			timeout := 30 * time.Second
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
					voiceSys.LogInfo("DRM detected for %s, but scraping also failed", t.URL)
					return fmt.Errorf("DRM-protected content not supported: %s", t.URL)
				}
			}
		}
	}

	if needsSearch {
		q := t.URL
		ytp := getYoutubePrefix()
		if strings.HasPrefix(strings.ToUpper(q), strings.ToUpper(ytp)) {
			q = strings.TrimSpace(q[len(ytp):])
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
				var err error = errors.New("fast metadata disabled")
				var title, uploader string
				var dur time.Duration

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
					voiceSys.LogInfo("Background metadata fetch failed for %s: %v", t.URL, err)
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

	meta, err := ytdlpExtractMetadata(ctx, t.URL)
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

	ctx, dcancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.downloadCancel = dcancel
	t.mu.Unlock()

	safeGo(func() {
		defer close(downloadDone)
		defer func() {
			if r := recover(); r != nil {
				voiceSys.LogInfo("CRITICAL: downloader safeGo panic recovered: %v", r)
				onceError.Do(func() { errorSig <- fmt.Errorf("panic: %v", r) })
			}
		}()
		ensureAudioCacheDir()
		partFilename := filename + ".part"

		t.mu.Lock()
		ss := t.SeekOffset
		t.mu.Unlock()

		thresh := int64(16 * 1024) // 16KB initial buffer for transcoding
		cacheFile, err := os.Create(partFilename)

		t.mu.Lock()
		if t.FileCreated != nil {
			close(t.FileCreated)
		}
		t.mu.Unlock()

		if err != nil {
			voiceSys.LogInfo("Failed to create cache file: %v", err)
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

		t.mu.Lock()
		t.downloadCancel = dcancel
		t.mu.Unlock()

		_, err = ytdlpStream(ctx, url, ss, sw)
		if err != nil {
			onceError.Do(func() { errorSig <- err })
		}
		cacheFile.Close()

		if err != nil {
			voiceSys.LogInfo("Stream/Cache failed for %s: %v", url, err)
			os.Remove(partFilename)
			return
		}

		onceReady.Do(func() { close(readySig) })

		t.mu.Lock()
		finalWb := t.WrittenBytes
		t.mu.Unlock()

		if finalWb < MinTrackSize {
			onceError.Do(func() { errorSig <- fmt.Errorf("download too small: %d bytes (min %d)", finalWb, MinTrackSize) })
			voiceSys.LogInfo("Download too small for %s: %d bytes. Deleting.", url, finalWb)
			os.Remove(partFilename)
			return
		}

		if err := os.Rename(partFilename, filename); err != nil {
			voiceSys.LogInfo("Failed to rename cache file for %s: %v", url, err)
			os.Remove(partFilename)
		} else {
			t.mu.Lock()
			wb := t.WrittenBytes
			t.mu.Unlock()
			voiceSys.LogInfo("Downloaded track file: %s (Size: %d bytes)", filename, wb)
		}
	})

	totalTimer := time.NewTimer(90 * time.Second)
	defer totalTimer.Stop()

	stallTimer := time.NewTimer(30 * time.Second)
	defer stallTimer.Stop()

loop:
	for {
		select {
		case <-readySig:
			break loop
		case <-ctx.Done():
			dcancel()
			t.MarkError(ctx.Err())
			return
		case <-totalTimer.C:
			dcancel()
			t.MarkError(errors.New("timeout: download too slow (max total time exceeded)"))
			return
		case <-stallTimer.C:
			dcancel()
			t.MarkError(errors.New("timeout: download stalled or failed to start"))
			return
		case err := <-errorSig:
			dcancel()
			t.MarkError(err)
			return
		case <-downloadDone:
			dcancel()
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

	if len(es) == 0 {
		voiceSys.LogInfo("Autoplay: yt-dlp returned 0 results, trying native search fallback for '%s %s'", title, artist)
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
	voiceSys.LogInfo("Autoplay: Found %d related tracks for %s", len(es), curTitle)

	s.lockQueue()
	count := min(len(s.HistoryTitles), 5)
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
		found := slices.Contains(s.History, nid)
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
		voiceSys.LogInfo("Autoplay: Strict filtering failed, trying fallback... %s", curTitle)
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
		voiceSys.LogInfo("Autoplay: Not enough tracks for fallback (Count: %d)", len(es))
	}
	return "", errors.New("none")
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
			voiceSys.LogInfo("CRITICAL: downloadLoop panic recovered: %v", r)
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
			if s.cancelCtx.Err() != nil {
				s.downloadMu.Unlock()
				return
			}
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
