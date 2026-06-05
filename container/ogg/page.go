package ogg

import (
	"encoding/binary"
)

// Page header flag constants.
const (
	// PageFlagContinuation indicates this page contains data from a packet
	// that began on a previous page.
	PageFlagContinuation = 0x01

	// PageFlagBOS (Beginning of Stream) indicates this is the first page
	// of a logical bitstream.
	PageFlagBOS = 0x02

	// PageFlagEOS (End of Stream) indicates this is the last page of a
	// logical bitstream.
	PageFlagEOS = 0x04
)

// Page header size constants.
const (
	// pageHeaderSize is the fixed portion of the page header (before segment table).
	pageHeaderSize = 27

	// oggMagic is the capture pattern that identifies an Ogg page.
	oggMagic = "OggS"
)

// Page represents a single Ogg page.
type Page struct {
	// Version is the stream structure version (always 0).
	Version byte

	// HeaderType contains page flags (continuation, BOS, EOS).
	HeaderType byte

	// GranulePos is the granule position, representing the number of
	// samples decoded (including this page) at the page's end.
	// For Opus, this is the sample count at 48kHz.
	GranulePos uint64

	// SerialNumber identifies the logical bitstream.
	SerialNumber uint32

	// PageSequence is the page sequence number within the bitstream.
	PageSequence uint32

	// Segments contains the segment table entries.
	// Each entry is the size of a segment (0-255).
	Segments []byte

	// Payload contains the concatenated packet data.
	Payload []byte
}

// BuildSegmentTable creates a segment table for a packet of the given length.
// Packets larger than 255 bytes span multiple segments (each 255 bytes except
// the final segment which contains the remainder).
func BuildSegmentTable(packetLen int) []byte {
	if packetLen == 0 {
		return []byte{0}
	}

	// Calculate number of segments needed.
	// Each full segment is 255 bytes, plus one partial segment.
	numSegments := packetLen / 255
	remainder := packetLen % 255

	// If packet length is an exact multiple of 255, we need an extra
	// zero-length segment to terminate the packet.
	if remainder == 0 {
		numSegments++
		segments := make([]byte, numSegments)
		for i := 0; i < numSegments-1; i++ {
			segments[i] = 255
		}
		segments[numSegments-1] = 0
		return segments
	}

	// Normal case: full segments plus one partial.
	segments := make([]byte, numSegments+1)
	for i := 0; i < numSegments; i++ {
		segments[i] = 255
	}
	segments[numSegments] = byte(remainder)
	return segments
}

// ParseSegmentTable reconstructs packet lengths from an Ogg lacing table
// (RFC 3533 §6). Each lacing value of 255 means the packet continues into the
// next segment; the first value below 255 terminates a packet whose length is
// the running sum of its segments. The returned slice holds one entry per
// completed packet.
//
// A table that ends on a 255 value describes a packet continued on the next
// page; that incomplete trailing packet is not included in the result. An empty
// table returns nil.
func ParseSegmentTable(segments []byte) []int {
	if len(segments) == 0 {
		return nil
	}

	var lengths []int
	currentLen := 0

	for _, seg := range segments {
		currentLen += int(seg)
		if seg < 255 {
			// End of packet
			lengths = append(lengths, currentLen)
			currentLen = 0
		}
	}

	// If the last segment was 255, the packet continues on the next page.
	// Don't append it as a complete packet here.
	return lengths
}

// IsBOS returns true if this is a Beginning of Stream page.
func (p *Page) IsBOS() bool {
	return p.HeaderType&PageFlagBOS != 0
}

// IsEOS returns true if this is an End of Stream page.
func (p *Page) IsEOS() bool {
	return p.HeaderType&PageFlagEOS != 0
}

// IsContinuation returns true if this page continues a packet from a previous page.
func (p *Page) IsContinuation() bool {
	return p.HeaderType&PageFlagContinuation != 0
}

// PacketLengths extracts packet lengths from the segment table.
// This is equivalent to ParseSegmentTable(p.Segments).
func (p *Page) PacketLengths() []int {
	return ParseSegmentTable(p.Segments)
}

// Packets splits the page payload into individual packet byte slices using the
// lacing lengths from PacketLengths.
//
// The returned slices alias p.Payload; they remain valid only as long as the
// page is not modified. A packet whose final lacing value is 255 continues onto
// the next page and is not returned here. If the payload is shorter than the
// segment table claims, the final packet is truncated to the bytes actually
// present and parsing stops, so a malformed page never produces a slice outside
// p.Payload.
func (p *Page) Packets() [][]byte {
	lengths := p.PacketLengths()
	if len(lengths) == 0 {
		return nil
	}

	packets := make([][]byte, len(lengths))
	offset := 0
	for i, length := range lengths {
		if offset+length > len(p.Payload) {
			// Truncated payload
			packets[i] = p.Payload[offset:]
			break
		}
		packets[i] = p.Payload[offset : offset+length]
		offset += length
	}
	return packets
}

