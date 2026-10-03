package ogg

import "io"

// Reader reads Opus packets from an Ogg stream. It retains parsing state and is
// not safe for concurrent use.
type Reader struct {
	r             io.Reader
	rs            io.ReadSeeker
	Header        *OpusHead // Parsed ID header (set after NewReader)
	Tags          *OpusTags // Parsed comment header (set after NewReader)
	granulePos    uint64    // Granule position of the last consumed packet
	eos           bool      // End-of-stream page consumed
	serial        uint32    // Stream serial number
	audioOffset   int64     // Stream offset of the first audio page for seekable inputs
	audioSequence uint32    // Sequence number of the first audio page

	lastAudioPageGranule uint64 // Granule position of the last completed audio page
	haveAudioPageGranule bool   // lastAudioPageGranule is known
	seenAudioPacket      bool   // a nonempty audio packet has completed
	audioHistoryKnown    bool   // audio start has no selected-stream sequence gap or orphan packet
	pageIsFirstAudio     bool   // no completed audio packet precedes this page
	pageAudioDuration    uint64 // Duration of completed packets before the current packet
	pageDurationKnown    bool   // pageAudioDuration includes every completed packet
	pageHasAudioPacket   bool   // page has a consumed nonempty packet

	pageBuffer   []byte // Read buffer; parsed pages alias it
	bufferOffset int    // Start of unconsumed bytes in pageBuffer
	bufferLen    int    // End of valid bytes in pageBuffer
	pendingErr   error  // Error returned with buffered bytes, deferred until they are parsed

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

// NewReader returns a Reader for r after parsing the OpusHead and OpusTags
// headers from the initial logical bitstream. It requires a BOS page containing
// OpusHead followed by OpusTags pages with the same serial number. Page lengths
// and CRCs are checked while reading, and nonzero Ogg page versions return
// ErrInvalidPage. It returns ErrNilReader for a nil reader, ErrInvalidPage or
// ErrBadCRC for invalid page framing or checksums,
// ErrInvalidHeader for malformed Opus headers, and propagates errors from r. If
// r implements io.ReadSeeker, the Reader also supports SeekGranule.
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

	// OpusHead is the only packet on the BOS page and must complete there.
	packets := page.Packets()
	if len(packets) == 0 {
		return nil, ErrInvalidHeader
	}
	if page.IsContinuation() || len(packets) != 1 || len(page.Segments) == 0 || page.Segments[len(page.Segments)-1] == 255 {
		return nil, ErrInvalidPage
	}

	or.Header, err = ParseOpusHead(packets[0])
	if err != nil {
		return nil, err
	}

	or.serial = page.SerialNumber

	// Read comment page(s) with OpusTags. OpusTags may span multiple pages if
	// there are many comments.
	var tagsData []byte
	lastSequence := page.PageSequence
	for {
		page, err = or.readPage()
		if err != nil {
			return nil, err
		}
		if page.SerialNumber != or.serial {
			return nil, ErrInvalidPage
		}
		if page.PageSequence != lastSequence+1 {
			return nil, ErrInvalidPage
		}
		lastSequence = page.PageSequence
		if len(page.Segments) > 0 && page.IsContinuation() != (len(tagsData) > 0) {
			return nil, ErrInvalidPage // The flag must match the pending comment packet.
		}

		// Stop at the OpusTags packet terminator, not merely the page's final
		// lacing value. The comment packet must finish its page.
		completed := false
		for i, segment := range page.Segments {
			if segment < 255 {
				if i != len(page.Segments)-1 {
					return nil, ErrInvalidPage
				}
				completed = true
				break
			}
		}
		tagsData = append(tagsData, page.Payload...)
		if completed {
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
	or.audioSequence = lastSequence + 1
	or.audioHistoryKnown = true

	return or, nil
}

// ReadPacket returns the next Opus packet and its granule position, reassembling
// packets that span pages. The returned packet is an independent copy that the
// caller may retain or modify. Pages from other logical bitstreams are skipped.
// Incomplete packets caused by missing pages are discarded.
// It returns io.EOF when the selected stream is exhausted or no more packet
// data can be read; an unterminated trailing packet can also end with io.EOF.
// Page framing, CRC, and underlying read errors are returned to the caller.
func (or *Reader) ReadPacket() (packet []byte, granulePos uint64, err error) {
	out, granule, err := or.nextPacket(or.pktScratch[:0], -1)
	if err != nil {
		return nil, 0, err
	}
	or.pktScratch = out // retain the (possibly grown) backing for reuse
	return append([]byte(nil), out...), granule, nil
}

// ReadPacketInto writes the next Opus packet into dst and returns its length and
// granule position. len(dst), rather than cap(dst), is the size limit; the
// method does not grow dst or write beyond its length. If the packet is larger,
// it discards the excess without allocating packet storage, consumes the packet,
// and returns n == 0, granulePos == 0, and ErrPacketTooLarge. Pages from other
// logical bitstreams are skipped, and io.EOF indicates stream exhaustion.
func (or *Reader) ReadPacketInto(dst []byte) (n int, granulePos uint64, err error) {
	out, granule, err := or.nextPacket(dst[:0], len(dst))
	if err != nil {
		return 0, 0, err
	}
	return len(out), granule, nil
}

// nextPacket appends the next packet's bytes to dst[:0] and returns the result
// along with its granule position. A negative limit allows dst to grow; otherwise
// packets larger than limit are consumed without growing dst and return
// ErrPacketTooLarge. It walks the lacing table across pages, skipping other
// logical streams, dropping abandoned continuations, and clamping truncated
// pages.
func (or *Reader) nextPacket(dst []byte, limit int) ([]byte, uint64, error) {
	if or.hasPush {
		or.hasPush = false
		or.granulePos = or.pushbackG
		if limit >= 0 && len(or.pushback) > limit {
			return dst[:0], 0, ErrPacketTooLarge
		}
		return append(dst[:0], or.pushback...), or.pushbackG, nil
	}

	for {
		// Position the cursor on an unread segment.
		for !or.havePage || or.segIdx >= len(or.page.Segments) {
			if or.eos {
				return dst[:0], 0, io.EOF
			}
			if _, err := or.advancePage(false); err != nil {
				return dst[:0], 0, err
			}
		}

		dst = dst[:0]
		dropped := false
		tooLarge := false
		var packetHeader [2]byte
		packetHeaderLen := 0
		for {
			seg := int(or.page.Segments[or.segIdx])
			or.segIdx++
			if avail := len(or.page.Payload) - or.payOff; seg > avail {
				seg = avail // truncated page: take what is present
			}
			if packetHeaderLen < len(packetHeader) {
				packetHeaderLen += copy(packetHeader[packetHeaderLen:], or.page.Payload[or.payOff:or.payOff+seg])
			}
			if limit >= 0 && seg > limit-len(dst) {
				tooLarge = true
			}
			if !tooLarge {
				dst = append(dst, or.page.Payload[or.payOff:or.payOff+seg]...)
			}
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
				continued, err := or.advancePage(true)
				if err != nil {
					return dst[:0], 0, err
				}
				if !continued {
					dropped = true
					break
				}
			}
			if dropped {
				break
			}
		}
		if dropped || (len(dst) == 0 && !tooLarge) {
			continue // restart, or skip an empty packet
		}
		var granule uint64
		if or.page.IsEOS() {
			duration, durationKnown := packetDuration48k(packetHeader[:packetHeaderLen])
			granule = or.eosPacketGranule(duration, durationKnown)
			if durationKnown && or.pageDurationKnown {
				or.pageAudioDuration += duration
			} else {
				or.pageDurationKnown = false
			}
		} else {
			granule = or.packetGranule()
		}
		or.granulePos = granule
		or.pageHasAudioPacket = true
		if tooLarge {
			return dst[:0], 0, ErrPacketTooLarge
		}
		return dst, granule, nil
	}
}

// advancePage loads the next page of this logical stream and reports whether
// it continues the packet being assembled. RFC 7845 section 3 requires a
// continued packet's pages to have consecutive sequence numbers. A leading
// continuation with no matching prefix is discarded through its first packet
// terminator; subsequent complete packets on the page remain readable. Empty
// pages preserve a pending packet when their sequence numbers are continuous.
func (or *Reader) advancePage(continuePacket bool) (bool, error) {
	// Capture this before readPage can replace or.page with another stream's page.
	expectedSequence := or.audioSequence
	if or.havePage {
		expectedSequence = or.page.PageSequence + 1
	}
	if or.havePage && or.pageHasAudioPacket {
		or.seenAudioPacket = true
		if or.page.GranulePos != ^uint64(0) {
			or.lastAudioPageGranule = or.page.GranulePos
			or.haveAudioPageGranule = true
		} else {
			or.haveAudioPageGranule = false
		}
	}
	for {
		if _, err := or.readPage(); err != nil {
			if err == io.EOF {
				or.eos = true
			}
			return false, err
		}
		if or.page.SerialNumber != or.serial {
			continue
		}
		sequenceContinuous := or.page.PageSequence == expectedSequence
		// RFC 7845 section 3 checks continuation on the next page with packet
		// data; an empty lacing table leaves the pending packet intact.
		continued := continuePacket && or.havePage && sequenceContinuous &&
			(len(or.page.Segments) == 0 || or.page.IsContinuation())
		or.pageIsFirstAudio = false
		or.pageAudioDuration = 0
		or.pageDurationKnown = true
		or.pageHasAudioPacket = false
		if !sequenceContinuous || (len(or.page.Segments) > 0 && or.page.IsContinuation() && !continued) {
			or.haveAudioPageGranule = false
			or.audioHistoryKnown = false
		} else if !or.haveAudioPageGranule && !or.seenAudioPacket && or.audioHistoryKnown {
			or.pageIsFirstAudio = true
		}
		or.segIdx = 0
		or.payOff = 0
		or.havePage = true
		if or.page.IsEOS() {
			or.eos = true
		}
		if or.page.IsContinuation() && !continued {
			for or.segIdx < len(or.page.Segments) {
				seg := or.page.Segments[or.segIdx]
				or.segIdx++
				or.payOff += int(seg)
				if seg < 255 {
					break
				}
			}
		}
		return continued, nil
	}
}

// eosPacketGranule returns the current packet's end position on an EOS page.
// The first audio page establishes its initial position from the page granule
// and decoded duration; later pages continue from the preceding audio page.
func (or *Reader) eosPacketGranule(duration uint64, durationKnown bool) uint64 {
	if !durationKnown || !or.pageDurationKnown {
		return or.packetGranule()
	}

	suffixDuration, suffixKnown, suffixPackets := or.trailingPacketInfo()
	pageGranule := or.page.GranulePos
	if or.haveAudioPageGranule {
		if !suffixKnown {
			return or.packetGranule()
		}
		end, ok := addGranuleDuration(or.lastAudioPageGranule, or.pageAudioDuration, duration)
		if !ok {
			return or.packetGranule()
		}
		if suffixPackets == 0 {
			return pageGranule
		}
		if pageGranule != ^uint64(0) && end > pageGranule {
			return pageGranule
		}
		return end
	}
	if !or.pageIsFirstAudio || !suffixKnown || pageGranule == ^uint64(0) {
		return or.packetGranule()
	}

	total, ok := addGranuleDuration(or.pageAudioDuration, duration, suffixDuration)
	if !ok {
		return or.packetGranule()
	}
	initialGranule := uint64(0)
	if pageGranule > total {
		initialGranule = pageGranule - total
	}
	end, ok := addGranuleDuration(initialGranule, or.pageAudioDuration, duration)
	if !ok {
		return or.packetGranule()
	}
	if suffixPackets == 0 {
		return pageGranule
	}
	if end > pageGranule {
		return pageGranule
	}
	return end
}

func addGranuleDuration(base, prefix, duration uint64) (uint64, bool) {
	if prefix > ^uint64(0)-base {
		return 0, false
	}
	base += prefix
	if duration > ^uint64(0)-base {
		return 0, false
	}
	return base + duration, true
}

// trailingPacketInfo totals complete packets after the current packet without
// retaining their payloads. The first two bytes are enough to read Opus packet
// duration, including code-3 frame counts.
func (or *Reader) trailingPacketInfo() (uint64, bool, int) {
	page := &or.page
	duration := uint64(0)
	durationsKnown := true
	packets := 0
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
			break // the trailing packet completes on another page
		}
		packets++
		packetDuration, ok := packetDuration48k(page.Payload[start:off])
		if !ok {
			durationsKnown = false
			continue
		}
		duration += packetDuration
	}
	return duration, durationsKnown, packets
}

