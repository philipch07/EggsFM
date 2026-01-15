package icecast

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/philipch07/EggsFM/internal/audio"
	"github.com/philipch07/EggsFM/internal/viewers"
)

type Config struct {
	FfmpegPath  string
	Cursor      *audio.Cursor
	StationName string
	StreamPath  string
}

type Streamer struct {
	ffmpegBin   string
	stationName string
	streamPath  string

	cmd    *exec.Cmd
	stdin  *io.PipeWriter
	sink   *pipeSink
	output *broadcaster

	mu        sync.RWMutex
	startedAt time.Time
	closed    chan struct{}
	closeOnce sync.Once
}

const (
	playlistCacheControl = "no-store, max-age=0"

	icecastPipeBufferSlots  = 256
	icecastClientBufferSize = 64
	icecastWarmBytes        = 32 * 1024

	mp3Bitrate    = "128k"
	mp3Channels   = "2"
	mp3SampleRate = "48000"

	ffmpegRestartDelay    = 2 * time.Second
	ffmpegRestartMaxDelay = 30 * time.Second
)

// Start spawns an ffmpeg process that consumes live Ogg Opus from stdin
// and emits MP3 bytes that are fanned out to HTTP listeners.
func Start(cfg Config) (*Streamer, error) {
	if cfg.Cursor == nil {
		return nil, errors.New("cursor is required to start icecast")
	}

	ffmpegPath := strings.TrimSpace(cfg.FfmpegPath)
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	ffmpegBin, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found (required for icecast/mp3): %w", err)
	}

	stationName := strings.TrimSpace(cfg.StationName)
	if stationName == "" {
		stationName = "EggsFM"
	}

	streamPath := strings.TrimSpace(cfg.StreamPath)
	if streamPath == "" {
		streamPath = "/api/icecast.mp3"
	}

	streamer := &Streamer{
		ffmpegBin:   ffmpegBin,
		stationName: stationName,
		streamPath:  streamPath,
		closed:      make(chan struct{}),
		output:      newBroadcaster(),
	}
	streamer.sink = newPipeSink(streamer)

	cmd, pw, stdout, err := streamer.startTranscoder()
	if err != nil {
		return nil, err
	}
	streamer.setTranscoder(cmd, pw)

	go streamer.pipeOutput(cmd, stdout)
	go streamer.supervise(cmd, pw)

	snap := cfg.Cursor.Snapshot()
	log.Printf(
		"Icecast ready at %s (cursor start=%s, offset=%s)",
		streamPath,
		snap.StartedAt.Format(time.RFC3339),
		snap.Position,
	)

	return streamer, nil
}

// AudioWriter returns a best-effort writer for the live Opus/Ogg stream.
func (s *Streamer) AudioWriter() io.Writer {
	return s.sink
}

// DropCount returns the total number of dropped Ogg writes.
func (s *Streamer) DropCount() uint64 {
	if s == nil || s.sink == nil {
		return 0
	}
	return s.sink.DropCount()
}

// Handler serves the live MP3 stream.
func (s *Streamer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.isClosed() {
			http.Error(w, "icecast unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		mp3BitrateKbps := strings.TrimSuffix(mp3Bitrate, "k")
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("icy-name", s.stationName)
		w.Header().Set("icy-description", s.stationName)
		w.Header().Set("icy-br", mp3BitrateKbps)
		w.Header().Set("icy-pub", "1")
		w.Header().Set("ice-audio-info", fmt.Sprintf("bitrate=%s;channels=%s;samplerate=%s", mp3BitrateKbps, mp3Channels, mp3SampleRate))
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		if r.Method == http.MethodHead {
			return
		}

		stopTracking := viewers.TrackConnection(viewers.ProtocolIcecast, r)
		defer stopTracking()

		flusher, _ := w.(http.Flusher)

		seed := s.output.Snapshot()
		for _, chunk := range seed {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}

		client := s.output.AddClient()
		defer s.output.RemoveClient(client)

		for {
			select {
			case <-s.closed:
				return
			case <-r.Context().Done():
				return
			case chunk, ok := <-client.ch:
				if !ok {
					return
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	})
}

// PlaylistHandler serves a simple M3U8 playlist pointing at the stream.
func (s *Streamer) PlaylistHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.isClosed() {
			http.Error(w, "icecast unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/x-mpegURL")
		w.Header().Set("Cache-Control", playlistCacheControl)

		body := s.playlistBody(r)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			log.Printf("icecast playlist write error: %v", err)
		}
	})
}

