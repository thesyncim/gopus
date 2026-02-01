//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides bit budget comparison tests between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestBitBudgetCalculations compares bit budget calculations between gopus and libopus.
// This traces:
// - Total bits available
// - Bits used for each component (header, coarse energy, TF, spread, dynalloc, fine energy, PVQ)
// - Remaining bits after each step
func TestBitBudgetCalculations(t *testing.T) {
	// Test parameters
	bitrate := 64000
	frameSize := 960 // 20ms at 48kHz
	channels := 1
	lm := 3 // LM=3 for 20ms

	// === Compute gopus bit budget ===
	t.Log("=== Gopus Bit Budget Computation ===")

	// Method 1: CBR calculation (simpler)
	enc := celt.NewEncoder(channels)
	enc.SetBitrate(bitrate)
	enc.SetVBR(false) // Use CBR for deterministic budget

	// Get CBR payload bytes
	cbrBytes := cbrPayloadBytesLocal(bitrate, frameSize, channels)
	t.Logf("CBR payload bytes: %d", cbrBytes)
	t.Logf("CBR total bits: %d", cbrBytes*8)

	// Method 2: VBR calculation (for reference)
	baseBits := bitrateToBitsLocal(bitrate, frameSize)
	t.Logf("VBR base bits: %d", baseBits)

	// Set back to CBR for actual encoding
	enc.SetVBR(false)

	// Now trace the actual encoding to see bit usage
	t.Log("\n=== Tracing Actual Encoding ===")

	// Generate test signal (440Hz sine)
	samples := make([]float64, frameSize)
	for i := range samples {
		ti := float64(i) / 48000.0
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Create a test encoder that traces bit usage
	targetBits := cbrBytes * 8 // Use CBR for predictable budget
	t.Logf("\nTarget bits for encoding: %d", targetBits)

	// Initialize range encoder with the expected buffer size
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Trace bit usage step by step
	tell0 := re.Tell()
	t.Logf("After init: tell=%d", tell0)

	// Simulate encoding header flags (silence, postfilter, transient, intra)
	// For non-silent audio with VBR-like encoding:
	re.EncodeBit(0, 15) // silence=0
	tell1 := re.Tell()
	t.Logf("After silence flag: tell=%d, used=%d", tell1, tell1-tell0)

	re.EncodeBit(0, 1) // postfilter=0
	tell2 := re.Tell()
	t.Logf("After postfilter flag: tell=%d, used=%d", tell2, tell1)

	if lm > 0 {
		re.EncodeBit(1, 3) // transient=1 (for first frame)
		tell3 := re.Tell()
		t.Logf("After transient flag: tell=%d, used=%d", tell3, tell2)
	}

	re.EncodeBit(1, 3) // intra=1 (first frame)
	tell4 := re.Tell()
	t.Logf("After intra flag: tell=%d", tell4)

	// Compute bits remaining for allocation (libopus formula)
	// bits = (nbCompressedBytes*8 << BITRES) - ec_tell_frac(enc) - 1
	bitRes := 3
	bitsUsedFrac := re.TellFrac()
	totalBitsQ3 := (targetBits << bitRes) - bitsUsedFrac - 1

	t.Logf("\nBit budget calculation (libopus style):")
	t.Logf("  targetBits = %d", targetBits)
	t.Logf("  bitsUsedFrac (after flags) = %d (Q3)", bitsUsedFrac)
	t.Logf("  totalBitsQ3 for allocation = %d", totalBitsQ3)
	t.Logf("  totalBits for allocation (Q0) = %d", totalBitsQ3>>bitRes)

	// Compare with libopus encoding
	t.Log("\n=== Comparing with libopus ===")

	// Create libopus encoder
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false) // Use CBR to match
	libEnc.SetSignal(OpusSignalMusic)

	// Convert samples to float32
	samples32 := make([]float32, frameSize)
	for i, v := range samples {
		samples32[i] = float32(v)
	}

	// Encode with libopus
	libPacket, libLen := libEnc.EncodeFloat(samples32, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	t.Logf("libopus encoded packet: %d bytes (including TOC)", len(libPacket))
	t.Logf("libopus payload: %d bytes (excluding TOC)", len(libPacket)-1)

	// Get libopus final range
	libRange := libEnc.GetFinalRange()
	t.Logf("libopus final range: 0x%08x", libRange)

	// Encode with gopus
	gopusPacket, err := enc.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("gopus encoded packet: %d bytes", len(gopusPacket))

	// Get gopus final range
	gopusRange := enc.FinalRange()
	t.Logf("gopus final range: 0x%08x", gopusRange)

	// Compare packet sizes
	t.Log("\n=== Comparison Summary ===")
	t.Logf("Packet size: gopus=%d bytes, libopus=%d bytes (payload only)", len(gopusPacket), len(libPacket)-1)
	t.Logf("Final range: gopus=0x%08x, libopus=0x%08x, match=%v", gopusRange, libRange, gopusRange == libRange)

	// Compare first bytes
	maxBytes := 20
	if len(gopusPacket) < maxBytes {
		maxBytes = len(gopusPacket)
	}
	libPayload := libPacket[1:] // skip TOC
	if len(libPayload) < maxBytes {
		maxBytes = len(libPayload)
	}

	t.Log("\nFirst bytes comparison:")
	t.Logf("gopus:   %02x", gopusPacket[:maxBytes])
	t.Logf("libopus: %02x", libPayload[:maxBytes])

	// Find divergence point
	divergeAt := -1
	for i := 0; i < len(gopusPacket) && i < len(libPayload); i++ {
		if gopusPacket[i] != libPayload[i] {
			divergeAt = i
			break
		}
	}
	if divergeAt >= 0 {
		t.Logf("\nDivergence at byte %d: gopus=0x%02x, libopus=0x%02x", divergeAt, gopusPacket[divergeAt], libPayload[divergeAt])
	} else if len(gopusPacket) != len(libPayload) {
		t.Logf("\nPackets have different lengths but identical prefix")
	} else {
		t.Log("\nPackets are identical!")
	}
}

// TestBitBudgetComponentBreakdown compares bit usage per encoding component.
func TestBitBudgetComponentBreakdown(t *testing.T) {
	// This test requires tracing internal state during encoding
	// We'll estimate bit usage based on the encoding order

	frameSize := 960
	channels := 1
	bitrate := 64000
	lm := 3
	nbBands := 21

	// Estimated bit usage for each component
	t.Log("=== Estimated Bit Usage per Component ===")
	t.Log("(Based on typical 64kbps, 20ms, mono CELT encoding)")

	// Total available bits
	totalBits := bitrateToBitsLocal(bitrate, frameSize)
	t.Logf("Total available bits: %d", totalBits)

	// Header flags (silence, postfilter, transient, intra)
	// silence: ~1 bit (logp=15), postfilter: ~1 bit, transient: ~1 bit (logp=3), intra: ~1 bit (logp=3)
	headerBits := 4 // approximate
	t.Logf("Header flags: ~%d bits", headerBits)

	// Coarse energy: ~70-100 bits for 21 bands (Laplace coding)
	coarseEnergyBits := 80 // approximate
	t.Logf("Coarse energy: ~%d bits", coarseEnergyBits)

	// TF encoding: ~20-30 bits
	tfBits := 25
	t.Logf("TF encoding: ~%d bits", tfBits)

	// Spread decision: ~2 bits (ICDF)
	spreadBits := 2
	t.Logf("Spread decision: ~%d bits", spreadBits)

	// Dynalloc: variable, often 0-20 bits
	dynallocBits := 10
	t.Logf("Dynalloc: ~%d bits", dynallocBits)

	// Alloc trim: ~3 bits (ICDF with 11 values)
	trimBits := 3
	t.Logf("Alloc trim: ~%d bits", trimBits)

	// Fine energy: variable based on allocation
	fineEnergyBits := 50 // approximate
	t.Logf("Fine energy: ~%d bits", fineEnergyBits)

	// Overhead subtotal
	overheadBits := headerBits + coarseEnergyBits + tfBits + spreadBits + dynallocBits + trimBits + fineEnergyBits
	t.Logf("\nOverhead subtotal: ~%d bits", overheadBits)

	// PVQ bands: remaining bits
	pvqBits := totalBits - overheadBits
	t.Logf("PVQ bands: ~%d bits (remaining)", pvqBits)

	// Now test actual allocation
	t.Log("\n=== Testing Actual Allocation Computation ===")

	// Available bits for allocation (after flags, coarse energy, TF, spread, dynalloc, trim)
	availBitsForAlloc := totalBits - overheadBits + fineEnergyBits // fine energy is part of allocation
	t.Logf("Available bits for allocation: ~%d", availBitsForAlloc)
	availBitsForAllocQ3 := availBitsForAlloc << 3

	// Compute allocation
	caps := initCapsLocal(nbBands, lm, channels)
	offsets := make([]int, nbBands)
	trim := 5 // default

	result := celt.ComputeAllocationWithEncoder(
		nil, // no encoder needed
		availBitsForAllocQ3,
		nbBands,
		channels,
		caps,
		offsets,
		trim,
		nbBands, // intensity disabled
		false,   // no dual stereo
		lm,
		0,         // no previous coded bands
		nbBands-1, // signal bandwidth
	)

	t.Logf("\nAllocation result:")
	t.Logf("  Coded bands: %d", result.CodedBands)
	t.Logf("  Balance: %d", result.Balance)

	// Sum up allocated bits
	totalPVQBits := 0
	totalFineBits := 0
	for i := 0; i < nbBands; i++ {
		totalPVQBits += result.BandBits[i]
		totalFineBits += result.FineBits[i]
	}
	t.Logf("  Total PVQ bits (Q3): %d -> %d bits", totalPVQBits, totalPVQBits/8)
	t.Logf("  Total fine bits: %d", totalFineBits)

	// Show per-band allocation
	t.Log("\nPer-band allocation:")
	for i := 0; i < nbBands; i++ {
		if result.BandBits[i] > 0 || result.FineBits[i] > 0 {
			t.Logf("  Band %d: PVQ=%d bits (Q3), fine=%d bits", i, result.BandBits[i], result.FineBits[i])
		}
	}
}

// TestEffectiveBytesCalculation compares effectiveBytes calculation.
func TestEffectiveBytesCalculation(t *testing.T) {
	// libopus formula:
	// if VBR:
	//   vbr_rate = bitrate_to_bits(bitrate, Fs, frame_size) << BITRES
	//   effectiveBytes = vbr_rate >> (3+BITRES)
	// else (CBR):
	//   effectiveBytes = nbCompressedBytes - nbFilledBytes

	testCases := []struct {
		bitrate   int
		frameSize int
	}{
		{64000, 960},  // 64kbps, 20ms
		{32000, 480},  // 32kbps, 10ms
		{128000, 960}, // 128kbps, 20ms
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			t.Logf("Bitrate: %d bps, Frame size: %d samples", tc.bitrate, tc.frameSize)

			// VBR calculation
			baseBits := bitrateToBitsLocal(tc.bitrate, tc.frameSize)
			t.Logf("  VBR base bits: %d", baseBits)
			t.Logf("  VBR effective bytes: %d", baseBits/8)

			// CBR calculation
			cbrBytes := cbrPayloadBytesLocal(tc.bitrate, tc.frameSize, 1)
			t.Logf("  CBR payload bytes: %d", cbrBytes)

			// Check if they're close
			diff := abs(baseBits/8 - cbrBytes)
			t.Logf("  Difference: %d bytes", diff)
		})
	}
}

// Helper functions (local copies to avoid import cycles)

func bitrateToBitsLocal(bitrate, frameSize int) int {
	if bitrate <= 0 {
		bitrate = 64000
	}
	if bitrate < 6000 {
		bitrate = 6000
	}
	if bitrate > 510000 {
		bitrate = 510000
	}
	return bitrate * frameSize / 48000
}

func cbrPayloadBytesLocal(bitrate, frameSize, channels int) int {
	const fs = 48000
	if bitrate <= 0 {
		if channels == 2 {
			bitrate = 128000
		} else {
			bitrate = 64000
		}
	}
	if bitrate < 6000 {
		bitrate = 6000
	}
	if bitrate > 510000 {
		bitrate = 510000
	}
	nbCompressed := (bitrate*frameSize + 4*fs) / (8 * fs)
	if nbCompressed < 2 {
		nbCompressed = 2
	}
	if nbCompressed > 1275 {
		nbCompressed = 1275
	}
	payload := nbCompressed - 1 // subtract TOC byte
	if payload < 0 {
		payload = 0
	}
	return payload
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
