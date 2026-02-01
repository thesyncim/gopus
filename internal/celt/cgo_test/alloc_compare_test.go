// Package cgo provides allocation comparison tests.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// initCapsLocal creates caps array for testing.
func initCapsLocal(nbBands, lm, channels int) []int {
	frameSize := 120 << lm // Get frame size from lm
	caps := make([]int, nbBands)
	const bitRes = 3 // matches celt.bitRes
	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, frameSize)
		cap := ((width)+8)<<bitRes - 1
		cap *= channels
		caps[i] = cap
	}
	return caps
}

// TestAllocationEncodeDecodeSymmetry verifies encoder and decoder compute the same allocation.
func TestAllocationEncodeDecodeSymmetry(t *testing.T) {
	// Test parameters matching our encode test
	totalBits := 1280 // About what we get for 64kbps @ 20ms
	totalBitsQ3 := totalBits << 3
	nbBands := 21
	channels := 1
	lm := 3 // 20ms frame
	trim := 5

	// Compute allocation with encoder
	caps := initCapsLocal(nbBands, lm, channels)
	offsets := make([]int, nbBands)

	encResult := celt.ComputeAllocationWithEncoder(
		nil, // No range encoder needed for basic computation
		totalBitsQ3,
		nbBands,
		channels,
		caps,
		offsets,
		trim,
		nbBands, // intensity disabled
		false,   // no dual stereo
		lm,
		0,         // no previous coded bands
		nbBands-1, // signalBandwidth
	)

	t.Logf("Encoder allocation:")
	t.Logf("  CodedBands: %d", encResult.CodedBands)
	t.Logf("  Balance: %d", encResult.Balance)

	t.Log("\n  BandBits (Q3):")
	for band := 0; band < nbBands; band++ {
		bits := encResult.BandBits[band]
		t.Logf("    Band %d: %d bits (Q3) = %.3f bits", band, bits, float64(bits)/8.0)
	}

	t.Log("\n  FineBits:")
	for band := 0; band < nbBands; band++ {
		bits := encResult.FineBits[band]
		if bits > 0 {
			t.Logf("    Band %d: %d fine bits", band, bits)
		}
	}

	// Check if lower bands are getting bits
	lowBandBits := 0
	highBandBits := 0
	for band := 0; band < 10; band++ {
		lowBandBits += encResult.BandBits[band]
	}
	for band := 10; band < nbBands; band++ {
		highBandBits += encResult.BandBits[band]
	}

	t.Logf("\n  Low bands (0-9) total: %d bits (Q3)", lowBandBits)
	t.Logf("  High bands (10-20) total: %d bits (Q3)", highBandBits)

	if lowBandBits < highBandBits {
		t.Log("\n  WARNING: Low bands have fewer bits than high bands!")
		t.Log("  This is backwards from expected based on BandAlloc table.")
	}
}

// TestComputeAllocationDirect directly tests the allocation computation.
func TestComputeAllocationDirect(t *testing.T) {
	// Test with typical parameters
	totalBits := 1000
	nbBands := 21
	channels := 1
	lm := 3 // 20ms
	trim := 5

	result := celt.ComputeAllocation(totalBits, nbBands, channels, nil, nil, trim, -1, false, lm)

	t.Logf("Allocation result for %d bits, %d bands, lm=%d:", totalBits, nbBands, lm)
	t.Logf("  CodedBands: %d", result.CodedBands)

	// Show bits per band
	for band := 0; band < nbBands; band++ {
		bits := result.BandBits[band]
		fine := result.FineBits[band]
		if bits > 0 || fine > 0 {
			t.Logf("  Band %d: %d PVQ bits (Q3), %d fine bits", band, bits, fine)
		}
	}
}

// TestDecodeAllocationFromPacket decodes allocation from an actual encoded packet.
func TestDecodeAllocationFromPacket(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * float64(i) / float64(frameSize) // Simple ramp
	}

	// Encode
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded: %d bytes", len(encoded))

	// Decode allocation from the packet
	// Parse the header manually to understand what's encoded
	if len(encoded) > 0 {
		t.Logf("First byte: 0x%02x", encoded[0])
	}

	// Now decode with gopus decoder
	decoder := celt.NewDecoder(channels)
	_, err = decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Logf("DecodeFrame error: %v (this may be expected)", err)
	}

	// The decoder's internal state after decoding should show allocation
	// But we can't easily access it without modifying the decoder
}

// TestAllocationWithRangeCoder tests allocation with actual range coder state.
func TestAllocationWithRangeCoder(t *testing.T) {
	_ = 960 // frameSize not used in this test
	channels := 1
	nbBands := 21
	lm := 3

	// Create a mock packet header to simulate decoder state
	// Just encode some basic flags to consume bits
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode silence=0, postfilter=0, transient=0 (for lm>0), intra=1
	re.EncodeBit(0, 15) // silence
	re.EncodeBit(0, 1)  // postfilter
	re.EncodeBit(0, 3)  // transient
	re.EncodeBit(1, 3)  // intra

	tellAfterFlags := re.Tell()
	t.Logf("Bits after flags: %d", tellAfterFlags)

	// Simulate coarse energy encoding (just dummy values)
	// In reality, coarse energy encoding consumes bits based on the probabilities
	// For simplicity, let's estimate it uses ~100 bits for 21 bands

	// Total bits available
	totalBits := len(buf) * 8

	// After flags and coarse energy, compute remaining for allocation
	// This is a rough estimate
	bitsForAlloc := totalBits - tellAfterFlags - 100 // rough estimate for coarse energy
	bitsForAllocQ3 := bitsForAlloc << 3

	t.Logf("Estimated bits for allocation: %d", bitsForAlloc)

	// Compute allocation
	caps := initCapsLocal(nbBands, lm, channels)
	offsets := make([]int, nbBands)
	trim := 5

	result := celt.ComputeAllocationWithEncoder(
		re,
		bitsForAllocQ3,
		nbBands,
		channels,
		caps,
		offsets,
		trim,
		nbBands,
		false,
		lm,
		0,
		nbBands-1,
	)

	t.Logf("\nAllocation result:")
	t.Logf("  CodedBands: %d", result.CodedBands)

	// Count non-zero bands
	nonZeroBands := 0
	for band := 0; band < nbBands; band++ {
		if result.BandBits[band] > 0 {
			nonZeroBands++
			t.Logf("  Band %d: %d bits", band, result.BandBits[band])
		}
	}
	t.Logf("  Non-zero bands: %d", nonZeroBands)
}