// Encode serializes the page to bytes with proper CRC.
// The output format is:
//   - 27-byte header
//   - Segment table
//   - Payload
//
// The CRC is computed over the entire page (with CRC field zeroed).
func (p *Page) Encode() []byte {
	// Calculate total page size.
	headerSize := pageHeaderSize + len(p.Segments)
	totalSize := headerSize + len(p.Payload)
	data := make([]byte, totalSize)

	// Write header.
	copy(data[0:4], oggMagic)
	data[4] = p.Version
	data[5] = p.HeaderType
	binary.LittleEndian.PutUint64(data[6:14], p.GranulePos)
	binary.LittleEndian.PutUint32(data[14:18], p.SerialNumber)
	binary.LittleEndian.PutUint32(data[18:22], p.PageSequence)
	// CRC at bytes 22-25 will be filled after.
	data[26] = byte(len(p.Segments))

	// Write segment table.
	copy(data[27:], p.Segments)

	// Write payload.
	copy(data[headerSize:], p.Payload)

	// Compute CRC (with CRC field zeroed).
	crc := oggCRC(data)
	binary.LittleEndian.PutUint32(data[22:26], crc)

	return data
}

// ParsePage parses a single Ogg page (RFC 3533 §6) from the front of data.
// It returns the parsed page, the number of bytes consumed (the full page
// length, header plus segment table plus payload), and any error.
//
// data may contain more than one page or trailing bytes; only the first page
// is consumed and callers should advance by the returned count. The returned
// Page owns copies of its segment table and payload, so the input slice may be
// reused or overwritten afterwards.
//
// Errors:
//
//   - ErrInvalidPage if data is shorter than a page header, the "OggS" capture
//     pattern is missing, or the declared segment table or payload extends past
//     the end of data (a truncated or incomplete page).
//   - ErrBadCRC if the page CRC-32 does not match the bytes on the wire,
//     indicating corruption.
//
// A truncated page yields ErrInvalidPage with a zero consumed count; the caller
// can distinguish "need more data" from genuine corruption by buffering more
// input and retrying.
func ParsePage(data []byte) (*Page, int, error) {
	p := &Page{}
	consumed, err := parsePageInto(data, p)
	if err != nil {
		return nil, 0, err
	}
	// ParsePage's contract is that the returned Page owns its data, so copy the
	// zero-copy slices parsePageInto produced into fresh buffers.
	p.Segments = append([]byte(nil), p.Segments...)
	p.Payload = append([]byte(nil), p.Payload...)
	return p, consumed, nil
}

// parsePageInto parses a single Ogg page from the front of data into p WITHOUT
// copying: p.Segments and p.Payload alias data and remain valid only until data
// is overwritten. It returns the bytes consumed; error conditions match
// ParsePage. The Reader uses this to parse into a reused Page over its stable
// read buffer, so steady-state page reads allocate nothing.
func parsePageInto(data []byte, p *Page) (int, error) {
	// Check minimum size for header.
	if len(data) < pageHeaderSize {
		return 0, ErrInvalidPage
	}

	// Verify magic signature.
	if string(data[0:4]) != oggMagic {
		return 0, ErrInvalidPage
	}

	// Parse header fields.
	p.Version = data[4]
	p.HeaderType = data[5]
	p.GranulePos = binary.LittleEndian.Uint64(data[6:14])
	p.SerialNumber = binary.LittleEndian.Uint32(data[14:18])
	p.PageSequence = binary.LittleEndian.Uint32(data[18:22])

	// Get CRC from header.
	storedCRC := binary.LittleEndian.Uint32(data[22:26])

	// Get segment count.
	numSegments := int(data[26])

	// Check we have enough data for segment table, then alias it.
	headerSize := pageHeaderSize + numSegments
	if len(data) < headerSize {
		return 0, ErrInvalidPage
	}
	p.Segments = data[pageHeaderSize : pageHeaderSize+numSegments]

	// Calculate payload size from segment table.
	payloadSize := 0
	for _, seg := range p.Segments {
		payloadSize += int(seg)
	}

	// Check we have enough data for payload, then alias it.
	totalSize := headerSize + payloadSize
	if len(data) < totalSize {
		return 0, ErrInvalidPage
	}
	p.Payload = data[headerSize:totalSize]

	// Verify CRC over the page with the 4-byte CRC field treated as zero,
	// computed in place so no full-page copy is allocated per page.
	var crcZero [4]byte
	computedCRC := oggCRCUpdate(0, data[:22])
	computedCRC = oggCRCUpdate(computedCRC, crcZero[:])
	computedCRC = oggCRCUpdate(computedCRC, data[26:totalSize])
	if computedCRC != storedCRC {
		return 0, ErrBadCRC
	}

	return totalSize, nil
}