// packetGranule is the backward fallback for non-EOS pages and EOS pages whose
// packet durations are unknown. It subtracts the durations of later packets
// that complete on the same page; known EOS trimming uses eosPacketGranule.
// If a trailing packet's duration is unparseable, the page granule is the safe
// fallback.
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

// SeekGranule rewinds a seekable stream to its first audio page and scans for
// the first packet whose computed granule position is at least target. On
// success, the next ReadPacket or ReadPacketInto returns that packet; repeated
// calls start the scan again from the first audio page. It returns ErrNotSeekable
// if the source does not implement io.ReadSeeker, io.EOF if no packet reaches
// target, and propagates seek or read errors.
func (or *Reader) SeekGranule(target uint64) error {
	if or.rs == nil {
		return ErrNotSeekable
	}
	if _, err := or.rs.Seek(or.audioOffset, io.SeekStart); err != nil {
		return err
	}

	or.granulePos = 0
	or.eos = false
	or.lastAudioPageGranule = 0
	or.haveAudioPageGranule = false
	or.seenAudioPacket = false
	or.audioHistoryKnown = true
	or.pageIsFirstAudio = false
	or.pageAudioDuration = 0
	or.pageDurationKnown = false
	or.pageHasAudioPacket = false
	or.havePage = false
	or.hasPush = false
	or.segIdx = 0
	or.payOff = 0
	or.bufferOffset = 0
	or.bufferLen = 0
	or.pendingErr = nil

	for {
		out, granule, err := or.nextPacket(or.pktScratch[:0], -1)
		if err != nil {
			return err
		}
		or.pktScratch = out
		if granule >= target {
			or.pushback = append(or.pushback[:0], out...)
			or.pushbackG = granule
			or.hasPush = true
			or.granulePos = 0
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

	duration := uint64(frameSize) * uint64(frameCount)
	// libopus src/opus_decoder.c:opus_packet_get_nb_samples rejects packets
	// longer than 120 ms. Unknown durations use the page-granule fallback.
	if duration > 5760 {
		return 0, false
	}
	return duration, true
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
// buffer as needed. It rejects unsupported page versions without consuming the
// page, and returns a pointer to valid version-0 pages.
func (or *Reader) readPage() (*Page, error) {
	for {
		if or.bufferLen > or.bufferOffset {
			data := or.pageBuffer[or.bufferOffset:or.bufferLen]
			consumed, err := parsePageInto(data, &or.page)
			if err == nil {
				if or.page.Version != 0 {
					return nil, ErrInvalidPage
				}
				or.bufferOffset += consumed
				return &or.page, nil
			}
			if err != ErrInvalidPage || !hasOggMagicPrefix(data) {
				return nil, err
			}
			// A valid capture-pattern prefix can still be an incomplete page.
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

		if or.pendingErr != nil {
			err := or.pendingErr
			or.pendingErr = nil
			return nil, err
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
			if err == io.EOF {
				if or.bufferLen == or.bufferOffset {
					return nil, io.EOF
				}
				data := or.pageBuffer[or.bufferOffset:or.bufferLen]
				consumed, parseErr := parsePageInto(data, &or.page)
				if parseErr == nil {
					if or.page.Version != 0 {
						return nil, ErrInvalidPage
					}
					or.bufferOffset += consumed
					return &or.page, nil
				}
				return nil, parseErr
			}
			if n > 0 {
				or.pendingErr = err
				continue
			}
			return nil, err
		}
	}
}

// hasOggMagicPrefix reports whether data starts with the bytes of the Ogg
// capture pattern it contains. A mismatch is permanent even when the header
// has not reached its full fixed size yet.
func hasOggMagicPrefix(data []byte) bool {
	limit := len(data)
	if limit > len(oggMagic) {
		limit = len(oggMagic)
	}
	for i := 0; i < limit; i++ {
		if data[i] != oggMagic[i] {
			return false
		}
	}
	return true
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

// SampleRate returns the original input rate recorded in OpusHead. Ogg Opus
// granule positions use 48 kHz sample units.
func (or *Reader) SampleRate() uint32 {
	if or.Header != nil {
		return or.Header.SampleRate
	}
	return 0
}

// GranulePos returns the granule position of the most recently assembled
// packet, in 48 kHz sample units. It is reset to zero by a successful
// SeekGranule and updated when the selected packet is read; an oversized packet
// consumed by ReadPacketInto also updates it.
func (or *Reader) GranulePos() uint64 {
	return or.granulePos
}

// EOF reports whether the Reader has seen the selected stream's EOS page or
// has encountered io.EOF while requesting more input. It can be true while
// packets already loaded from the EOS page remain unread.
func (or *Reader) EOF() bool {
	return or.eos
}

// Serial returns the stream serial number.
func (or *Reader) Serial() uint32 {
	return or.serial
}
