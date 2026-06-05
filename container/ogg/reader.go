package ogg

import "io"

// Reader reads Opus packets from an Ogg container.
// It parses the Ogg stream and extracts Opus packets for decoding.
type Reader struct {
	r             io.Reader
	rs            io.ReadSeeker
	Header        *OpusHead // Parsed ID header (set after NewReader)
	Tags          *OpusTags // Parsed comment header (set after NewReader)
	granulePos    uint64    // Last granule position seen
	eos           bool      // End of stream reached
	partialPacket []byte    // For packets spanning pages
	pending       []packetEntry
	serial        uint32 // Stream serial number
	audioOffset   int64  // Stream offset of the first audio page for seekable inputs
	pageBuffer    []byte // Buffer for reading pages
	bufferOffset  int    // Current position in buffer
	bufferLen     int    // Valid data in buffer
	page          Page   // Reused parse target; Segments/Payload alias pageBuffer
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

	// Parse OpusHead from first page.
	packets := page.Packets()
	if len(packets) == 0 {
		return nil, ErrInvalidHeader
	}

	or.Header, err = ParseOpusHead(packets[0])
	if err != nil {
		return nil, err
	}

	or.serial = page.SerialNumber

	// Read comment page(s) with OpusTags.
	// OpusTags may span multiple pages if there are many comments.
	var tagsData []byte

	for {
		page, err = or.readPage()
		if err != nil {
			return nil, err
		}

		// Verify serial number matches.
		if page.SerialNumber != or.serial {
			return nil, ErrInvalidPage
		}

		// Check for continuation.
		if page.IsContinuation() && len(tagsData) == 0 {
			return nil, ErrInvalidPage // Can't continue from nothing.
		}

		// Collect payload.
		tagsData = append(tagsData, page.Payload...)

		// Check if we have a complete packet.
		// If the last segment is < 255, the packet is complete.
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

// ReadPacket reads the next Opus packet from the stream, reassembling packets
// that span page boundaries via the lacing table. It returns the packet bytes,
// the granule position attributed to that packet (derived from the page granule
// and the per-packet duration), and any error.
//
// The returned slice is freshly allocated and owned by the caller; it is not
// overwritten by later reads. Packets from logical bitstreams whose serial
// number differs from the first stream are skipped. ReadPacket returns io.EOF
// once the end-of-stream page has been consumed and no buffered packets remain;
// a malformed or truncated page surfaces the underlying parse error.
func (or *Reader) ReadPacket() (packet []byte, granulePos uint64, err error) {
	// Drain any pending packets first.
	if len(or.pending) > 0 {
		entry := or.pending[0]
		or.pending = or.pending[1:]
		or.granulePos = entry.granulePos
		return entry.data, entry.granulePos, nil
	}

	if or.eos {
		return nil, 0, io.EOF
	}

	for {
		page, readErr := or.readPage()
		if readErr != nil {
			if readErr == io.EOF {
				or.eos = true
			}
			return nil, 0, readErr
		}

		if page.SerialNumber != or.serial {
			// Skip unrelated logical streams.
			continue
		}

		if !page.IsContinuation() && len(or.partialPacket) > 0 {
			// Dropped continuation; reset to avoid splicing unrelated packets.
			or.partialPacket = nil
		}

		or.appendPagePackets(page)

		if page.IsEOS() {
			or.eos = true
		}

		if len(or.pending) > 0 {
			entry := or.pending[0]
			or.pending = or.pending[1:]
			or.granulePos = entry.granulePos
			return entry.data, entry.granulePos, nil
		}

		if or.eos {
			return nil, 0, io.EOF
		}
	}
}

// ReadPacketInto reads the next Opus packet into dst, avoiding the per-packet
// allocation of ReadPacket. It returns the number of bytes copied and the
// packet's granule position.
//
// If the packet does not fit in dst it returns ErrPacketTooLarge with n == 0;
// note the packet has already been consumed from the stream in that case, so
// dst should be sized to the largest expected Opus packet. Other errors,
// including io.EOF at end of stream, are propagated from ReadPacket.
func (or *Reader) ReadPacketInto(dst []byte) (n int, granulePos uint64, err error) {
	packet, granule, err := or.ReadPacket()
	if err != nil {
		return 0, 0, err
	}
	if len(packet) > len(dst) {
		return 0, 0, ErrPacketTooLarge
	}
	n = copy(dst, packet)
	return n, granule, nil
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
	or.partialPacket = nil
	or.pending = nil
	or.bufferOffset = 0
	or.bufferLen = 0

	for {
		packet, granule, err := or.ReadPacket()
		if err != nil {
			return err
		}
		if granule >= target {
			packetCopy := make([]byte, len(packet))
			copy(packetCopy, packet)
			or.pending = append([]packetEntry{{data: packetCopy, granulePos: granule}}, or.pending...)
			or.granulePos = 0
			or.eos = false
			return nil
		}
	}
}

type packetEntry struct {
	data       []byte
	granulePos uint64
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

func assignPacketGranules(pageGranule uint64, entries []packetEntry) {
	if len(entries) == 0 {
		return
	}

	trailingDuration := uint64(0)
	for i := len(entries) - 1; i >= 0; i-- {
		duration, ok := packetDuration48k(entries[i].data)
		if !ok {
			for j := range entries {
				entries[j].granulePos = pageGranule
			}
			return
		}

		if pageGranule >= trailingDuration {
			entries[i].granulePos = pageGranule - trailingDuration
		} else {
			entries[i].granulePos = 0
		}

		trailingDuration += duration
	}
}

// appendPagePackets rebuilds packets across page boundaries using the segment table.
func (or *Reader) appendPagePackets(page *Page) {
	offset := 0
	pendingStart := len(or.pending)
	for _, seg := range page.Segments {
		segLen := int(seg)
		if offset+segLen > len(page.Payload) {
			segLen = len(page.Payload) - offset
		}

		if segLen > 0 {
			or.partialPacket = append(or.partialPacket, page.Payload[offset:offset+segLen]...)
			offset += segLen
		}

		if seg < 255 {
			if len(or.partialPacket) > 0 {
				packetCopy := make([]byte, len(or.partialPacket))
				copy(packetCopy, or.partialPacket)
				or.pending = append(or.pending, packetEntry{data: packetCopy})
			}
			or.partialPacket = or.partialPacket[:0]
		}
	}
	assignPacketGranules(page.GranulePos, or.pending[pendingStart:])
}

// readPage reads the next Ogg page from the stream.
func (or *Reader) readPage() (*Page, error) {
	// First, try to parse from existing buffer.
	for {
		if or.bufferLen > or.bufferOffset {
			consumed, err := parsePageInto(or.pageBuffer[or.bufferOffset:or.bufferLen], &or.page)
			if err == nil {
				or.bufferOffset += consumed
				return &or.page, nil
			}
			// Not enough data, need to read more.
		}

		// Compact buffer if needed.
		if or.bufferOffset > 0 {
			remaining := or.bufferLen - or.bufferOffset
			if remaining > 0 {
				copy(or.pageBuffer, or.pageBuffer[or.bufferOffset:or.bufferLen])
			}
			or.bufferLen = remaining
			or.bufferOffset = 0
		}

		// Read more data.
		if or.bufferLen >= len(or.pageBuffer) {
			// Buffer full but no complete page - expand buffer.
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
				// Try to parse remaining data.
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
