package webrtc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

type TrackMeta struct {
	Path    string
	Title   string
	Artists []string
}

// PublishNowPlaying updates the shared metadata used by /status.
func PublishNowPlaying(title string, artists []string) {
	if str == nil {
		return
	}
	str.nowPlayingLock.Lock()
	str.nowPlayingTitle = title

	dst := make([]string, 0, len(artists))
	dst = append(dst, artists...)
	str.nowPlayingArtists = dst

	str.nowPlayingLock.Unlock()
}

func CurrentNowPlaying() (title string, artists []string) {
	if str == nil {
		return "", []string{}
	}
	str.nowPlayingLock.RLock()
	title = str.nowPlayingTitle

	out := make([]string, 0, len(str.nowPlayingArtists))
	out = append(out, str.nowPlayingArtists...)
	str.nowPlayingLock.RUnlock()
	return title, out
}

// LoadOpusPlaylist returns all *.opus (Ogg Opus) files in mediaDir,
// with best-effort Title/Artist extracted from OpusTags.
// This is unsorted for now, but will be sorted in the future.
func LoadOpusPlaylist(mediaDir string) ([]TrackMeta, error) {
	if mediaDir == "" {
		mediaDir = "media"
	}

	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".opus" { // ONLY .opus
			paths = append(paths, filepath.Join(mediaDir, e.Name()))
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("no .opus files found")
	}

	out := make([]TrackMeta, 0, len(paths))
	for _, p := range paths {
		title, artists := readOpusTagsBestEffort(p)
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		out = append(out, TrackMeta{
			Path:    p,
			Title:   title,
			Artists: artists,
		})
	}

	return out, nil
}

// Best-effort OpusTags parse. Returns ("", nil) if missing/unreadable.
func readOpusTagsBestEffort(path string) (title string, artists []string) {
	f, err := os.Open(path)
	if err != nil {
		return "", []string{}
	}
	defer func() { _ = f.Close() }()

	pr := newOggPacketReader(f)

	var artistVals []string

	for {
		pkt, err := pr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(pkt) < 8 {
			break
		}

		// find the OpusTags packet (reassembled)
		if bytes.Equal(pkt[:8], []byte("OpusTags")) {
			tags, err := oggreader.ParseOpusTags(pkt)
			if err != nil {
				break
			}

			for _, c := range tags.UserComments {
				key := strings.ToLower(strings.TrimSpace(c.Comment))
				val := strings.TrimSpace(c.Value)

				switch key {
				case "title":
					if title == "" && val != "" {
						title = val
					}

				case "artist":
					if val != "" {
						artistVals = append(artistVals, val)
					}
				}
			}
			break
		}
	}

	// normalize + de-dupe artists (never return nil)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(artistVals))
	for _, v := range artistVals {
		for _, a := range splitArtists(v) {
			if a == "" {
				continue
			}
			if _, ok := seen[a]; ok {
				continue
			}
			seen[a] = struct{}{}
			out = append(out, a)
		}
	}

	return title, out
}

type oggPacketReader struct {
	r *bufio.Reader

	// In-progress packet spanning pages.
	carry []byte

	// Queue of completed packets.
	queue [][]byte
	qHead int

	// Reused buffers to avoid allocs each page.
	hdr    [27]byte
	segArr [255]byte // fixed segment table storage (max 255 segments/page)
	buf    []byte    // reused page payload buffer
}

func newOggPacketReader(r io.Reader) *oggPacketReader {
	return &oggPacketReader{
		r:   bufio.NewReaderSize(r, 256*1024),
		buf: make([]byte, 0, 255*255), // max page payload size: 65025
	}
}

func (o *oggPacketReader) Next() ([]byte, error) {
	for {
		if o.qHead < len(o.queue) {
			p := o.queue[o.qHead]

			// Clear reference so GC can collect old packets.
			o.queue[o.qHead] = nil
			o.qHead++

			// If drained, reset without retaining old references.
			if o.qHead == len(o.queue) {
				o.queue = o.queue[:0]
				o.qHead = 0
			}

			return p, nil
		}

		if err := o.readNextPagePackets(); err != nil {
			return nil, err
		}
	}
}

func (o *oggPacketReader) readNextPagePackets() error {
	if _, err := io.ReadFull(o.r, o.hdr[:]); err != nil {
		return err
	}

	// Avoid string allocation: check "OggS"
	if o.hdr[0] != 'O' || o.hdr[1] != 'g' || o.hdr[2] != 'g' || o.hdr[3] != 'S' {
		return fmt.Errorf("invalid ogg capture pattern: %q", o.hdr[0:4])
	}

	pageSegments := int(o.hdr[26]) // 0..255
	seg := o.segArr[:pageSegments]

	if _, err := io.ReadFull(o.r, seg); err != nil {
		return err
	}

	total := 0
	for _, s := range seg {
		total += int(s)
	}

	// Reuse page payload buffer.
	if cap(o.buf) < total {
		o.buf = make([]byte, total)
	} else {
		o.buf = o.buf[:total]
	}

	if _, err := io.ReadFull(o.r, o.buf); err != nil {
		return err
	}

	cur := o.carry
	o.carry = nil

	off := 0
	for _, s := range seg {
		n := int(s)
		if n > 0 {
			if off+n > len(o.buf) {
				return fmt.Errorf("ogg page corrupt: segment overflow")
			}
			// Copies out of o.buf into the packet buffer.
			cur = append(cur, o.buf[off:off+n]...)
			off += n
		}

		// Packet ends when s < 255
		if s < 255 {
			if len(cur) > 0 {
				o.queue = append(o.queue, cur)
			}
			cur = nil
		}
	}

	// Packet continues across pages if last segment was 255.
	if len(cur) > 0 {
		o.carry = cur
	}

	return nil
}

func splitArtists(v string) []string {
	s := strings.TrimSpace(v)
	if s == "" {
		return nil
	}

	// keep it conservative; avoid splitting on commas in case artist names contain commas
	seps := []string{" feat. ", " ft. ", " featuring ", ";", " & ", "/", " x "}
	out := []string{s}
	for _, sep := range seps {
		var next []string
		for _, cur := range out {
			parts := strings.Split(cur, sep)
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					next = append(next, p)
				}
			}
		}
		out = next
	}
	return out
}
