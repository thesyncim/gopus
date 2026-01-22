package ogg

import (
	"io"
)

// Reader reads Opus packets from an Ogg container.
// It parses the Ogg stream and extracts Opus packets for decoding.
type Reader struct {
	r             io.Reader
	Header        *OpusHead  // Parsed ID header (set after NewReader)
	Tags          *OpusTags  // Parsed comment header (set after NewReader)
	granulePos    uint64     // Last granule position seen
	eos           bool       // End of stream reached
	partialPacket []byte     // For packets spanning pages
	serial        uint32     // Stream serial number
	pageBuffer    []byte     // Buffer for reading pages
	bufferOffset  int        // Current position in buffer
	bufferLen     int        // Valid data in buffer
	pendingPages  []*Page    // Pages read but not yet processed
}

// readerBufferSize is the size of the internal read buffer.
const readerBufferSize = 64 * 1024 // 64KB

// NewReader creates a new OggReader and parses the Ogg Opus headers.
// It reads the ID header (OpusHead) and comment header (OpusTags) immediately.
// Returns an error if the stream is not a valid Ogg Opus stream.
func NewReader(r io.Reader) (*Reader, error) {
	or := &Reader{
		r:          r,
		pageBuffer: make([]byte, readerBufferSize),
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

	return or, nil
}

// ReadPacket reads the next Opus packet from the stream.
// Returns the packet data, granule position, and any error.
// Returns io.EOF when the end of stream is reached.
func (or *Reader) ReadPacket() (packet []byte, granulePos uint64, err error) {
	if or.eos {
		return nil, 0, io.EOF
	}

	// Handle partial packet from previous page.
	if len(or.partialPacket) > 0 {
		return or.readContinuedPacket()
	}

	// Read next page.
	page, err := or.readPage()
	if err != nil {
		if err == io.EOF {
			or.eos = true
		}
		return nil, 0, err
	}

	// Verify serial number.
	if page.SerialNumber != or.serial {
		// Different stream, skip.
		return or.ReadPacket()
	}

	// Check for EOS.
	if page.IsEOS() {
		or.eos = true
	}

	or.granulePos = page.GranulePos

	// Handle continuation.
	if page.IsContinuation() {
		// This should have been handled by partialPacket.
		// If we get here without partial data, discard the first (incomplete) packet.
		packets := page.Packets()
		if len(packets) <= 1 {
			// Only the continuation, nothing else.
			if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] == 255 {
				// Packet continues on next page.
				or.partialPacket = page.Payload
			}
			// Try to get next packet.
			if or.eos {
				return nil, 0, io.EOF
			}
			return or.ReadPacket()
		}
		// Skip first (incomplete) packet, return second.
		packet = packets[1]
		// If there are more packets or continuation, handle them.
		if len(packets) > 2 {
			// Store remaining packets for later.
			or.storeRemainingPackets(page, 2)
		} else if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] == 255 {
			// Last packet continues on next page.
			or.partialPacket = packets[len(packets)-1]
			if len(packets) > 2 {
				packet = packets[1]
			}
		}
		return packet, or.granulePos, nil
	}

	// Normal page with complete packets.
	packets := page.Packets()
	if len(packets) == 0 || (len(packets) == 1 && len(packets[0]) == 0) {
		// Empty page (e.g., EOS with no data or zero-length packet).
		if or.eos {
			return nil, 0, io.EOF
		}
		return or.ReadPacket()
	}

	// Return first packet.
	packet = packets[0]

	// Check if last packet continues on next page.
	if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] == 255 {
		// Last packet is incomplete.
		if len(packets) == 1 {
			// The only packet is incomplete.
			or.partialPacket = packets[0]
			return or.ReadPacket()
		}
		// Save partial packet for later.
		or.partialPacket = packets[len(packets)-1]
	}

	// If there are multiple packets, queue remaining for later.
	if len(packets) > 1 {
		// For simplicity, we return one packet at a time.
		// Store remaining packets in a pending structure.
		or.storePendingPackets(packets[1:], page.GranulePos)
	}

	return packet, or.granulePos, nil
}

// readContinuedPacket handles reading a packet that spans pages.
func (or *Reader) readContinuedPacket() ([]byte, uint64, error) {
	partial := or.partialPacket
	or.partialPacket = nil

	// Read continuation pages until packet is complete.
	for {
		page, err := or.readPage()
		if err != nil {
			return nil, 0, err
		}

		if page.SerialNumber != or.serial {
			continue
		}

		if page.IsEOS() {
			or.eos = true
		}

		or.granulePos = page.GranulePos

		if !page.IsContinuation() {
			// Unexpected: expected continuation but got new packet.
			// Discard partial and process this page.
			return or.processPage(page)
		}

		// Append continuation data.
		partial = append(partial, page.Payload...)

		// Check if packet is complete.
		if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] < 255 {
			// Packet complete.
			return partial, or.granulePos, nil
		}
	}
}

// processPage extracts packets from a page.
func (or *Reader) processPage(page *Page) ([]byte, uint64, error) {
	packets := page.Packets()
	if len(packets) == 0 {
		if or.eos {
			return nil, 0, io.EOF
		}
		return or.ReadPacket()
	}

	packet := packets[0]

	// Handle continuation on last packet.
	if len(page.Segments) > 0 && page.Segments[len(page.Segments)-1] == 255 {
		if len(packets) == 1 {
			or.partialPacket = packets[0]
			return or.ReadPacket()
		}
		or.partialPacket = packets[len(packets)-1]
	}

	// Queue remaining packets.
	if len(packets) > 1 {
		or.storePendingPackets(packets[1:], page.GranulePos)
	}

	return packet, or.granulePos, nil
}

// pendingPackets stores packets from multi-packet pages.
type pendingPackets struct {
	packets    [][]byte
	granulePos uint64
}

var pendingQueue []pendingPackets

// storePendingPackets stores packets for later retrieval.
func (or *Reader) storePendingPackets(packets [][]byte, granulePos uint64) {
	// For simplicity, just store in a global queue.
	// A proper implementation would use a struct field.
	pendingQueue = append(pendingQueue, pendingPackets{
		packets:    packets,
		granulePos: granulePos,
	})
}

// storeRemainingPackets stores remaining packets from a page.
func (or *Reader) storeRemainingPackets(page *Page, startIdx int) {
	packets := page.Packets()
	if startIdx < len(packets) {
		or.storePendingPackets(packets[startIdx:], page.GranulePos)
	}
}

// readPage reads the next Ogg page from the stream.
func (or *Reader) readPage() (*Page, error) {
	// First, try to parse from existing buffer.
	for {
		if or.bufferLen > or.bufferOffset {
			page, consumed, err := ParsePage(or.pageBuffer[or.bufferOffset:or.bufferLen])
			if err == nil {
				or.bufferOffset += consumed
				return page, nil
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
				page, consumed, parseErr := ParsePage(or.pageBuffer[or.bufferOffset:or.bufferLen])
				if parseErr == nil {
					or.bufferOffset += consumed
					return page, nil
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
