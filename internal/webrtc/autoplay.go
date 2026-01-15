package webrtc

import (
	"bufio"
	"bytes"
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/philipch07/EggsFM/internal/audio"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

const (
	webrtcSampleBufferSlots = 256
	autoplayStopTimeout     = 5 * time.Second
)

var (
	autoplayState struct {
		mu       sync.Mutex
		running  bool
		mediaDir string
		stop     chan struct{}
		done     chan struct{}
		writer   *sampleWriter
	}

	errAutoplayStopped = errors.New("autoplay stopped")
)

type sampleWriter struct {
	track    *webrtc.TrackLocalStaticSample
	buf      chan media.Sample
	dropOnce sync.Once
	errOnce  sync.Once
	dropCnt  uint64
	closed   uint32
}

func newSampleWriter(track *webrtc.TrackLocalStaticSample) *sampleWriter {
	writer := &sampleWriter{
		track: track,
		buf:   make(chan media.Sample, webrtcSampleBufferSlots),
	}
	go writer.drain()
	return writer
}

func (w *sampleWriter) writeSample(sample media.Sample) {
	if atomic.LoadUint32(&w.closed) != 0 {
		return
	}
	select {
	case w.buf <- sample:
		return
	default:
		atomic.AddUint64(&w.dropCnt, 1)
		w.dropOnce.Do(func() {
			log.Printf("autoplay: dropping webrtc samples (buffer full)")
		})
	}
}

func (w *sampleWriter) close() {
	if !atomic.CompareAndSwapUint32(&w.closed, 0, 1) {
		return
	}
	close(w.buf)
}

func (w *sampleWriter) DropCount() uint64 {
	return atomic.LoadUint64(&w.dropCnt)
}

func (w *sampleWriter) drain() {
	for sample := range w.buf {
		if err := w.track.WriteSample(sample); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				continue
			}
			w.errOnce.Do(func() {
				log.Printf("autoplay: webrtc sample write error: %v", err)
			})
		}
	}
}

// StartAutoplayFromMediaDir loads all .opus files from mediaDir and begins the stream
// it also loops the playlist (all the files) indefinitely.
func StartAutoplayFromMediaDir(mediaDir string) error {
	if mediaDir == "" {
		mediaDir = "media"
	}

	track, err := GetAudioTrack()
	if err != nil {
		return err
	}

	playlist, err := LoadOpusPlaylist(mediaDir)
	if err != nil {
		return err
	}
	if len(playlist) == 0 {
		return fmt.Errorf("no .opus tracks found in %q", mediaDir)
	}

	autoplayState.mu.Lock()
	if autoplayState.running {
		autoplayState.mu.Unlock()
		return nil
	}
	autoplayState.running = true
	autoplayState.mediaDir = mediaDir
	stop := make(chan struct{})
	done := make(chan struct{})
	writer := newSampleWriter(track)
	autoplayState.stop = stop
	autoplayState.done = done
	autoplayState.writer = writer
	autoplayState.mu.Unlock()

	log.Printf("Loaded %d track(s) from %q", len(playlist), mediaDir)

	// Publish + log the first track immediately on start
	first := playlist[0]
	log.Printf("Now playing: %q", filepath.Base(first.Path))
	PublishNowPlaying(first.Title, first.Artists)

	go func() {
		autoplayPlaylistLoop(playlist, writer, stop)
		close(done)
	}()

	return nil
}

// RestartAutoplay stops the current autoplay loop (if any) and starts it again.
func RestartAutoplay() error {
	return RestartAutoplayFromMediaDir("")
}

// RestartAutoplayFromMediaDir restarts autoplay using the specified media dir.
func RestartAutoplayFromMediaDir(mediaDir string) error {
	autoplayState.mu.Lock()
	if mediaDir == "" {
		mediaDir = autoplayState.mediaDir
	}
	running := autoplayState.running
	stop := autoplayState.stop
	done := autoplayState.done
	writer := autoplayState.writer
	autoplayState.running = false
	autoplayState.stop = nil
	autoplayState.done = nil
	autoplayState.writer = nil
	autoplayState.mu.Unlock()

	if running && stop != nil {
		close(stop)
		if done != nil {
			select {
			case <-done:
			case <-time.After(autoplayStopTimeout):
			}
		}
	}

	if writer != nil {
		writer.close()
	}

	if mediaDir == "" {
		return errors.New("autoplay not started")
	}

	return StartAutoplayFromMediaDir(mediaDir)
}

// AutoplayDropCount returns the total number of dropped WebRTC samples.
func AutoplayDropCount() uint64 {
	autoplayState.mu.Lock()
	writer := autoplayState.writer
	autoplayState.mu.Unlock()

	if writer == nil {
		return 0
	}
	return writer.DropCount()
}

func autoplayPlaylistLoop(list []TrackMeta, writer *sampleWriter, stop <-chan struct{}) {
	if len(list) == 0 {
		return
	}

	i := 0

	// already published track 0 in StartAutoplayFromMediaDir,
	// so seed lastPath to avoid double publish/log on first iteration.
	lastPath := list[0].Path

	for {
		if isAutoplayStopped(stop) {
			return
		}
		m := list[i]

		// track change via log + publish
		if m.Path != lastPath {
			log.Printf("Autoplay: now playing %q", filepath.Base(m.Path))
			PublishNowPlaying(m.Title, m.Artists)
			lastPath = m.Path
		}

		if err := playOnce(m.Path, writer, stop); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				log.Println("autoplay: track closed; stopping")
				return
			}
			if errors.Is(err, errAutoplayStopped) {
				return
			}
			log.Println("autoplay:", err)
			time.Sleep(time.Second)
		}

		i++
		if i >= len(list) {
			i = 0
		}
	}
}

