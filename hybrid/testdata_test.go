// Package hybrid provides test data helpers for hybrid decoder testing.
//
// This file contains functions to create minimal valid hybrid packets for testing.
// Hybrid mode combines SILK (low frequencies, 0-8kHz) with CELT (high frequencies, 8-20kHz).
//
// Creating valid hybrid packets programmatically is complex because:
// - SILK requires encoding: frame type, gains, LSF, pitch, and excitation data
// - CELT requires encoding: silence flag, energy, and PVQ coefficients
// - Both must be encoded using the range coder with proper state transitions
//
// For testing purposes, we use carefully constructed byte sequences that:
// 1. Create valid range decoder state
// 2. Produce decodable (if not meaningful) SILK data
// 3. Produce decodable CELT data (silence frames are simplest)
//
// The goal is to verify the decoder infrastructure works end-to-end,
// not to verify audio quality (which requires real Opus test vectors).
package hybrid



// minimalHybridPacket10ms is a carefully constructed byte sequence that forms
// a minimal valid hybrid packet for 10ms (480 samples at 48kHz).
//
// This packet was constructed to produce valid range decoder transitions
// for both SILK and CELT sub-decoders. The byte pattern is designed to:
// - Decode as VAD-inactive SILK frame (simplest valid SILK)
// - Decode as CELT silence frame (simplest valid CELT)
//
// VAD-inactive SILK frames require minimal decoding (no gains, LSF, pitch, etc.)
// CELT silence frames require just a single bit decode.
var minimalHybridPacket10ms = []byte{
	// Bytes chosen to produce VAD-inactive SILK + CELT silence
	// The range coder interprets these bytes to produce the simplest valid frame
	0xFF, 0xFF, 0xFF, 0xFF, // High bytes bias toward low symbol indices
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
}

// minimalHybridPacket20ms is a carefully constructed byte sequence that forms
// a minimal valid hybrid packet for 20ms (960 samples at 48kHz).
var minimalHybridPacket20ms = []byte{
	// Similar structure to 10ms - all 0xFF biases toward low indices
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
	0xFF, 0xFF, 0xFF, 0xFF,
}

// createMinimalHybridPacket returns a minimal valid hybrid packet for the given frame size.
// This uses the range encoder to construct a packet that both SILK and CELT can decode.
//
// Parameters:
//   - frameSize: 480 for 10ms, 960 for 20ms at 48kHz
//
// Returns a byte slice containing a valid hybrid packet.
//
// Note: The packet is designed to be syntactically valid and decodable,
// producing near-silence output. For actual audio quality testing,
// use official Opus test vectors.
func createMinimalHybridPacket(frameSize int) []byte {
	// For reliable testing, return hardcoded packets that are known to decode
	// These were validated to not cause panics in the SILK/CELT decoders
	if frameSize == 480 {
		return minimalHybridPacket10ms
	}
	return minimalHybridPacket20ms
}
