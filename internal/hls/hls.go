package hls

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/philipch07/EggsFM/internal/audio"
	"github.com/philipch07/EggsFM/internal/viewers"
)

type Config struct {
	OutputDir           string
	FfmpegPath          string
	SegmentCacheControl string
	Cursor              *audio.Cursor
}

type Streamer struct {
	dir       string
	ffmpegBin string
	cmd       *exec.Cmd
	stdin     *io.PipeWriter
	sink      *pipeSink
	cursor    *audio.Cursor

	handler http.Handler

	mu        sync.RWMutex
	startedAt time.Time
	closed    chan struct{}
	closeOnce sync.Once
	restarts  uint64
}

const (
	playlistCacheControl   = "no-store, max-age=0"
	playlistFilename       = "live.m3u8"
	hlsPipeBufferSlots     = 256
	ffmpegRestartDelay     = 2 * time.Second
	ffmpegRestartMaxDelay  = 30 * time.Second
	ffmpegStaleCheckEvery  = 10 * time.Second
	ffmpegStalePlaylistAge = 45 * time.Second
	// at 48kHz this muxer overflows past 12h so restart before we get close
	ffmpegMaxUptime = 8 * time.Hour
)

// Start spawns an ffmpeg process that consumes a live Ogg Opus stream from stdin
// and emits HLS (fMP4) fragments + manifests in OutputDir.
func Start(cfg Config) (*Streamer, error) {
	if cfg.Cursor == nil {
		return nil, errors.New("cursor is required to start HLS")
	}

	dir := cfg.OutputDir
	if strings.TrimSpace(dir) == "" {
		dir = "hls"
	}

	ffmpegPath := cfg.FfmpegPath
	if strings.TrimSpace(ffmpegPath) == "" {
		ffmpegPath = "ffmpeg"
	}

	ffmpegBin, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found (required for HLS/AAC): %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create hls output dir: %w", err)
	}
	if err := wipeDir(dir); err != nil {
		return nil, err
	}

	segmentCacheControl := strings.TrimSpace(cfg.SegmentCacheControl)
	if segmentCacheControl == "" {
		segmentCacheControl = playlistCacheControl
	}

	streamer := &Streamer{
		dir:       dir,
		ffmpegBin: ffmpegBin,
		cursor:    cfg.Cursor,
		closed:    make(chan struct{}),
		handler:   newFileHandler(dir, playlistCacheControl, segmentCacheControl),
	}
	streamer.sink = newPipeSink(streamer)

	cmd, pw, err := streamer.startTranscoder()
	if err != nil {
		return nil, err
	}
	streamer.setTranscoder(cmd, pw, false)

	go streamer.supervise(cmd, pw)
	go streamer.monitorPlaylist()

	snap := cfg.Cursor.Snapshot()
	log.Printf(
		"HLS ready at /api/hls/ (output: %s, cursor start=%s, offset=%s)",
		dir,
		snap.StartedAt.Format(time.RFC3339),
		snap.Position,
	)

	return streamer, nil
}

// AudioWriter returns a best-effort writer for the live Opus/Ogg stream.
func (s *Streamer) AudioWriter() io.Writer {
	return s.sink
}

// DropCount returns the total number of dropped HLS audio writes.
func (s *Streamer) DropCount() uint64 {
	if s == nil || s.sink == nil {
		return 0
	}
	return s.sink.DropCount()
}

// Handler serves the generated HLS outputs with cache headers.
func (s *Streamer) Handler() http.Handler {
	return s.handler
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

	headerMu      sync.RWMutex
	header        []byte
	collector     *audio.OpusHeaderCollector
	primeMu       sync.Mutex
	primeFor      *io.PipeWriter
	primeWarnOnce sync.Once
	syncNeeded    uint32
	primeHeaders  bool
}

func newPipeSink(parent *Streamer) *pipeSink {
	sink := &pipeSink{
		parent:    parent,
		buf:       make(chan []byte, hlsPipeBufferSlots),
		collector: audio.NewOpusHeaderCollector(),
	}
	go sink.drain()
	return sink
}

func (p *pipeSink) DropCount() uint64 {
	return atomic.LoadUint64(&p.dropCnt)
}

