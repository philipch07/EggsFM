package hls

import (
	"bytes"
	"encoding/binary"
)

var (
	opusHeadSig = [8]byte{'O', 'p', 'u', 's', 'H', 'e', 'a', 'd'}
	opusTagsSig = [8]byte{'O', 'p', 'u', 's', 'T', 'a', 'g', 's'}
)

// opusHeaderCollector watches a raw Ogg Opus byte stream and caches the
// OpusHead + OpusTags pages for the current logical stream so we can replay
// them when ffmpeg is restarted. It is intentionally lightweight and only
// cares about the initial headers.
type opusHeaderCollector struct {
	buf     bytes.Buffer // raw bytes for the cached header pages
	scratch []byte       // partial data that doesn't yet form a full page

	carry      []byte // packet continuation across pages
	seenHead   bool
	seenTags   bool
	headerDone bool
	serial     uint32
}

func newOpusHeaderCollector() *opusHeaderCollector {
	return &opusHeaderCollector{}
}

// Feed consumes a chunk of the stream. If it finishes caching the header for
// the current logical bitstream, it returns a copy of the header bytes.
func (c *opusHeaderCollector) Feed(chunk []byte) []byte {
	if len(chunk) == 0 {
		return nil
	}
	c.scratch = append(c.scratch, chunk...)

	for {
		// need at least the fixed 27-byte page header.
		if len(c.scratch) < 27 {
			return nil
		}

		// ensure we are aligned on an Ogg page. If not, discard until we find one.
		if !bytes.HasPrefix(c.scratch, []byte("OggS")) {
			if idx := bytes.Index(c.scratch[1:], []byte("OggS")); idx >= 0 {
				c.scratch = c.scratch[idx+1:]
			} else {
				c.scratch = c.scratch[:0]
			}
			continue
		}

		pageSegments := int(c.scratch[26])
		if len(c.scratch) < 27+pageSegments {
			return nil
		}

		segTable := c.scratch[27 : 27+pageSegments]
		payloadBytes := 0
		for _, s := range segTable {
			payloadBytes += int(s)
		}

		pageLen := 27 + pageSegments + payloadBytes
		if len(c.scratch) < pageLen {
			return nil
		}

		page := c.scratch[:pageLen]
		c.scratch = c.scratch[pageLen:]

		headerType := page[5]
		if headerType&0x02 != 0 {
			c.buf.Reset()
			c.carry = nil
			c.seenHead = false
			c.seenTags = false
			c.headerDone = false
			c.serial = binary.LittleEndian.Uint32(page[14:18])
		}

		if c.headerDone {
			continue
		}

		c.buf.Write(page)

		payload := page[27+pageSegments:]
		pkt := c.carry
		offset := 0

		for _, lace := range segTable {
			size := int(lace)
			if size > 0 {
				pkt = append(pkt, payload[offset:offset+size]...)
				offset += size
			}

			if lace < 255 {
				if len(pkt) >= 8 {
					prefix := pkt[:8]
					switch {
					case !c.seenHead && bytes.Equal(prefix, opusHeadSig[:]):
						c.seenHead = true
					case !c.seenTags && bytes.Equal(prefix, opusTagsSig[:]):
						c.seenTags = true
					}
				}
				pkt = nil

				if c.seenHead && c.seenTags {
					c.headerDone = true
					hdr := make([]byte, c.buf.Len())
					copy(hdr, c.buf.Bytes())
					return hdr
				}
			}
		}

		c.carry = pkt
	}
}
