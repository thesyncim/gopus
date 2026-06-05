package ogg

import "io"

// Reader reads Opus packets from an Ogg container.
// It parses the Ogg stream and extracts Opus packets for decoding.
type Reader struct {
	r           io.Reader
	rs          io.ReadSeeker
	Header      *OpusHead // Parsed ID header (set after NewReader)
	Tags        *OpusTags // Parsed comment header (set after NewReader)
	granulePos  uint64    // Granule position of the last returned packet
	eos         bool      // End-of-stream page consumed
	serial      uint32    // Stream serial number
	audioOffset int64     // Stream offset of the first audio page for seekable inputs

	pageBuffer   []byte // Read buffer; parsed pages alias it
	bufferOffset int    // Start of unconsumed bytes in pageBuffer
	bufferLen    int    // End of valid bytes in pageBuffer

	page     Page // Current page, parsed zero-copy over pageBuffer
	havePage bool // page holds a loaded page of this stream
	segIdx   int  // Next unread lacing segment in page.Segments
	payOff   int  // Next unread byte in page.Payload

	pktScratch []byte // Reused assembly buffer backing ReadPacket
	pushback   []byte // One-packet pushback set by SeekGranule
	pushbackG  uint64 // Granule of the pushed-back packet
	hasPush    bool   // pushback holds a packet
}

// readerBufferSize is the size of the internal read buffer.
const readerBufferSize = 64 * 1024 // 64KB

// NewReader creates a Reader over r and parses the Ogg Opus headers up front:
// it reads the beginning-of-stream page carrying OpusHead and the following
// page(s) carrying OpusTags, exposing them via the Header and Tags fields. The
// comment header may span multiple pages and is reassembled before returning.
//
// It returns ErrNilReader if r is nil, ErrInvalidPage or ErrBadCRC if the
// leading pages are malformed or corrupt, and ErrInvalidHeader if the OpusHead
// or OpusTags packets are not well-formed. If r also implements io.ReadSeeker,
// the Reader records the offset of the first audio page so SeekGranule can be
// used later.
func NewReader(r io.Reader) (*Reader, error) {
	if r == nil {
		return nil, ErrNilReader
	}

	or := &Reader{
		r:          r,
		pageBuffer: make([]byte, readerBufferSize),
	}
	if rs, ok := r.(io.ReadSeeker); ok {
		or.rs = rs
	}

	// Read BOS page with OpusHead.
	page, err := or.readPage()
	if err != nil {
		return nil, err
	}

	if !page.IsBOS() {
		return nil, ErrInvalidPage
	}

	// Parse OpusHead from the first page.
	packets := page.Packets()
	if len(packets) == 0 {
		return nil, ErrInvalidHeader
	}

	or.Header, err = ParseOpusHead(packets[0])
	if err != nil {
		return nil, err
	}

	or.serial = page.SerialNumber

	// Read comment page(s) with OpusTags. OpusTags may span multiple pages if
	// there are many comments.
	var tagsData []byte
	for {
		page, err = or.readPage()
		if err != nil {
			return nil, err
		}
		if page.SerialNumber != or.serial {
			return nil, ErrInvalidPage
		}
		if page.IsContinuation() && len(tagsData) == 0 {
			return nil, ErrInvalidPage // Can't continue from nothing.
		}
		tagsData = append(tagsData, page.Payload...)
		// A final lacing value < 255 terminates the packet.
		if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] < 255 {
			break
		}
	}

	or.Tags, err = ParseOpusTags(tagsData)
	if err != nil {
		return nil, err
	}
	if or.rs != nil {
		offset, offsetErr := or.streamOffset()
		if offsetErr != nil {
			return nil, offsetErr
		}
		or.audioOffset = offset
	}

	return or, nil
}

// ReadPacket reads the next Opus packet, reassembling packets that span page
// boundaries via the lacing table. It returns the packet bytes, the granule
// position attributed to that packet, and any error.
//
// The returned slice is freshly allocated and owned by the caller; it is not
// overwritten by later reads. Packets from logical bitstreams whose serial
// number differs from the first stream are skipped. ReadPacket returns io.EOF
// once the end-of-stream page has been consumed; a malformed or truncated page
// surfaces the underlying parse error. To avoid the per-packet allocation, use
// ReadPacketInto.
func (or *Reader) ReadPacket() (packet []byte, granulePos uint64, err error) {
	out, granule, err := or.nextPacket(or.pktScratch[:0])
	if err != nil {
		return nil, 0, err
	}
	or.pktScratch = out // retain the (possibly grown) backing for reuse
	return append([]byte(nil), out...), granule, nil
}