func (p *pipeSink) primeWriter(w *io.PipeWriter, allowHeader bool) {
	if w == nil {
		return
	}
	p.primeMu.Lock()
	p.primeFor = w
	p.primeHeaders = allowHeader
	p.primeMu.Unlock()
	if allowHeader {
		atomic.StoreUint32(&p.syncNeeded, 1)
	} else {
		atomic.StoreUint32(&p.syncNeeded, 0)
	}
}

func (p *pipeSink) primeIfNeeded(w *io.PipeWriter) {
	p.primeMu.Lock()
	if p.primeFor != w {
		p.primeMu.Unlock()
		return
	}
	allowHeader := p.primeHeaders
	p.primeMu.Unlock()

	if !allowHeader {
		p.primeMu.Lock()
		if p.primeFor == w {
			p.primeFor = nil
		}
		p.primeMu.Unlock()
		return
	}

	header := p.headerCopy()
	if len(header) == 0 {
		return
	}

	p.primeMu.Lock()
	if p.primeFor == w {
		p.primeFor = nil
	}
	p.primeMu.Unlock()

	if _, err := w.Write(header); err != nil {
		atomic.AddUint64(&p.dropCnt, 1)
		p.primeWarnOnce.Do(func() {
			log.Printf("hls sink dropped header: %v", err)
		})
		p.parent.dropStdin(w)
	}
}

func (p *pipeSink) headerCopy() []byte {
	p.headerMu.RLock()
	defer p.headerMu.RUnlock()
	if len(p.header) == 0 {
		return nil
	}
	cp := make([]byte, len(p.header))
	copy(cp, p.header)
	return cp
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

	if p.collector != nil {
		if header := p.collector.Feed(buf); header != nil {
			p.headerMu.Lock()
			p.header = header
			p.headerMu.Unlock()
		}
	}

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
			log.Printf("hls sink dropping audio: buffer full")
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

		p.primeIfNeeded(w)

		if atomic.LoadUint32(&p.syncNeeded) != 0 {
			if idx := bytes.Index(b, []byte("OggS")); idx >= 0 {
				b = b[idx:]
				atomic.StoreUint32(&p.syncNeeded, 0)
			} else {
				continue
			}
		}

		if _, err := w.Write(b); err != nil {
			atomic.AddUint64(&p.dropCnt, 1)
			p.warnOnce.Do(func() {
				log.Printf("hls sink dropped audio: %v", err)
			})
			p.parent.dropStdin(w)
		}
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

func newFileHandler(dir, playlistCacheControl, segmentCacheControl string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewers.TrackRequest(viewers.ProtocolHLS, r)

		cacheControl := playlistCacheControl
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")

		case strings.HasSuffix(r.URL.Path, ".m4s"):
			w.Header().Set("Content-Type", "video/iso.segment")
			cacheControl = segmentCacheControl

		case strings.HasSuffix(r.URL.Path, ".mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			cacheControl = segmentCacheControl
		}

		if cacheControl != "" {
			w.Header().Set("Cache-Control", cacheControl)
		}

		fileServer.ServeHTTP(w, r)
	})
}

func wipeDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read hls dir: %w", err)
	}

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove %q: %w", path, err)
		}
	}

	return nil
}

func buildArgs(segmentPrefix string) []string {
	logLevel := strings.TrimSpace(os.Getenv("FFMPEG_LOGLEVEL_HLS"))
	if logLevel == "" {
		logLevel = "warning"
	}

	common := []string{
		"-hide_banner",
		"-loglevel", logLevel,
		"-fflags", "+igndts+genpts",
		"-use_wallclock_as_timestamps", "1",
		"-f", "ogg",
		"-i", "pipe:0",
		"-map", "0:a:0",
		"-c:a", "aac",
		"-ac", "2",
		"-ar", "48000",
		"-b:a", "192k",
		"-profile:a", "aac_low",
		"-af", "asetpts=N/SR/TB",
	}

	segmentPrefix = strings.TrimSuffix(strings.TrimSpace(segmentPrefix), "/")
	segmentPattern := "segment_%05d.m4s"
	initFilename := "init.mp4"
	if segmentPrefix != "" {
		segmentPattern = segmentPrefix + "/segment_%05d.m4s"
		initFilename = segmentPrefix + "/init.mp4"
	}
	segmentDuration := "3"
	hlsFlags := strings.Join([]string{
		"delete_segments",
		"independent_segments",
		"omit_endlist",
		"program_date_time",
		"temp_file",
	}, "+")

	args := append(common,
		"-f", "hls",
		"-hls_time", segmentDuration,
		"-hls_init_time", segmentDuration,
		"-hls_list_size", "32",
		"-hls_delete_threshold", "200",
		"-hls_flags", hlsFlags,
		"-strftime_mkdir", "1",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", initFilename,
		"-hls_segment_filename", segmentPattern,
		"-master_pl_name", "master.m3u8",
		"-hls_allow_cache", "0",
		playlistFilename,
	)

	return args
}

