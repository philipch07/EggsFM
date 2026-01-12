package hls

import (
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
	"time"

	"github.com/google/uuid"
	"github.com/philipch07/EggsFM/internal/audio"
)

type Config struct {
	OutputDir           string
	FfmpegPath          string
	SegmentCacheControl string
	Cursor              *audio.Cursor
}

type Streamer struct {
	dir    string
	cmd    *exec.Cmd
	stdin  *io.PipeWriter
	sink   *pipeSink
	cursor *audio.Cursor

	handler http.Handler

	mu     sync.RWMutex
	closed chan struct{}
}

const playlistCacheControl = "no-store, max-age=0"

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

	segmentPrefix := newSegmentPrefix()
	segmentDir := filepath.Join(dir, segmentPrefix)
	if err := os.MkdirAll(segmentDir, 0o755); err != nil {
		return nil, fmt.Errorf("create hls segment dir: %w", err)
	}

	pr, pw := io.Pipe()

	segmentCacheControl := strings.TrimSpace(cfg.SegmentCacheControl)
	if segmentCacheControl == "" {
		segmentCacheControl = playlistCacheControl
	}

	args := buildArgs(filepath.ToSlash(segmentPrefix))

	cmd := exec.Command(ffmpegBin, args...)
	cmd.Dir = dir
	cmd.Stdin = pr
	cmd.Stdout = io.Discard
	cmd.Stderr = &lineLogger{prefix: "ffmpeg (hls): "}

	streamer := &Streamer{
		dir:     dir,
		cmd:     cmd,
		stdin:   pw,
		cursor:  cfg.Cursor,
		closed:  make(chan struct{}),
		handler: newFileHandler(dir, playlistCacheControl, segmentCacheControl),
	}
	streamer.sink = &pipeSink{parent: streamer}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg for hls: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("hls transcoder exited: %v", err)
		} else {
			log.Println("hls transcoder exited cleanly")
		}

		streamer.mu.Lock()
		streamer.stdin = nil
		streamer.mu.Unlock()

		_ = pw.Close()
		close(streamer.closed)
	}()

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

// Handler serves the generated HLS outputs with cache headers.
func (s *Streamer) Handler() http.Handler {
	return s.handler
}

type pipeSink struct {
	parent   *Streamer
	warnOnce sync.Once
}

func (p *pipeSink) Write(b []byte) (int, error) {
	p.parent.mu.RLock()
	w := p.parent.stdin
	p.parent.mu.RUnlock()

	if w == nil {
		return len(b), nil
	}

	n, err := w.Write(b)
	if err != nil {
		p.warnOnce.Do(func() {
			log.Printf("hls sink dropped audio: %v", err)
		})
		return len(b), nil
	}

	if n < len(b) {
		return len(b), nil
	}

	return n, nil
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

func newSegmentPrefix() string {
	return filepath.Join("segments", uuid.New().String())
}

func buildArgs(segmentPrefix string) []string {
	common := []string{
		"-hide_banner",
		"-loglevel", "warning",
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
	playlistPath := "live.m3u8"
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
		"-hls_list_size", "12",
		"-hls_flags", hlsFlags,
		"-strftime_mkdir", "1",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", initFilename,
		"-hls_segment_filename", segmentPattern,
		"-master_pl_name", "master.m3u8",
		"-hls_allow_cache", "0",
		playlistPath,
	)

	return args
}