// Restart forces the ffmpeg transcoder to restart.
func (s *Streamer) Restart() {
	if s == nil {
		return
	}
	if s.isClosed() {
		return
	}
	s.mu.RLock()
	cmd := s.cmd
	s.mu.RUnlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// Close stops the transcoder and background goroutines.
func (s *Streamer) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.closed)

		s.mu.Lock()
		cmd := s.cmd
		stdin := s.stdin
		s.cmd = nil
		s.stdin = nil
		s.mu.Unlock()

		if stdin != nil {
			_ = stdin.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if s.sink != nil {
			s.sink.close()
		}
		if s.output != nil {
			s.output.Close()
		}
	})
}

type pipeSink struct {
	parent    *Streamer
	warnOnce  sync.Once
	dropOnce  sync.Once
	buf       chan []byte
	dropCnt   uint64
	closed    uint32
	closeOnce sync.Once
}

func newPipeSink(parent *Streamer) *pipeSink {
	sink := &pipeSink{
		parent: parent,
		buf:    make(chan []byte, icecastPipeBufferSlots),
	}
	go sink.drain()
	return sink
}

func (p *pipeSink) DropCount() uint64 {
	return atomic.LoadUint64(&p.dropCnt)
}

func (p *pipeSink) close() {
	p.closeOnce.Do(func() {
		atomic.StoreUint32(&p.closed, 1)
		close(p.buf)
	})
}

func (p *pipeSink) Write(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}
	n = len(b)
	if atomic.LoadUint32(&p.closed) != 0 {
		atomic.AddUint64(&p.dropCnt, 1)
		return n, nil
	}
	buf := make([]byte, len(b))
	copy(buf, b)

	defer func() {
		if r := recover(); r != nil {
			atomic.AddUint64(&p.dropCnt, 1)
		}
	}()

	select {
	case p.buf <- buf:
		return n, nil
	default:
		atomic.AddUint64(&p.dropCnt, 1)
		p.dropOnce.Do(func() {
			log.Printf("icecast sink dropping audio: buffer full")
		})
		return n, nil
	}
}

func (p *pipeSink) drain() {
	for b := range p.buf {
		p.parent.mu.RLock()
		w := p.parent.stdin
		p.parent.mu.RUnlock()

		if w == nil {
			atomic.AddUint64(&p.dropCnt, 1)
			continue
		}

		if _, err := w.Write(b); err != nil {
			atomic.AddUint64(&p.dropCnt, 1)
			p.warnOnce.Do(func() {
				log.Printf("icecast sink dropped audio: %v", err)
			})
			p.parent.dropStdin(w)
		}
	}
}

type broadcaster struct {
	mu             sync.RWMutex
	clients        map[*client]struct{}
	closed         bool
	dropCnt        uint64
	recent         [][]byte
	recentBytes    int
	recentMaxBytes int
}

type client struct {
	ch chan []byte
}

func newBroadcaster() *broadcaster {
	return &broadcaster{
		clients:        make(map[*client]struct{}),
		recentMaxBytes: icecastWarmBytes,
	}
}

func (b *broadcaster) AddClient() *client {
	c := &client{
		ch: make(chan []byte, icecastClientBufferSize),
	}
	b.mu.Lock()
	if b.closed {
		close(c.ch)
		b.mu.Unlock()
		return c
	}
	b.clients[c] = struct{}{}
	b.mu.Unlock()
	return c
}

func (b *broadcaster) Snapshot() [][]byte {
	b.mu.RLock()
	if b.closed || len(b.recent) == 0 {
		b.mu.RUnlock()
		return nil
	}
	snapshot := append([][]byte(nil), b.recent...)
	b.mu.RUnlock()
	return snapshot
}

func (b *broadcaster) RemoveClient(c *client) {
	if c == nil {
		return
	}
	b.mu.Lock()
	if _, ok := b.clients[c]; ok {
		delete(b.clients, c)
		close(c.ch)
	}
	b.mu.Unlock()
}

func (b *broadcaster) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	for c := range b.clients {
		delete(b.clients, c)
		close(c.ch)
	}
	b.mu.Unlock()
}

func (b *broadcaster) HasClients() bool {
	b.mu.RLock()
	has := len(b.clients) > 0
	b.mu.RUnlock()
	return has
}

func (b *broadcaster) Broadcast(chunk []byte) {
	if chunk == nil {
		return
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.appendRecentLocked(chunk)
	if len(b.clients) == 0 {
		b.mu.Unlock()
		return
	}
	clients := make([]*client, 0, len(b.clients))
	for c := range b.clients {
		clients = append(clients, c)
	}
	b.mu.Unlock()

	var stale []*client
	for _, c := range clients {
		select {
		case c.ch <- chunk:
		default:
			stale = append(stale, c)
		}
	}

	if len(stale) == 0 {
		return
	}

	b.mu.Lock()
	for _, c := range stale {
		if _, ok := b.clients[c]; ok {
			delete(b.clients, c)
			close(c.ch)
			atomic.AddUint64(&b.dropCnt, 1)
		}
	}
	b.mu.Unlock()
}

func (b *broadcaster) appendRecentLocked(chunk []byte) {
	if b.recentMaxBytes <= 0 {
		return
	}
	b.recent = append(b.recent, chunk)
	b.recentBytes += len(chunk)
	for b.recentBytes > b.recentMaxBytes && len(b.recent) > 0 {
		b.recentBytes -= len(b.recent[0])
		b.recent = b.recent[1:]
	}
}

type lineLogger struct {
	prefix string
}

func (l *lineLogger) Write(p []byte) (int, error) {
	lines := strings.Split(strings.TrimSpace(string(p)), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			log.Printf("%s%s", l.prefix, ln)
		}
	}

	return len(p), nil
}

