package webrtc

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

var autoplayOnce sync.Once

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

	autoplayOnce.Do(func() {
		log.Printf("Loaded %d track(s) from %q", len(playlist), mediaDir)

		// Publish + log the first track immediately on start
		first := playlist[0]
		log.Printf("Now playing: %q", filepath.Base(first.Path))
		PublishNowPlaying(first.Title, first.Artists)

		go autoplayPlaylistLoop(playlist, track)
	})

	return nil
}

func autoplayPlaylistLoop(list []TrackMeta, track *webrtc.TrackLocalStaticSample) {
	if len(list) == 0 {
		return
	}

	i := 0

	// already published track 0 in StartAutoplayFromMediaDir,
	// so seed lastPath to avoid double publish/log on first iteration.
	lastPath := list[0].Path

	for {
		m := list[i]

		// track change via log + publish
		if m.Path != lastPath {
			log.Printf("Autoplay: now playing %q", filepath.Base(m.Path))
			PublishNowPlaying(m.Title, m.Artists)
			lastPath = m.Path
		}

		if err := playOnce(m.Path, track); err != nil {
			if errors.Is(err, io.ErrClosedPipe) {
				log.Println("autoplay: track closed; stopping")
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

func playOnce(path string, track *webrtc.TrackLocalStaticSample) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	serial, rate, err := detectOpusStream(f)
	if err != nil {
		return fmt.Errorf("scan ogg stream: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind ogg: %w", err)
	}

	var src io.Reader = f
	if str != nil {
		src = str.teeReader(src)
	}

	reader := newOggOpusPacketReader(src, serial, rate)

	offset := 0 * time.Second
	if magfest := os.Getenv("MAGFEST"); magfest != "" {
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			loc = time.FixedZone("EST", -5*60*60)
		}

		now := time.Now().In(loc)
		log.Printf("curr time (NY): %s", now.Format(time.RFC3339Nano))

		// Next 11:30pm in EST
		target := time.Date(now.Year(), now.Month(), now.Day(), 23, 30, 0, 0, loc)
		if !now.Before(target) {
			target = target.Add(24 * time.Hour)
		}
		log.Printf("target (NY):    %s", target.Format(time.RFC3339))

		desired := 29*time.Hour + 4*time.Minute
		timeUntilTarget := target.Sub(now)

		offset = max(desired-timeUntilTarget, 0)
	} else {
		offset = getResumeOffset()
	}

	log.Printf("offset seconds: %v", offset.Seconds())
	log.Printf("offset time: %v", offset.Hours())

	nextSend := time.Now()
	remaining := offset
	started := offset <= 0

	for {
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

		// skip until we reach the offset.
		// this is insanely slow...
		if !started {
			for remaining > dur {
				remaining -= dur
				str.cursor.Advance(dur)
				log.Printf("skipping, %v remaining", remaining)
				reader.Next()
			}
			log.Print("ready")
			started = true
		}

		if err := track.WriteSample(media.Sample{Data: pkt, Duration: dur}); err != nil {
			return err
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

func detectOpusStream(f *os.File) (uint32, uint32, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}

	br := bufio.NewReaderSize(f, 256*1024)
	r, err := oggreader.NewWithOptions(br, oggreader.WithDoChecksum(false))
	if err != nil {
		return 0, 0, err
	}

	for {
		payload, header, err := r.ParseNextPage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, 0, err
		}
		if header == nil || len(payload) < 8 {
			continue
		}

		if ht, ok := header.HeaderType(payload); ok && ht == oggreader.HeaderOpusID {
			head, err := oggreader.ParseOpusHead(payload)
			if err != nil {
				return 0, 0, err
			}
			sr := head.SampleRate
			if sr == 0 {
				sr = 48000
			}
			return header.Serial, sr, nil
		}
	}

	return 0, 0, fmt.Errorf("no Opus stream found")
}

func getResumeOffset() time.Duration {
	raw := strings.TrimSpace(os.Getenv("RESUME_OFFSET"))
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

type oggOpusPacketReader struct {
	r *bufio.Reader

	// In-progress audio packet that continues across pages.
	carry []byte

	// If we're currently discarding a header packet (OpusHead/OpusTags)
	// that spans multiple pages, keep discarding until it terminates.
	discardingHeader bool

	lastGranule  uint64
	sampleRate   uint32
	activeSerial uint32
	skipSerials  map[uint32]struct{}

	// Queue of packets to return (avoid slice-shift retention).
	queue []queuedPkt
	qHead int

	// Reused buffers (no per-page allocs).
	hdr    [27]byte
	segArr [255]byte
	buf    []byte
}

type queuedPkt struct {
	data    []byte
	dur     time.Duration
	granule uint64
}

var (
	opusHeadSig = [8]byte{'O', 'p', 'u', 's', 'H', 'e', 'a', 'd'}
	opusTagsSig = [8]byte{'O', 'p', 'u', 's', 'T', 'a', 'g', 's'}
)

func newOggOpusPacketReader(r io.Reader, serial uint32, sampleRate uint32) *oggOpusPacketReader {
	if sampleRate == 0 {
		sampleRate = 48000
	}
	return &oggOpusPacketReader{
		r:            bufio.NewReaderSize(r, 256*1024),
		buf:          make([]byte, 0, 255*255),
		sampleRate:   sampleRate,
		activeSerial: serial,
		skipSerials:  map[uint32]struct{}{},
	}
}

func (o *oggOpusPacketReader) Next() ([]byte, time.Duration, uint64, error) {
	for {
		if o.qHead < len(o.queue) {
			q := o.queue[o.qHead]
			o.queue[o.qHead] = queuedPkt{}
			o.qHead++

			if o.qHead == len(o.queue) {
				o.queue = o.queue[:0]
				o.qHead = 0
			}

			return q.data, q.dur, q.granule, nil
		}

		if o.qHead == len(o.queue) {
			o.queue = o.queue[:0]
			o.qHead = 0
		}

		granule, start, n, err := o.appendNextAudioPagePacketsToQueue()
		if err != nil {
			return nil, 0, 0, err
		}
		if n == 0 {
			continue
		}

		var pageSamples uint64
		if granule > o.lastGranule {
			pageSamples = granule - o.lastGranule
		} else {
			pageSamples = 960 * uint64(n)
		}
		o.lastGranule = granule

		sr := o.sampleRate
		if sr == 0 {
			sr = 48000
		}

		pageDur := time.Duration(pageSamples) * time.Second / time.Duration(sr)
		if pageDur <= 0 {
			pageDur = 20 * time.Millisecond * time.Duration(n)
		}

		base := pageDur / time.Duration(n)
		if base <= 0 {
			base = 20 * time.Millisecond
		}
		rem := pageDur - base*time.Duration(n-1)
		if rem <= 0 {
			rem = base
		}

		for i := 0; i < n; i++ {
			d := base
			if i == n-1 {
				d = rem
			}
			o.queue[start+i].granule = granule
			o.queue[start+i].dur = d
		}
	}
}

func (o *oggOpusPacketReader) appendNextAudioPagePacketsToQueue() (granule uint64, start int, n int, err error) {
	if _, err = io.ReadFull(o.r, o.hdr[:]); err != nil {
		return 0, 0, 0, err
	}

	if o.hdr[0] != 'O' || o.hdr[1] != 'g' || o.hdr[2] != 'g' || o.hdr[3] != 'S' {
		return 0, 0, 0, fmt.Errorf("invalid ogg capture pattern: %q", o.hdr[0:4])
	}

	granule = binary.LittleEndian.Uint64(o.hdr[6:14])
	pageSegments := int(o.hdr[26])

	segTable := o.segArr[:pageSegments]
	if _, err = io.ReadFull(o.r, segTable); err != nil {
		return 0, 0, 0, err
	}

	total := 0
	for _, s := range segTable {
		total += int(s)
	}

	if cap(o.buf) < total {
		o.buf = make([]byte, total)
	} else {
		o.buf = o.buf[:total]
	}
	if _, err = io.ReadFull(o.r, o.buf); err != nil {
		return 0, 0, 0, err
	}

	start = len(o.queue)

	cur := o.carry
	o.carry = nil

	discarding := o.discardingHeader

	off := 0
	for _, s := range segTable {
		sz := int(s)
		if sz > 0 {
			if off+sz > len(o.buf) {
				return 0, 0, 0, fmt.Errorf("ogg page corrupt: segment overflow")
			}

			if discarding {
				off += sz
			} else {
				cur = append(cur, o.buf[off:off+sz]...)
				off += sz

				if len(cur) >= 8 {
					pfx := cur[:8]
					if bytes.Equal(pfx, opusHeadSig[:]) {
						if head, headErr := oggreader.ParseOpusHead(cur); headErr == nil && head.SampleRate != 0 {
							o.sampleRate = head.SampleRate
						}
						cur = nil
						discarding = true
					} else if bytes.Equal(pfx, opusTagsSig[:]) {
						cur = nil
						discarding = true
					}
				}
			}
		}

		if s < 255 {
			if discarding {
				discarding = false
			} else {
				if len(cur) > 0 {
					o.queue = append(o.queue, queuedPkt{data: cur})
				}
				cur = nil
			}
		}
	}

	o.discardingHeader = discarding

	if !discarding && len(cur) > 0 {
		o.carry = cur
	}

	n = len(o.queue) - start
	return granule, start, n, nil
}
