package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"unsafe"

	"github.com/disgoorg/disgo/voice"
	"github.com/ebitengine/purego"
	"github.com/obinnaokechukwu/ffgo/avcodec"
	"github.com/obinnaokechukwu/ffgo/avformat"
	"github.com/obinnaokechukwu/ffgo/avutil"
	"github.com/obinnaokechukwu/ffgo/swresample"
)

func (t *Track) FullDisplay() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	title := t.Title
	if title == "" || strings.HasPrefix(title, "http") {
		if id := extractVideoID(t.URL); id != "" {
			title = "YouTube Track (" + id + ")"
		} else {
			title = "Music Track"
		}
	}
	if t.Channel != "" && t.Channel != "NA" {
		return fmt.Sprintf("[%s](%s) · %s", title, t.URL, t.Channel)
	}
	return fmt.Sprintf("[%s](%s)", title, t.URL)
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
		size := int64(0)
		if st, err := os.Stat(t.Path); err == nil {
			size = st.Size()
		}
		err := os.Remove(t.Path)
		if err != nil && !os.IsNotExist(err) {
			voiceSys.LogInfo("Failed to remove track file %s: %v", t.Path, err)
		} else if err == nil {
			voiceSys.LogInfo("Cleaned up track file: %s (Size: %d bytes)", t.Path, size)
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

func (t *Track) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		return t.Error
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Track) MarkReady(path, title, channel string, d time.Duration, s io.Reader) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Downloaded || t.Error != nil {
		return
	}
	t.Path, t.Title, t.Channel, t.Duration, t.Downloaded, t.LiveStream = path, title, channel, d, true, s
	t.SafeCloseMetadata()
	close(t.done)
}