// ReadPacketInto reads the next Opus packet into dst, allocating nothing when
// the packet fits. It returns the number of bytes written and the packet's
// granule position.
//
// If the packet is larger than dst it returns ErrPacketTooLarge with n == 0; the
// packet has already been consumed in that case, so dst should be sized to the
// largest expected packet. Serial-mismatched bitstreams are skipped and io.EOF
// is returned at end of stream.
func (or *Reader) ReadPacketInto(dst []byte) (n int, granulePos uint64, err error) {
	limit := len(dst)
	out, granule, err := or.nextPacket(dst[:0])
	if err != nil {
		return 0, 0, err
	}
	if len(out) > limit {
		return 0, 0, ErrPacketTooLarge
	}
	return len(out), granule, nil
}

// nextPacket appends the next packet's bytes to dst[:0] and returns the result
// along with its granule position. dst is grown via append only if the packet
// exceeds its capacity, so a caller buffer with enough capacity makes this
// allocation-free. It walks the lacing table across pages, skipping other
// logical streams, dropping abandoned continuations, and clamping truncated
// pages.
func (or *Reader) nextPacket(dst []byte) ([]byte, uint64, error) {
	if or.hasPush {
		or.hasPush = false
		or.granulePos = or.pushbackG
		return append(dst[:0], or.pushback...), or.pushbackG, nil
	}

	for {
		// Position the cursor on an unread segment.
		for !or.havePage || or.segIdx >= len(or.page.Segments) {
			if or.eos {
				return dst[:0], 0, io.EOF
			}
			if err := or.advancePage(); err != nil {
				return dst[:0], 0, err
			}
		}

		dst = dst[:0]
		dropped := false
		for {
			seg := int(or.page.Segments[or.segIdx])
			or.segIdx++
			if avail := len(or.page.Payload) - or.payOff; seg > avail {
				seg = avail // truncated page: take what is present
			}
			dst = append(dst, or.page.Payload[or.payOff:or.payOff+seg]...)
			or.payOff += seg
			if or.page.Segments[or.segIdx-1] < 255 {
				break // a lacing value < 255 terminates the packet
			}
			// A lacing value of 255 only continues the packet onto the next page
			// when the current page is exhausted; otherwise the next segment of
			// this page continues it. The next page must be a continuation —
			// otherwise the partial packet is abandoned and assembly restarts.
			for or.segIdx >= len(or.page.Segments) {
				if or.eos {
					return dst[:0], 0, io.EOF // truncated trailing packet
				}
				if err := or.advancePage(); err != nil {
					return dst[:0], 0, err
				}
				if !or.page.IsContinuation() {
					dropped = true
					break
				}
			}
			if dropped {
				break
			}
		}
		if dropped || len(dst) == 0 {
			continue // restart, or skip an empty packet
		}
		granule := or.packetGranule()
		or.granulePos = granule
		return dst, granule, nil
	}
}

// advancePage loads the next page of this logical stream into or.page (skipping
// other bitstreams), resets the segment cursor, and records the end-of-stream
// flag.
func (or *Reader) advancePage() error {
	for {
		if _, err := or.readPage(); err != nil {
			if err == io.EOF {
				or.eos = true
			}
			return err
		}
		if or.page.SerialNumber != or.serial {
			continue
		}
		or.segIdx = 0
		or.payOff = 0
		or.havePage = true
		if or.page.IsEOS() {
			or.eos = true
		}
		return nil
	}
}

// packetGranule returns the granule position of the packet just assembled: the
// current page granule minus the duration of every packet that completes after
// it on the same page (RFC 7845 §4). If any trailing packet's duration is
// unparseable the page granule is used as a safe fallback.
func (or *Reader) packetGranule() uint64 {
	page := &or.page
	trailing := uint64(0)
	off := or.payOff
	for i := or.segIdx; i < len(page.Segments); {
		start := off
		terminated := false
		for i < len(page.Segments) {
			seg := int(page.Segments[i])
			i++
			if avail := len(page.Payload) - off; seg > avail {
				seg = avail
			}
			off += seg
			if page.Segments[i-1] < 255 {
				terminated = true
				break
			}
		}
		if !terminated {
			break // trailing packet spans out; it does not complete on this page
		}
		dur, ok := packetDuration48k(page.Payload[start:off])
		if !ok {
			return page.GranulePos
		}
		trailing += dur
	}
	if page.GranulePos >= trailing {
		return page.GranulePos - trailing
	}
	return 0
}

