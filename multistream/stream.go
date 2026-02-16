package multistream

import "errors"

// Errors for multistream packet parsing.
var (
	// ErrPacketTooShort indicates insufficient data in the multistream packet.
	ErrPacketTooShort = errors.New("multistream: packet too short")

	// ErrInvalidPacket indicates malformed Opus packet framing.
	ErrInvalidPacket = errors.New("multistream: invalid packet framing")

	// ErrDurationMismatch indicates streams have different frame durations.
	ErrDurationMismatch = errors.New("multistream: streams have different frame durations")

	// ErrInvalidStreamCount indicates an invalid stream count (must be >= 1).
	ErrInvalidStreamCount = errors.New("multistream: invalid stream count (must be >= 1)")
)

// parseSelfDelimitedLength parses a self-delimiting packet length from the data.
// Per RFC 6716 Section 3.2.1, lengths use a 1-2 byte encoding:
//   - If first byte < 252: length = firstByte (1 byte consumed)
//   - Otherwise: length = 4*secondByte + firstByte (2 bytes consumed)
//
// This is the same encoding used for frame lengths in standard Opus packets.
//
// Returns:
//   - length: the decoded packet length in bytes
//   - consumed: number of bytes consumed for the length field (1 or 2)
//   - err: ErrPacketTooShort if insufficient bytes available
func parseSelfDelimitedLength(data []byte) (length, consumed int, err error) {
	if len(data) == 0 {
		return 0, 0, ErrPacketTooShort
	}

	firstByte := int(data[0])
	if firstByte < 252 {
		return firstByte, 1, nil
	}

	// Two-byte encoding
	if len(data) < 2 {
		return 0, 0, ErrPacketTooShort
	}
	secondByte := int(data[1])
	return 4*secondByte + firstByte, 2, nil
}

// parseMultistreamPacket extracts individual stream packets from a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 streams use self-delimited Opus packet framing
//   - Last stream uses standard framing (consumes remaining bytes)
//
// Parameters:
//   - data: raw multistream packet data
//   - numStreams: total number of streams (N)
//
// Returns:
//   - packets: slice of N standard-framed Opus packets, one per stream
//   - err: parsing error if data is malformed
func parseMultistreamPacket(data []byte, numStreams int) ([][]byte, error) {
	if numStreams < 1 {
		return nil, ErrInvalidStreamCount
	}

	packets := make([][]byte, numStreams)
	offset := 0

	// Parse first N-1 packets with self-delimited framing and convert them
	// back to standard framing for the elementary decoders.
	for i := 0; i < numStreams-1; i++ {
		if offset >= len(data) {
			return nil, ErrPacketTooShort
		}

		packet, consumed, err := decodeSelfDelimitedPacket(data[offset:])
		if err != nil {
			return nil, err
		}
		packets[i] = packet
		offset += consumed
	}

	// Last packet uses remaining bytes (standard framing)
	if offset >= len(data) {
		return nil, ErrPacketTooShort
	}
	lastPacket := data[offset:]
	if _, err := parseOpusPacket(lastPacket, false); err != nil {
		return nil, err
	}
	packets[numStreams-1] = lastPacket

	return packets, nil
}

// getFrameDuration returns the frame duration in samples at 48kHz from a packet's TOC byte.
// This is used to verify all streams in a multistream packet have consistent timing.
//
// Parameters:
//   - packet: raw Opus packet data (first byte is TOC)
//
// Returns:
//   - Frame size in samples at 48kHz (120, 240, 480, 960, 1920, or 2880)
//   - Returns 0 if packet is empty
//
// Reference: RFC 6716 Section 3.1
func getFrameDuration(packet []byte) int {
	if len(packet) == 0 {
		return 0
	}

	parsed, err := parseOpusPacket(packet, false)
	if err != nil || len(parsed.frames) == 0 {
		return 0
	}

	// TOC byte structure: config (5 bits) | stereo (1 bit) | code (2 bits)
	config := packet[0] >> 3 // Top 5 bits

	// Frame size table indexed by config (0-31)
	// Matches gopus.configTable from packet.go
	frameSizeTable := [32]int{
		// SILK NB (configs 0-3): 10/20/40/60ms
		480, 960, 1920, 2880,
		// SILK MB (configs 4-7): 10/20/40/60ms
		480, 960, 1920, 2880,
		// SILK WB (configs 8-11): 10/20/40/60ms
		480, 960, 1920, 2880,
		// Hybrid SWB (configs 12-13): 10/20ms
		480, 960,
		// Hybrid FB (configs 14-15): 10/20ms
		480, 960,
		// CELT NB (configs 16-19): 2.5/5/10/20ms
		120, 240, 480, 960,
		// CELT WB (configs 20-23): 2.5/5/10/20ms
		120, 240, 480, 960,
		// CELT SWB (configs 24-27): 2.5/5/10/20ms
		120, 240, 480, 960,
		// CELT FB (configs 28-31): 2.5/5/10/20ms
		120, 240, 480, 960,
	}

	return frameSizeTable[config] * len(parsed.frames)
}

// validateStreamDurations checks that all stream packets have the same frame duration.
// Returns the common duration if valid, or an error if durations don't match.
func validateStreamDurations(packets [][]byte) (int, error) {
	if len(packets) == 0 {
		return 0, ErrInvalidStreamCount
	}

	duration := getFrameDuration(packets[0])
	if duration == 0 {
		return 0, ErrPacketTooShort
	}

	for i := 1; i < len(packets); i++ {
		streamDuration := getFrameDuration(packets[i])
		if streamDuration != duration {
			return 0, ErrDurationMismatch
		}
	}

	return duration, nil
}