func buildArgs() []string {
	return []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+igndts+genpts",
		"-use_wallclock_as_timestamps", "1",
		"-flush_packets", "1",
		"-f", "ogg",
		"-i", "pipe:0",
		"-map", "0:a:0",
		"-c:a", "libmp3lame",
		"-ac", mp3Channels,
		"-ar", mp3SampleRate,
		"-b:a", mp3Bitrate,
		"-af", "asetpts=N/SR/TB",
		"-f", "mp3",
		"pipe:1",
	}
}

func (s *Streamer) setTranscoder(cmd *exec.Cmd, stdin *io.PipeWriter) {
	s.mu.Lock()
	old := s.stdin
	s.stdin = stdin
	s.cmd = cmd
	s.startedAt = time.Now()
	s.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
}

func (s *Streamer) clearTranscoder(cmd *exec.Cmd, stdin *io.PipeWriter) {
	s.mu.Lock()
	if s.cmd == cmd {
		s.cmd = nil
	}
	if s.stdin == stdin {
		s.stdin = nil
	}
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
}

func (s *Streamer) dropStdin(stdin *io.PipeWriter) {
	s.mu.Lock()
	if s.stdin == stdin {
		s.stdin = nil
	}
	s.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
}

func (s *Streamer) startTranscoder() (*exec.Cmd, *io.PipeWriter, io.ReadCloser, error) {
	pr, pw := io.Pipe()
	args := buildArgs()

	cmd := exec.Command(s.ffmpegBin, args...)
	cmd.Stdin = pr
	cmd.Stderr = &lineLogger{prefix: "ffmpeg (icecast): "}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, nil, nil, fmt.Errorf("create ffmpeg stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("start ffmpeg for icecast: %w", err)
	}

	return cmd, pw, stdout, nil
}

func (s *Streamer) pipeOutput(cmd *exec.Cmd, stdout io.ReadCloser) {
	defer func() { _ = stdout.Close() }()

	buf := make([]byte, 32*1024)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			if s.output != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.output.Broadcast(chunk)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("icecast stdout error: %v", err)
			}
			return
		}
		if s.isClosed() {
			return
		}
	}
}

func (s *Streamer) supervise(cmd *exec.Cmd, stdin *io.PipeWriter) {
	backoff := ffmpegRestartDelay

	for {
		if err := cmd.Wait(); err != nil {
			log.Printf("icecast transcoder exited: %v", err)
		} else {
			log.Println("icecast transcoder exited cleanly")
		}

		if s.isClosed() {
			return
		}

		s.clearTranscoder(cmd, stdin)

		for {
			if s.isClosed() {
				return
			}

			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-s.closed:
				timer.Stop()
				return
			}

			nextCmd, nextStdin, nextStdout, err := s.startTranscoder()
			if err != nil {
				log.Printf("icecast transcoder restart failed: %v", err)
				backoff *= 2
				if backoff > ffmpegRestartMaxDelay {
					backoff = ffmpegRestartMaxDelay
				}
				continue
			}

			s.setTranscoder(nextCmd, nextStdin)
			go s.pipeOutput(nextCmd, nextStdout)

			cmd = nextCmd
			stdin = nextStdin
			backoff = ffmpegRestartDelay
			break
		}
	}
}

func (s *Streamer) playlistBody(r *http.Request) string {
	streamURL := s.streamPath
	if r != nil {
		streamURL = resolveStreamURL(r, streamURL)
	}

	return fmt.Sprintf("#EXTM3U\n#EXTINF:-1,%s\n%s\n", s.stationName, streamURL)
}

func resolveStreamURL(r *http.Request, streamPath string) string {
	if streamPath == "" {
		return ""
	}
	if strings.HasPrefix(streamPath, "http://") || strings.HasPrefix(streamPath, "https://") {
		return streamPath
	}
	if !strings.HasPrefix(streamPath, "/") {
		return streamPath
	}

	host := headerFirst(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	proto := headerFirst(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	return fmt.Sprintf("%s://%s%s", proto, host, streamPath)
}

func headerFirst(value string) string {
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return strings.TrimSpace(value)
}

func (s *Streamer) isClosed() bool {
	if s == nil {
		return true
	}
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}
