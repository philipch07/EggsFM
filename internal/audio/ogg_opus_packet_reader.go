package audio

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

var (
	opusHeadSig = [8]byte{'O', 'p', 'u', 's', 'H', 'e', 'a', 'd'}
	opusTagsSig = [8]byte{'O', 'p', 'u', 's', 'T', 'a', 'g', 's'}
)

type OggOpusPacketReader struct {
	bufioReader *bufio.Reader

	// In-progress audio packet that continues across pages.
	carry []byte

	// If we're currently discarding a header packet (OpusHead/OpusTags)
	// that spans multiple pages, keep discarding until it terminates.
	isDiscarding bool

	prevGranule uint64
	sampleRate  uint32

	preSkip uint64 // Opus pre-skip in 48kHz samples (RFC 7845)

	// Queue of packets to return.
	queue []queuedPkt
	qHead int

	// Reusable buffers.
	// at most 27 bytes are read for the header
	header [27]byte
	// at most 255 bytes can be read for segments
	segArr [255]byte
	buf    []byte
}

type queuedPkt struct {
	data    []byte
	dur     time.Duration
	granule uint64
}

func NewOggOpusPacketReader(ioReader io.Reader, sampleRate uint32) *OggOpusPacketReader {
	if sampleRate == 0 {
		sampleRate = 48000
	}
	return &OggOpusPacketReader{
		bufioReader: bufio.NewReaderSize(ioReader, 256*1024),
		buf:         make([]byte, 0, 255*255),
		sampleRate:  sampleRate,
	}
}

func (o *OggOpusPacketReader) Next() ([]byte, time.Duration, uint64, error) {
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

		granule, initLen, newPkts, err := o.appendNextAudioPagePacketsToQueue()
		if err != nil {
			return nil, 0, 0, err
		}
		// guard against div by 0
		if newPkts == 0 {
			continue
		}

		var pageSamples uint64
		if granule > o.prevGranule {
			pageSamples = granule - o.prevGranule
		} else {
			pageSamples = 960 * uint64(newPkts)
		}
		o.prevGranule = granule

		// guard against div by 0
		if o.sampleRate == 0 {
			o.sampleRate = 48000
		}

		pageDur := time.Duration(pageSamples) * time.Second / time.Duration(o.sampleRate)
		if pageDur <= 0 {
			pageDur = 20 * time.Millisecond * time.Duration(newPkts)
		}

		base := pageDur / time.Duration(newPkts)
		if base <= 0 {
			base = 20 * time.Millisecond
		}

		rem := pageDur - base*time.Duration(newPkts-1)
		if rem <= 0 {
			rem = base
		}

		for i := range newPkts {
			duration := base
			if i == newPkts-1 {
				duration = rem
			}
			o.queue[initLen+i].granule = granule
			o.queue[initLen+i].dur = duration
		}
	}
}

func (o *OggOpusPacketReader) appendNextAudioPagePacketsToQueue() (granule uint64, initLen int, newPkts int, err error) {
	// read 27 bytes into header
	if _, err = io.ReadFull(o.bufioReader, o.header[:]); err != nil {
		return 0, 0, 0, err
	}

	if o.header[0] != 'O' || o.header[1] != 'g' || o.header[2] != 'g' || o.header[3] != 'S' {
		return 0, 0, 0, fmt.Errorf("invalid ogg capture pattern: %q", o.header[0:4])
	}

	// the number of bytes to read is the 26th header byte (pageSegments)
	segTable := o.segArr[:int(o.header[26])]
	if _, err = io.ReadFull(o.bufioReader, segTable); err != nil {
		return 0, 0, 0, err
	}

	total := 0
	for _, s := range segTable {
		total += int(s)
	}

	// guarantee no overflow
	if cap(o.buf) < total {
		o.buf = make([]byte, total)
	} else {
		o.buf = o.buf[:total]
	}

	// read each segment into o.buf
	if _, err = io.ReadFull(o.bufioReader, o.buf); err != nil {
		return 0, 0, 0, err
	}

	initLen = len(o.queue)

	pkt := o.carry
	o.carry = nil

	offset := 0

	// if any of the packets have a prefix denoting OpusHead or OpusTags then
	// do not append the packet to the queue.
	for _, b := range segTable {
		size := int(b)
		if size > 0 {
			if !o.isDiscarding {
				pkt = append(pkt, o.buf[offset:offset+size]...)

				if len(pkt) >= 8 {
					prefix := pkt[:8]

					// reset pkt and discard any packets that are opus heads or opus tags
					if bytes.Equal(prefix, opusHeadSig[:]) {
						// preSkip is LE u16 at offset 10 (8 sig + 1 ver + 1 ch)
						if len(pkt) >= 12 {
							o.preSkip = uint64(binary.LittleEndian.Uint16(pkt[10:12]))
						}
						pkt = nil
						o.isDiscarding = true
					} else if bytes.Equal(prefix, opusTagsSig[:]) {
						pkt = nil
						o.isDiscarding = true
					}
				}
			}

			// increment offset by the size
			offset += size
		}

		if b < 255 {
			if o.isDiscarding {
				o.isDiscarding = false
			} else {
				if len(pkt) > 0 {
					o.queue = append(o.queue, queuedPkt{data: pkt})
				}
				pkt = nil
			}
		}
	}

	if len(pkt) > 0 {
		o.carry = pkt
	}

	newPkts = len(o.queue) - initLen

	return binary.LittleEndian.Uint64(o.header[6:14]), initLen, newPkts, nil
}