func (s *Streamer) setTranscoder(cmd *exec.Cmd, stdin *io.PipeWriter, allowHeaderPrime bool) {
	s.mu.Lock()
	old := s.stdin
	s.stdin = stdin
	s.cmd = cmd
	s.startedAt = time.Now()
	s.mu.Unlock()

	if s.sink != nil {
		s.sink.primeWriter(stdin, allowHeaderPrime)
	}

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

func (s *Streamer) startTranscoder() (*exec.Cmd, *io.PipeWriter, error) {
	segmentPrefix := filepath.Join("segments", uuid.New().String())
	segmentDir := filepath.Join(s.dir, segmentPrefix)
	if err := os.MkdirAll(segmentDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create hls segment dir: %w", err)
	}

	pr, pw := io.Pipe()
	args := buildArgs(filepath.ToSlash(segmentPrefix))

	cmd := exec.Command(s.ffmpegBin, args...)
	cmd.Dir = s.dir
	cmd.Stdin = pr
	cmd.Stdout = io.Discard
	cmd.Stderr = &lineLogger{prefix: "ffmpeg (hls): "}

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, nil, fmt.Errorf("start ffmpeg for hls: %w", err)
	}

	return cmd, pw, nil
}

func (s *Streamer) supervise(cmd *exec.Cmd, stdin *io.PipeWriter) {
	backoff := ffmpegRestartDelay

	for {
		if err := cmd.Wait(); err != nil {
			log.Printf("hls transcoder exited: %v", err)
		} else {
			log.Println("hls transcoder exited cleanly")
		}

		// Exit cleanly when the streamer is closed; otherwise keep trying with backoff.
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

			nextCmd, nextStdin, err := s.startTranscoder()
			if err != nil {
				log.Printf("hls transcoder restart failed: %v", err)
				backoff *= 2
				if backoff > ffmpegRestartMaxDelay {
					backoff = ffmpegRestartMaxDelay
				}
				continue
			}

			s.restarts++
			s.setTranscoder(nextCmd, nextStdin, true)
			cmd = nextCmd
			stdin = nextStdin
			backoff = ffmpegRestartDelay
			break
		}
	}
}

func (s *Streamer) monitorPlaylist() {
	ticker := time.NewTicker(ffmpegStaleCheckEvery)
	defer ticker.Stop()

	playlistPath := filepath.Join(s.dir, playlistFilename)

	for {
		select {
		case <-ticker.C:
		case <-s.closed:
			return
		}

		s.mu.RLock()
		cmd := s.cmd
		startedAt := s.startedAt
		s.mu.RUnlock()

		if cmd == nil || cmd.Process == nil {
			continue
		}

		if time.Since(startedAt) > ffmpegMaxUptime {
			log.Printf("hls transcoder uptime exceeded; restarting to wrap timestamps")
			_ = cmd.Process.Kill()
			continue
		}

		info, err := os.Stat(playlistPath)
		if err != nil {
			if time.Since(startedAt) > ffmpegStalePlaylistAge {
				log.Printf("hls playlist missing; restarting ffmpeg")
				_ = cmd.Process.Kill()
			}
			continue
		}

		if time.Since(info.ModTime()) > ffmpegStalePlaylistAge && time.Since(startedAt) > ffmpegStalePlaylistAge {
			log.Printf("hls playlist stale; restarting ffmpeg")
			_ = cmd.Process.Kill()
		}
	}
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