// SeekGranule rewinds a seekable stream to the first packet at or after target.
//
// This is a correctness-first linear scan from the first audio page, which keeps
// the API small and deterministic for in-memory or file-backed readers. Later
// optimizations can replace the linear walk with a true bisection search.
func (or *Reader) SeekGranule(target uint64) error {
	if or.rs == nil {
		return ErrNotSeekable
	}
	if _, err := or.rs.Seek(or.audioOffset, io.SeekStart); err != nil {
		return err
	}

	or.granulePos = 0
	or.eos = false
	or.havePage = false
	or.hasPush = false
	or.segIdx = 0
	or.payOff = 0
	or.bufferOffset = 0
	or.bufferLen = 0

	for {
		out, granule, err := or.nextPacket(or.pktScratch[:0])
		if err != nil {
			return err
		}
		or.pktScratch = out
		if granule >= target {
			or.pushback = append(or.pushback[:0], out...)
			or.pushbackG = granule
			or.hasPush = true
			or.granulePos = 0
			or.eos = false
			return nil
		}
	}
}

var opusFrameSizes48k = [32]uint16{
	480, 960, 1920, 2880,
	480, 960, 1920, 2880,
	480, 960, 1920, 2880,
	480, 960,
	480, 960,
	120, 240, 480, 960,
	120, 240, 480, 960,
	120, 240, 480, 960,
	120, 240, 480, 960,
}

func packetDuration48k(packet []byte) (uint64, bool) {
	if len(packet) < 1 {
		return 0, false
	}

	config := packet[0] >> 3
	if config >= uint8(len(opusFrameSizes48k)) {
		return 0, false
	}

	frameSize := opusFrameSizes48k[config]
	frameCount := 0
	switch packet[0] & 0x03 {
	case 0:
		frameCount = 1
	case 1, 2:
		frameCount = 2
	case 3:
		if len(packet) < 2 {
			return 0, false
		}
		frameCount = int(packet[1] & 0x3F)
		if frameCount == 0 || frameCount > 48 {
			return 0, false
		}
	default:
		return 0, false
	}

	return uint64(frameSize) * uint64(frameCount), true
}

func (or *Reader) streamOffset() (int64, error) {
	current, err := or.rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	buffered := int64(or.bufferLen - or.bufferOffset)
	return current - buffered, nil
}

// readPage parses the next Ogg page into the reused or.page, refilling the read
// buffer as needed, and returns a pointer to it.
func (or *Reader) readPage() (*Page, error) {
	for {
		if or.bufferLen > or.bufferOffset {
			consumed, err := parsePageInto(or.pageBuffer[or.bufferOffset:or.bufferLen], &or.page)
			if err == nil {
				or.bufferOffset += consumed
				return &or.page, nil
			}
			// Not enough buffered for a complete page; read more.
		}

		// Compact the buffer.
		if or.bufferOffset > 0 {
			remaining := or.bufferLen - or.bufferOffset
			if remaining > 0 {
				copy(or.pageBuffer, or.pageBuffer[or.bufferOffset:or.bufferLen])
			}
			or.bufferLen = remaining
			or.bufferOffset = 0
		}

		// Grow if a single page exceeds the buffer.
		if or.bufferLen >= len(or.pageBuffer) {
			newBuffer := make([]byte, len(or.pageBuffer)*2)
			copy(newBuffer, or.pageBuffer[:or.bufferLen])
			or.pageBuffer = newBuffer
		}

		n, err := or.r.Read(or.pageBuffer[or.bufferLen:])
		if n > 0 {
			or.bufferLen += n
		}
		if err != nil {
			if err == io.EOF && or.bufferLen > or.bufferOffset {
				consumed, parseErr := parsePageInto(or.pageBuffer[or.bufferOffset:or.bufferLen], &or.page)
				if parseErr == nil {
					or.bufferOffset += consumed
					return &or.page, nil
				}
			}
			return nil, err
		}
	}
}

// PreSkip returns the pre-skip value from the OpusHead header.
// This is the number of samples to discard at the start of decode.
func (or *Reader) PreSkip() uint16 {
	if or.Header != nil {
		return or.Header.PreSkip
	}
	return 0
}

// Channels returns the channel count from the OpusHead header.
func (or *Reader) Channels() uint8 {
	if or.Header != nil {
		return or.Header.Channels
	}
	return 0
}

// SampleRate returns the original sample rate from the OpusHead header.
// Note: Opus always operates at 48kHz internally; this is informational only.
func (or *Reader) SampleRate() uint32 {
	if or.Header != nil {
		return or.Header.SampleRate
	}
	return 0
}

// GranulePos returns the granule position of the last read packet.
func (or *Reader) GranulePos() uint64 {
	return or.granulePos
}

// EOF returns true if the end of stream has been reached.
func (or *Reader) EOF() bool {
	return or.eos
}

// Serial returns the stream serial number.
func (or *Reader) Serial() uint32 {
	return or.serial
}