func (o *OggOpusPacketReader) SetSeekState(prevGranule uint64, preSkip uint64) {
	o.prevGranule = prevGranule
	o.preSkip = preSkip
}

// SeekOffset finds the byte offset from the given resumeTimestamp and
// makes the reader seek to that offset if possible.
func SeekOffset(opusFile *os.File, resumeTimestamp time.Duration) (prevGranule uint64, preSkip uint64, err error) {
	offset, prevGranule, preSkip, err := findOffsetFromPlaybackTime(opusFile, resumeTimestamp)
	if err != nil {
		return 0, 0, err
	}

	// seek to the offset in the file
	if _, err := opusFile.Seek(offset, io.SeekStart); err != nil {
		return 0, 0, err
	}

	return prevGranule, preSkip, nil
}

// findOffsetFromPlaybackTime advances the reader so that the next packet returned by Next()
// is at (or shortly after) the requested playback time.
//
// The caller MUST ensure that `target` is >= 0.
//
// This is forward-only: it works on a streaming io.Reader by discarding whole pages
// using granule positions (fast), then parsing one page to align on a packet boundary.
func findOffsetFromPlaybackTime(opusFile *os.File, resumeTimestamp time.Duration) (pageOffset int64, prevGranule uint64, preSkip uint64, err error) {
	// guarantee that we start at the beginning of the file.
	if _, err := opusFile.Seek(0, io.SeekStart); err != nil {
		return 0, 0, 0, err
	}

	var (
		header     [27]byte
		segArr     [255]byte
		carry      []byte
		buf        []byte
		seenHead   bool
		seenTags   bool
		discarding bool // discarding current header packet (OpusHead/OpusTags) across pages

		lastAudioGranule uint64
		targetPCM        = (uint64(resumeTimestamp) * 48000) / uint64(time.Second)
	)

	// keep seeking until we pass the targetPCM.
	for {
		pageStart, _ := opusFile.Seek(0, io.SeekCurrent)

		// read 27 bytes into the header
		if _, err := io.ReadFull(opusFile, header[:]); err != nil {
			return 0, 0, 0, err
		}

		if header[0] != 'O' || header[1] != 'g' || header[2] != 'g' || header[3] != 'S' {
			return 0, 0, 0, fmt.Errorf("invalid ogg capture pattern at %d: %q", pageStart, header[0:4])
		}

		// keep track of the granule so we know how close are relative to the target.
		granule := binary.LittleEndian.Uint64(header[6:14])

		// the number of bytes to read is the 26th header byte (pageSegments)
		segTable := segArr[:int(header[26])]
		if _, err := io.ReadFull(opusFile, segTable); err != nil {
			return 0, 0, 0, err
		}

		total := 0
		for _, s := range segTable {
			total += int(s)
		}

		// guarantee no overflow
		if cap(buf) < total {
			buf = make([]byte, total)
		} else {
			buf = buf[:total]
		}

		// consume until we see either OpusHead/OpusTags so we can parse preSkip
		// and to detect OpusTags packet boundary. then we can safely seek.
		if !seenHead || !seenTags {
			// read each segment into o.buf
			if _, err := io.ReadFull(opusFile, buf); err != nil {
				return 0, 0, 0, err
			}

			// reconstruct packets using the lacing table only to:
			// - parse OpusHead for preSkip
			// - skip OpusTags
			off := 0
			pkt := carry
			carry = nil

			for _, b := range segTable {
				size := int(b)
				if size > 0 && !discarding {
					pkt = append(pkt, buf[off:off+size]...)
				}
				off += size

				// packet boundary
				if b < 255 {
					if discarding {
						discarding = false
					} else if len(pkt) >= 8 {
						// reset pkt and discard any packets that are opus heads or opus tags
						if !seenHead && bytes.Equal(pkt[:8], opusHeadSig[:]) {
							// preSkip is LE u16 at offset 10 (8 sig + 1 ver + 1 ch)
							if len(pkt) >= 12 {
								preSkip = uint64(binary.LittleEndian.Uint16(pkt[10:12]))
							}
							seenHead = true
						} else if !seenTags && bytes.Equal(pkt[:8], opusTagsSig[:]) {
							seenTags = true
						}
					}
					pkt = nil
				}
			}

			if len(pkt) > 0 {
				carry = pkt
			}

			// continue scanning until headers done.
			continue
		}

		// now we can compute the target granule since preSkip is known.
		targetGranule := targetPCM + preSkip

		// if the granule is before target, skip payload via Seek (fast).
		if granule < targetGranule {
			if _, err := opusFile.Seek(int64(total), io.SeekCurrent); err != nil {
				return 0, 0, 0, err
			}

			// keep the last audio granule for duration calculations.
			lastAudioGranule = granule
			continue
		}

		// if granule >= targetGranule then return pageStart as the point to Seek to.
		return pageStart, lastAudioGranule, preSkip, nil
	}
}

