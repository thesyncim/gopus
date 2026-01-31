package ogg

import (
	"io"
)

// Reader reads Opus packets from an Ogg container.
// It parses the Ogg stream and extracts Opus packets for decoding.
type Reader struct {
	r             io.Reader
	Header        *OpusHead // Parsed ID header (set after NewReader)
	Tags          *OpusTags // Parsed comment header (set after NewReader)
	granulePos    uint64    // Last granule position seen
	eos           bool      // End of stream reached
	partialPacket []byte    // For packets spanning pages
	pending       []packetEntry
	serial        uint32 // Stream serial number
	pageBuffer    []byte // Buffer for reading pages
	bufferOffset  int    // Current position in buffer
	bufferLen     int    // Valid data in buffer
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
	// Drain any pending packets first.
	if len(or.pending) > 0 {
		entry := or.pending[0]
		or.pending = or.pending[1:]
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

		or.granulePos = page.GranulePos
		or.appendPagePackets(page)

		if page.IsEOS() {
			or.eos = true
		}

		if len(or.pending) > 0 {
			entry := or.pending[0]
			or.pending = or.pending[1:]
			return entry.data, entry.granulePos, nil
		}

		if or.eos {
			return nil, 0, io.EOF
		}
	}
}

// ReadPacketInto reads the next Opus packet into dst.
// Returns the number of bytes copied and the granule position.
func (or *Reader) ReadPacketInto(dst []byte) (int, uint64, error) {
	packet, granule, err := or.ReadPacket()
	if err != nil {
		return 0, 0, err
	}
	if len(packet) > len(dst) {
		return 0, 0, ErrPacketTooLarge
	}
	n := copy(dst, packet)
	return n, granule, nil
}

type packetEntry struct {
	data       []byte
	granulePos uint64
}

// appendPagePackets rebuilds packets across page boundaries using the segment table.
func (or *Reader) appendPagePackets(page *Page) {
	offset := 0
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
				or.pending = append(or.pending, packetEntry{
					data:       packetCopy,
					granulePos: page.GranulePos,
				})
			}
			or.partialPacket = nil
		}
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
