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

// ParseSegmentTable extracts packet lengths from a segment table.
// Returns a slice of packet lengths. A segment value of 255 indicates
// the packet continues; a value less than 255 ends the packet.
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

// Packets extracts individual packets from the payload.
// Uses PacketLengths() to split the payload into packets.
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

// ParsePage parses an Ogg page from bytes.
// Returns the parsed page, number of bytes consumed, and any error.
// Returns ErrInvalidPage if the magic signature is missing or data is truncated.
// Returns ErrBadCRC if the CRC checksum does not match.
func ParsePage(data []byte) (*Page, int, error) {
	// Check minimum size for header.
	if len(data) < pageHeaderSize {
		return nil, 0, ErrInvalidPage
	}

	// Verify magic signature.
	if string(data[0:4]) != oggMagic {
		return nil, 0, ErrInvalidPage
	}

	// Parse header fields.
	p := &Page{
		Version:      data[4],
		HeaderType:   data[5],
		GranulePos:   binary.LittleEndian.Uint64(data[6:14]),
		SerialNumber: binary.LittleEndian.Uint32(data[14:18]),
		PageSequence: binary.LittleEndian.Uint32(data[18:22]),
	}

	// Get CRC from header.
	storedCRC := binary.LittleEndian.Uint32(data[22:26])

	// Get segment count.
	numSegments := int(data[26])

	// Check we have enough data for segment table.
	headerSize := pageHeaderSize + numSegments
	if len(data) < headerSize {
		return nil, 0, ErrInvalidPage
	}

	// Copy segment table.
	p.Segments = make([]byte, numSegments)
	copy(p.Segments, data[27:27+numSegments])

	// Calculate payload size from segment table.
	payloadSize := 0
	for _, seg := range p.Segments {
		payloadSize += int(seg)
	}

	// Check we have enough data for payload.
	totalSize := headerSize + payloadSize
	if len(data) < totalSize {
		return nil, 0, ErrInvalidPage
	}

	// Copy payload.
	p.Payload = make([]byte, payloadSize)
	copy(p.Payload, data[headerSize:totalSize])

	// Verify CRC.
	// Create a copy with CRC field zeroed for verification.
	pageCopy := make([]byte, totalSize)
	copy(pageCopy, data[:totalSize])
	pageCopy[22] = 0
	pageCopy[23] = 0
	pageCopy[24] = 0
	pageCopy[25] = 0

	computedCRC := oggCRC(pageCopy)
	if computedCRC != storedCRC {
		return nil, 0, ErrBadCRC
	}

	return p, totalSize, nil
}