func (t *Track) MarkError(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Downloaded || t.Error != nil {
		return
	}
	t.Error = err
	t.SafeCloseMetadata()
	close(t.done)
}

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
	var once sync.Once
	p.OnFinish = func() {
		once.Do(func() {
			close(done)
		})
	}
	ctx, cancel := context.WithCancel(s.cancelCtx)
	s.streamCancel = cancel
	p.SetContext(ctx)
	if tr, ok := reader.(*TailingReader); ok {
		tr.SetPlayContext(ctx)
	}
	s.unlockQueue()

	safeGo(func() {
		defer func() {
			p.Close()
		}()
		defer p.PushFrame(nil)
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
		var err error
		for range 100 {
			err = t.OpenInput(inputPath, reader)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return
			}
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}

		if err != nil {
			voiceSys.LogInfo("Transcoder OpenInput failed after retries: %v", err)
			return
		}

		s.lockQueue()
		s.transcoder = t
		s.unlockQueue()

		if err := t.SetupDecoder(); err != nil {
			voiceSys.LogInfo("Transcoder SetupDecoder failed: %v", err)
			return
		}
		if err := t.SetupEncoder(); err != nil {
			voiceSys.LogInfo("Transcoder SetupEncoder failed: %m", err)
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

		err = t.Transcode(ctx, p.PushFrame)
		if err != nil {
			voiceSys.LogInfo("Transcoder finished for: %s (Err: %v)", url, err)
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
		voiceSys.LogInfo("Playback finished: %s", getMsg())
	case <-ctx.Done():
		voiceSys.LogInfo("Playback stopped: %s", getMsg())
	case <-s.cancelCtx.Done():
		voiceSys.LogInfo("Global session canceled for: %s", getMsg())
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
		packet:        avcodec.PacketAlloc(),
		frame:         avutil.FrameAlloc(),
		resampleFrame: avutil.FrameAlloc(),
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
	t.inputCtx = avformat.AllocContext()
	if t.inputCtx == nil {
		return errors.New("failed to alloc ctx")
	}
	if r != nil {
		t.reader = r

		buffer := avutil.Malloc(16 * 1024)
		var readCb, seekCb uintptr
		readCb = purego.NewCallback(func(opaque unsafe.Pointer, buf *byte, bufSize int32) int32 {
			b := unsafe.Slice(buf, bufSize)
			n, err := t.reader.Read(b)
			if err != nil {
				return avutil.AVERROR_EOF
			}
			return int32(n)
		})
		if s, ok := t.reader.(io.Seeker); ok {
			seekCb = purego.NewCallback(func(opaque unsafe.Pointer, offset int64, whence int32) int64 {
				if whence == 0x10000 {
					return -1
				}
				n, err := s.Seek(offset, int(whence))
				if err != nil {
					return -1
				}
				return n
			})
		}

		ioCtx := avformat.IOAllocContext(buffer, 16*1024, false, nil, readCb, 0, seekCb)
		t._readCb = readCb
		t._seekCb = seekCb
		avformat.SetIOContext(t.inputCtx, ioCtx)
		avformat.SetFlags(t.inputCtx, avformat.GetFlags(t.inputCtx)|avformat.AVFMT_FLAG_CUSTOM_IO)

		var opts avutil.Dictionary
		defer avutil.DictFree(&opts)
		avutil.DictSet(&opts, "probesize", "5000000", 0)
		avutil.DictSet(&opts, "analyzeduration", "5000000", 0)
		avutil.DictSet(&opts, "fflags", "nobuffer", 0)
		avutil.DictSet(&opts, "flags", "low_delay", 0)

		if err := avformat.OpenInput(&t.inputCtx, in, nil, &opts); err != nil {
			return err
		}
	} else {
		var opts avutil.Dictionary
		defer avutil.DictFree(&opts)
		if strings.HasPrefix(in, "http") {
			avutil.DictSet(&opts, "reconnect", "1", 0)
			avutil.DictSet(&opts, "reconnect_at_eof", "1", 0)
			avutil.DictSet(&opts, "reconnect_streamed", "1", 0)
			avutil.DictSet(&opts, "reconnect_delay_max", "30", 0)
			avutil.DictSet(&opts, "timeout", "30000000", 0)
		}
		avutil.DictSet(&opts, "probesize", "5000000", 0)
		avutil.DictSet(&opts, "analyzeduration", "5000000", 0)
		if err := avformat.OpenInput(&t.inputCtx, in, nil, &opts); err != nil {
			return err
		}
	}
	if err := avformat.FindStreamInfo(t.inputCtx, nil); err != nil {
		return err
	}
	t.audioStreamIndex = -1
	for i := 0; i < avformat.GetNbStreams(t.inputCtx); i++ {
		s := avformat.GetStream(t.inputCtx, i)
		p := avformat.GetStreamCodecPar(s)
		if avformat.GetCodecParType(p) == avutil.MediaTypeAudio {
			t.audioStreamIndex = int(avformat.GetStreamIndex(s))
			break
		}
	}
	if t.audioStreamIndex == -1 {
		return errors.New("no audio")
	}
	return nil
}

func (t *AstiavTranscoder) SetupDecoder() error {
	p := avformat.GetStreamCodecPar(avformat.GetStream(t.inputCtx, t.audioStreamIndex))
	d := avcodec.FindDecoder(avformat.GetCodecParCodecID(p))
	if d == nil {
		return errors.New("no decoder")
	}
	t.decoderCtx = avcodec.AllocContext3(d)
	_ = avcodec.ParametersToContext(t.decoderCtx, p)
	return avcodec.Open2(t.decoderCtx, d, nil)
}

func (t *AstiavTranscoder) SetupEncoder() error {
	e := avcodec.FindEncoderByName("libopus")
	if e == nil {
		e = avcodec.FindEncoder(avcodec.CodecIDOPUS)
	}
	if e == nil {
		return errors.New("no encoder")
	}
	t.encoderCtx = avcodec.AllocContext3(e)
	avcodec.SetCtxBitRate(t.encoderCtx, int64(192000))
	avcodec.SetCtxSampleRate(t.encoderCtx, int32(48000))
	avcodec.SetCtxChannelLayout(t.encoderCtx, 2)
	avcodec.SetCtxSampleFmt(t.encoderCtx, int32(avutil.SampleFormatS16))
	avcodec.SetCtxTimeBase(t.encoderCtx, 1, 48000)
	var o avutil.Dictionary
	defer avutil.DictFree(&o)
	avutil.DictSet(&o, "vbr", "on", 0)
	avutil.DictSet(&o, "compression_level", "10", 0)
	avutil.DictSet(&o, "frame_size", "20", 0)
	if err := avcodec.Open2(t.encoderCtx, e, &o); err != nil {
		return err
	}
	t.resampleCtx = swresample.Alloc()
	if t.resampleCtx == nil {
		return errors.New("failed to allocate resampler")
	}
	return nil
}

func (t *AstiavTranscoder) Transcode(ctx context.Context, on func([]byte)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("transcoder panic: %v", r)
			voiceSys.LogInfo("CRITICAL: Transcoder panic recovered: %v", r)
		}
	}()

	defer avformat.PacketUnref(t.packet)
	t.onFrame = on
	defer func() {
		if t.onFrame != nil {
			t.onFrame(nil)
		}
	}()

	fifoSize := 960 * 2
	t.pcmBuffer = make([]byte, 0, fifoSize*4)
	if t.pcmBuffer == nil {
		return errors.New("failed to alloc fifo")
	}
	defer func() {
		if t.pcmBuffer != nil {
			t.pcmBuffer = nil
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

		avformat.PacketUnref(t.packet)

		if err := avformat.ReadFrame(t.inputCtx, t.packet); err != nil {
			if err != nil && strings.Contains(err.Error(), "EOF") {
				break
			}
			return err
		}

		if int(avcodec.GetPacketStreamIndex(t.packet)) != t.audioStreamIndex {
			continue
		}

		if err := avcodec.SendPacket(t.decoderCtx, t.packet); err != nil {
			return err
		}

		for {
			if err := avcodec.ReceiveFrame(t.decoderCtx, t.frame); err != nil {
				break
			}

			if err := t.pushToFifo(); err != nil {
				return err
			}

			avutil.FrameUnref(t.frame)
		}

		if !t.nearingEndTriggered && avformat.GetDuration(t.inputCtx) > 0 {
			t.checkNearingEnd()
		}
	}

	if t.decoderCtx != nil {
		_ = avcodec.SendPacket(t.decoderCtx, nil)
		for {
			if err := avcodec.ReceiveFrame(t.decoderCtx, t.frame); err != nil {
				break
			}
			if err := t.pushToFifo(); err != nil {
				return err
			}
			avutil.FrameUnref(t.frame)
		}
	}

	if err := t.processFifo(true); err != nil {
		return err
	}

	if t.encoderCtx != nil {
		_ = avcodec.SendFrame(t.encoderCtx, nil)
		_ = t.receiveAndWrite()
	}
	return nil
}

func (t *AstiavTranscoder) receiveAndWrite() error {
	for {
		avformat.PacketUnref(t.packet)
		if avcodec.ReceivePacket(t.encoderCtx, t.packet) != nil {
			break
		}
		if t.onFrame != nil {
			d := unsafe.Slice((*byte)(avcodec.GetPacketData(t.packet)), avcodec.GetPacketSize(t.packet))
			fd := make([]byte, len(d))
			copy(fd, d)
			t.onFrame(fd)
		}
	}
	return nil
}

func (t *AstiavTranscoder) handleSeek(ts int64) error {
	num, den := avformat.GetStreamTimeBase(avformat.GetStream(t.inputCtx, t.audioStreamIndex))
	streamTs := int64(ts) * int64(den) / (int64(num) * 48000)

	var err error
	err = avformat.SeekFrame(t.inputCtx, int32(t.audioStreamIndex), streamTs, int32(avformat.SeekFlagBackward))
	if err != nil && ts == 0 {
		err = avformat.SeekFrame(t.inputCtx, -1, 0, int32(avformat.SeekFlagBackward))
	}

	if err != nil {
		voiceSys.LogInfo("SeekFrame failed: %v", err)
	} else {
		if t.decoderCtx != nil {
			avcodec.FreeContext(&t.decoderCtx)
		}
		if t.encoderCtx != nil {
			avcodec.FreeContext(&t.encoderCtx)
		}
		if t.resampleCtx != nil {
			swresample.Free(&t.resampleCtx)
		}

		if err := t.SetupDecoder(); err != nil {
			return err
		}
		if err := t.SetupEncoder(); err != nil {
			return err
		}

		if t.pcmBuffer != nil {
			t.pcmBuffer = nil
			t.pcmBuffer = make([]byte, 0, 960*4)
		}
		atomic.StoreInt64(&t.pts, ts)
	}
	return nil
}

func (t *AstiavTranscoder) checkNearingEnd() {
	totalSecs := float64(avformat.GetDuration(t.inputCtx)) / 1000000.0
	currentSecs := float64(atomic.LoadInt64(&t.pts)) / 48000.0
	threshold := math.Max(7, math.Min(totalSecs*0.1, 20))
	if currentSecs > totalSecs-threshold {
		t.nearingEndTriggered = true
		if t.OnNearingEnd != nil {
			t.OnNearingEnd()
		}
	}
}

func (t *AstiavTranscoder) encodeAndWrite(f avutil.Frame) error {
	if err := avcodec.SendFrame(t.encoderCtx, f); err != nil {
		return err
	}
	return t.receiveAndWrite()
}

func (t *AstiavTranscoder) pushToFifo() error {
	avutil.FrameUnref(t.resampleFrame)
	avutil.FrameSetChannels(t.resampleFrame, avcodec.GetCtxChannels(t.encoderCtx))
	avutil.SetFrameFormat(t.resampleFrame, avcodec.GetCtxSampleFmt(t.encoderCtx))
	avutil.SetFrameSampleRate(t.resampleFrame, avcodec.GetCtxSampleRate(t.encoderCtx))
	nb := int(int64(avutil.GetFrameNbSamples(t.frame)) * int64(avcodec.GetCtxSampleRate(t.encoderCtx)) / int64(avutil.GetFrameSampleRate(t.frame)))
	if nb > 0 {
		avutil.SetFrameNbSamples(t.resampleFrame, int32(nb))
		_ = avutil.FrameGetBufferErr(t.resampleFrame, 0)
		if swresample.ConvertFrame(t.resampleCtx, t.resampleFrame, t.frame) == nil {
			size := avutil.GetFrameNbSamples(t.resampleFrame) * 2 * 2
			dataPtr := (*byte)(avutil.GetFrameDataPlane(t.resampleFrame, 0))
			t.pcmBuffer = append(t.pcmBuffer, unsafe.Slice(dataPtr, size)...)
			return t.processFifo(false)
		}
	}
	return nil
}

func (t *AstiavTranscoder) processFifo(drain bool) error {
	if t.pcmBuffer == nil {
		return nil
	}
	for {
		sz := 960
		if (len(t.pcmBuffer) / 4) < sz {
			if !drain || (len(t.pcmBuffer)/4) == 0 {
				return nil
			}
			sz = (len(t.pcmBuffer) / 4)
		}
		avutil.FrameUnref(t.resampleFrame)
		avutil.SetFrameNbSamples(t.resampleFrame, int32(sz))
		avutil.FrameSetChannels(t.resampleFrame, avcodec.GetCtxChannels(t.encoderCtx))
		avutil.SetFrameFormat(t.resampleFrame, avcodec.GetCtxSampleFmt(t.encoderCtx))
		avutil.SetFrameSampleRate(t.resampleFrame, avcodec.GetCtxSampleRate(t.encoderCtx))
		_ = avutil.FrameGetBufferErr(t.resampleFrame, 0)
		chunk := t.pcmBuffer[:sz*4]
		size := avutil.GetFrameNbSamples(t.resampleFrame) * 2 * 2
		dataPtr := (*byte)(avutil.GetFrameDataPlane(t.resampleFrame, 0))
		copy(unsafe.Slice(dataPtr, size), chunk)
		t.pcmBuffer = t.pcmBuffer[sz*4:]

		t.frameCount++

		if t.volume != nil {
			vol := t.volume.Load()
			if vol != 100 {
				size := avutil.GetFrameNbSamples(t.resampleFrame) * 2 * 2
				dataPtr := (*byte)(avutil.GetFrameDataPlane(t.resampleFrame, 0))
				data := unsafe.Slice(dataPtr, size)
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
			}
		}

		avutil.SetFramePTS(t.resampleFrame, atomic.LoadInt64(&t.pts))
		atomic.AddInt64(&t.pts, int64(sz))
		if err := t.encodeAndWrite(t.resampleFrame); err != nil {
			return err
		}
	}
}

func (t *AstiavTranscoder) Close() {
	if t.resampleCtx != nil {
		swresample.Free(&t.resampleCtx)
	}
	if t.resampleFrame != nil {
		avutil.FrameFree(&t.resampleFrame)
	}
	if t.packet != nil {
		avcodec.PacketFree(&t.packet)
	}
	if t.frame != nil {
		avutil.FrameFree(&t.frame)
	}
	if t.decoderCtx != nil {
		avcodec.FreeContext(&t.decoderCtx)
	}
	if t.encoderCtx != nil {
		avcodec.FreeContext(&t.encoderCtx)
	}
	if t.inputCtx != nil {
		avformat.CloseInput(&t.inputCtx)
		avformat.FreeContext(t.inputCtx)
	}
}