func playOnce(path string, writer *sampleWriter, stop <-chan struct{}) error {
	// ensure that we can play the opus file.
	opusFile, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = opusFile.Close() }()

	// get the opus stream
	rate, err := detectOpusStream(opusFile)
	if err != nil {
		return fmt.Errorf("scan ogg stream: %w", err)
	}
	if _, err := opusFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind ogg: %w", err)
	}

	reader, err := prepareReader(opusFile, rate)
	if err != nil {
		return fmt.Errorf("unable to prepare opus reader: %w", err)
	}

	nextSend := time.Now()

	for {
		if isAutoplayStopped(stop) {
			return errAutoplayStopped
		}
		pkt, dur, _, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("ogg next: %w", err)
		}
		if len(pkt) == 0 {
			continue
		}
		if dur <= 0 {
			dur = 20 * time.Millisecond
		}

		if writer != nil {
			writer.writeSample(media.Sample{Data: pkt, Duration: dur})
		}

		if str != nil && str.cursor != nil {
			str.cursor.Advance(dur)
		}

		nextSend = nextSend.Add(dur)
		// somehow this doesn't break on windows
		if sleep := time.Until(nextSend); sleep > 0 {
			time.Sleep(sleep)
		} else {
			// if fallen behind then resync.
			nextSend = time.Now()
		}
	}
}

func isAutoplayStopped(stop <-chan struct{}) bool {
	if stop == nil {
		return false
	}
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// when preparing the reader we have two options:
//
//  1. if no RESUME_TIMESTAMP is set in the .env cfg then
//     we are simply starting at the beginning of the file.
//
//  2. otherwise, we must seek to the provided timestamp in the file.
//     because we must use a teeReader to pass data from the opus file
//     to ffmpeg for transcoding (this is needed as hls doesn't support opus)
//     we must AVOID using a teeReader while seeking in the file.
//
//     Warning: using the teeReader during large seeks WILL result in a cpu thread
//     being maxed out.
func prepareReader(opusFile *os.File, rate uint32) (*audio.OggOpusPacketReader, error) {
	// if the resume timestamp is NOT set then just set the tee reader
	resumeTimestamp := getResumeTimestamp()
	log.Printf("Resuming at %v", resumeTimestamp)

	// if no timestamp is set in the cfg then use the teeReader immediately.
	if resumeTimestamp == 0 {
		var src io.Reader = opusFile
		if str != nil {
			src = str.teeReader(src)
		}
		return audio.NewOggOpusPacketReader(src, rate), nil
	}

	// a timestamp is set in the cfg so we must seek to that location in the file.
	// first, save the opus headerPages for ffmpeg.
	headerPages, err := audio.ReadOpusHeaderPages(opusFile)
	if err != nil {
		return nil, err
	}

	// seeks to close to the provided timestamp.
	prevGranule, preSkip, err := audio.SeekOffset(opusFile, resumeTimestamp)
	if err != nil {
		return nil, err
	}

	// build the source with the headerPages and the opusFile
	src := io.MultiReader(bytes.NewReader(headerPages), opusFile)

	// now create the tee reader, note that ffmpeg sees headers first
	if str != nil {
		src = str.teeReader(src)
	}

	reader := audio.NewOggOpusPacketReader(src, rate)
	reader.SetSeekState(prevGranule, uint64(preSkip))
	return reader, nil
}

func getResumeTimestamp() time.Duration {
	if rts := os.Getenv("RANDOM_TIMESTAMP"); rts != "" {
		randMax := strings.TrimSpace(rts)

		dur, err := time.ParseDuration(randMax)
		if err == nil { // since we can parse it, make sure it's >= 0.
			dur = max(dur, 0)
		} else {
			// Otherwise treat bare numbers as seconds.
			if secs, err := strconv.ParseFloat(randMax, 64); err == nil && secs > 0 {
				dur = time.Duration(secs * float64(time.Second))
			} else {
				dur = 0
			}
		}

		n, err := crand.Int(crand.Reader, big.NewInt(int64(dur)+1))
		if err != nil { // try non-crypto.
			r := mrand.New(mrand.NewSource(time.Now().UnixNano()))
			return time.Duration(r.Int63n(int64(dur) + 1))
		}

		t := time.Duration(n.Int64())

		return t
	}

	raw := strings.TrimSpace(os.Getenv("RESUME_TIMESTAMP"))
	if raw == "" {
		return 0
	}

	// Prefer Go duration syntax: "90s", "1m30s", etc.
	if d, err := time.ParseDuration(raw); err == nil {
		if d > 0 {
			return d
		}
		return 0
	}

	// Otherwise treat bare numbers as seconds.
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}

	return 0
}

func detectOpusStream(f *os.File) (uint32, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	br := bufio.NewReaderSize(f, 256*1024)
	r, err := oggreader.NewWithOptions(br, oggreader.WithDoChecksum(false))
	if err != nil {
		return 0, err
	}

	for {
		payload, header, err := r.ParseNextPage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		if header == nil || len(payload) < 8 {
			continue
		}

		if ht, ok := header.HeaderType(payload); ok && ht == oggreader.HeaderOpusID {
			head, err := oggreader.ParseOpusHead(payload)
			if err != nil {
				return 0, err
			}
			sr := head.SampleRate
			if sr == 0 {
				sr = 48000
			}
			return sr, nil
		}
	}

	return 0, fmt.Errorf("no Opus stream found")
}