// i have no idea.
func ReadOggOpusHeaderPages(opusFile *os.File) (pages []byte, preSkip uint16, err error) {
	if _, err := opusFile.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}

	var (
		hdr    [27]byte
		segArr [255]byte

		out bytes.Buffer

		pktCarry []byte
		gotHead  bool
		gotTags  bool
	)

	for {
		// page start
		if _, err := io.ReadFull(opusFile, hdr[:]); err != nil {
			return nil, 0, err
		}
		if string(hdr[0:4]) != "OggS" {
			return nil, 0, fmt.Errorf("invalid ogg capture pattern: %q", hdr[0:4])
		}

		pageSegments := int(hdr[26])
		segTable := segArr[:pageSegments]
		if _, err := io.ReadFull(opusFile, segTable); err != nil {
			return nil, 0, err
		}

		total := 0
		for _, s := range segTable {
			total += int(s)
		}

		payload := make([]byte, total)
		if _, err := io.ReadFull(opusFile, payload); err != nil {
			return nil, 0, err
		}

		// Save raw page bytes
		out.Write(hdr[:])
		out.Write(segTable)
		out.Write(payload)

		// Reassemble packets to detect OpusHead/OpusTags end
		off := 0
		pkt := pktCarry
		pktCarry = nil

		for _, lace := range segTable {
			n := int(lace)
			if n > 0 {
				pkt = append(pkt, payload[off:off+n]...)
				off += n
			}

			if lace < 255 {
				// packet boundary
				if len(pkt) >= 8 {
					prefix := pkt[:8]
					if !gotHead && bytes.Equal(prefix, opusHeadSig[:]) {
						gotHead = true
						// OpusHead pre-skip is LE uint16 at offset 10
						if len(pkt) >= 12 {
							preSkip = binary.LittleEndian.Uint16(pkt[10:12])
						}
					} else if !gotTags && bytes.Equal(prefix, opusTagsSig[:]) {
						gotTags = true
						// Once OpusTags ends, we have all the headers ffmpeg needs.
					}
				}
				pkt = nil

				if gotHead && gotTags {
					return out.Bytes(), preSkip, nil
				}
			}
		}

		if len(pkt) > 0 {
			pktCarry = pkt
		}
	}
}
