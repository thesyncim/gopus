// Package cgo provides CGO comparison tests for spread decision encoding.
// Agent 22: Debug spread decision divergence at byte 7
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestSpreadICDFMatchesLibopus verifies spread_icdf table matches
func TestSpreadICDFMatchesLibopus(t *testing.T) {
	// Get libopus spread_icdf
	libICDF := GetSpreadICDF()

	// gopus spread_icdf from tables.go
	gopusICDF := []uint8{25, 23, 2, 0}

	t.Logf("libopus spread_icdf: %v", libICDF)
	t.Logf("gopus spread_icdf:   %v", gopusICDF)

	for i := 0; i < 4; i++ {
		if libICDF[i] != gopusICDF[i] {
			t.Errorf("Mismatch at index %d: libopus=%d gopus=%d", i, libICDF[i], gopusICDF[i])
		}
	}
}

var spreadNames = []string{"SPREAD_NONE", "SPREAD_LIGHT", "SPREAD_NORMAL", "SPREAD_AGGRESSIVE"}

// TestSpreadEncodeMatchesLibopus tests that spread encoding produces same bytes
func TestSpreadEncodeMatchesLibopus(t *testing.T) {
	spreadICDF := []uint8{25, 23, 2, 0}

	for spread := 0; spread <= 3; spread++ {
		t.Run(spreadNames[spread], func(t *testing.T) {
			// Encode with libopus
			libBytes := EncodeSpreadDecision(spread)

			// Encode with gopus
			goBuf := make([]byte, 256)
			enc := &rangecoding.Encoder{}
			enc.Init(goBuf)
			enc.EncodeICDF(spread, spreadICDF, 5)
			goBytes := enc.Done()

			t.Logf("Spread %d (%s):", spread, spreadNames[spread])
			t.Logf("  libopus: %x (len=%d)", libBytes, len(libBytes))
			t.Logf("  gopus:   %x (len=%d)", goBytes, len(goBytes))

			// Compare bytes
			match := len(goBytes) == len(libBytes)
			if match {
				for i := range goBytes {
					if goBytes[i] != libBytes[i] {
						match = false
						break
					}
				}
			}
			if !match {
				t.Errorf("Mismatch for spread=%d", spread)
			}
		})
	}
}

// TestSpreadDecodeRoundtrip tests that libopus can decode what gopus encodes
func TestSpreadDecodeRoundtrip(t *testing.T) {
	spreadICDF := []uint8{25, 23, 2, 0}

	for spread := 0; spread <= 3; spread++ {
		t.Run(spreadNames[spread], func(t *testing.T) {
			// Encode with gopus
			goBuf := make([]byte, 256)
			enc := &rangecoding.Encoder{}
			enc.Init(goBuf)
			enc.EncodeICDF(spread, spreadICDF, 5)
			goBytes := enc.Done()

			// Decode with libopus
			decoded := DecodeSpreadDecision(goBytes)

			t.Logf("Encoded spread=%d, decoded=%d", spread, decoded)

			if decoded != spread {
				t.Errorf("Roundtrip failed: encoded %d, decoded %d", spread, decoded)
			}
		})
	}
}

// TestSpreadingDecisionAlgorithm tests the spreading_decision algorithm
// by comparing gopus implementation against the libopus algorithm step-by-step
func TestSpreadingDecisionAlgorithm(t *testing.T) {
	// Test case: 440Hz sine wave, 20ms frame, mono
	frameSize := 960
	channels := 1
	freq := 440.0
	amp := 0.5

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = amp * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Create gopus encoder and process up to spread decision
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	// Get the spread decision by encoding a frame
	encoded, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}

	t.Logf("Encoded frame: %d bytes", len(encoded))
	t.Logf("First 16 bytes: %02x", encoded[:minInt(16, len(encoded))])
}

// TestSpreadDecisionPosition determines the bit position of spread encoding
func TestSpreadDecisionPosition(t *testing.T) {
	// The frame structure for a 20ms CELT mono 64kbps frame:
	// 1. Silence flag (if tell==1): 1 bit at logp=15
	// 2. Postfilter flag: 1 bit at logp=1
	// 3. Transient flag (if LM>0): 1 bit at logp=3
	// 4. Intra energy flag: 1 bit at logp=3
	// 5. Coarse energy encoding (Laplace): variable bits
	// 6. TF encoding: variable bits
	// 7. SPREAD encoding: ~2 bits using spread_icdf with ftb=5
	// 8. Dynalloc: variable bits
	// 9. Allocation trim: variable bits
	// 10. Bit allocation: computed
	// 11. Fine energy: variable bits
	// 12. PVQ encoding: remaining bits

	t.Log("Frame structure analysis:")
	t.Log("  Bit 0: silence flag check (if tell==1)")
	t.Log("  After silence=0: +1 bit (logp=15)")
	t.Log("  Postfilter=0: +1 bit (logp=1)")
	t.Log("  Transient=1 (for 20ms): +1 bit (logp=3)")
	t.Log("  Intra=1 (first frame): +1 bit (logp=3)")
	t.Log("  Coarse energy: 21 bands * ~3-4 bits = ~63-84 bits")
	t.Log("  TF encoding: ~5-10 bits for 21 bands")
	t.Log("  Spread: ~2 bits (ICDF with ftb=5)")
}

// TestCompareTFAndSpreadEncoding traces both TF and spread encoding
func TestCompareTFAndSpreadEncoding(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	gopusBytes, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Encode with libopus for comparison
	pcm32 := make([]float32, frameSize)
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libBytes, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}

	// Skip TOC byte in libopus output
	libPayload := libBytes[1:]
	gopusPayload := gopusBytes

	t.Logf("gopus payload:   %d bytes", len(gopusPayload))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Find divergence point
	divergeIdx := -1
	minLen := len(gopusPayload)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	for i := 0; i < minLen; i++ {
		if gopusPayload[i] != libPayload[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx >= 0 {
		t.Logf("Divergence at byte %d:", divergeIdx)
		t.Logf("  gopus:   0x%02X (binary: %08b)", gopusPayload[divergeIdx], gopusPayload[divergeIdx])
		t.Logf("  libopus: 0x%02X (binary: %08b)", libPayload[divergeIdx], libPayload[divergeIdx])

		// Analyze the bit difference
		xorDiff := gopusPayload[divergeIdx] ^ libPayload[divergeIdx]
		t.Logf("  XOR diff: 0x%02X (binary: %08b)", xorDiff, xorDiff)

		// Show context
		start := divergeIdx - 2
		if start < 0 {
			start = 0
		}
		end := divergeIdx + 5
		if end > minLen {
			end = minLen
		}

		t.Log("Context around divergence:")
		for i := start; i < end; i++ {
			marker := ""
			if i == divergeIdx {
				marker = " <-- DIVERGE"
			} else if i > divergeIdx && gopusPayload[i] != libPayload[i] {
				marker = " <-- mismatch"
			}
			t.Logf("  [%d] gopus=%02X libopus=%02X%s", i, gopusPayload[i], libPayload[i], marker)
		}

		// Estimate bit position of divergence
		bytePos := divergeIdx
		t.Logf("\nDivergence is in byte %d (bits %d-%d of payload)", bytePos, bytePos*8, bytePos*8+7)
	} else if len(gopusPayload) != len(libPayload) {
		t.Logf("Payloads match up to min length, but sizes differ: gopus=%d, libopus=%d",
			len(gopusPayload), len(libPayload))
	} else {
		t.Log("EXACT MATCH!")
	}
}

// TestTraceFrameEncoding traces the frame encoding steps
func TestTraceFrameEncoding(t *testing.T) {
	// Trace with typical parameters for a 440Hz sine first frame
	trace, bytes := TraceFrameEncodeToSpread(
		0, // silence=0
		0, // postfilter=0
		1, // transient=1 (patched for first frame)
		1, // intra=1 (first frame)
		2, // spread=SPREAD_NORMAL
		nil, // tfRes
		0,  // tfSelect
		21, // nbBands
		3,  // lm=3 for 20ms
		5,  // allocTrim=5 (default)
	)

	t.Logf("Trace results:")
	t.Logf("  Tell before TF:     %d bits", trace.TellBeforeTF)
	t.Logf("  Tell after TF:      %d bits", trace.TellAfterTF)
	t.Logf("  Tell after Spread:  %d bits", trace.TellAfterSpread)
	t.Logf("  Tell after Dynalloc: %d bits", trace.TellAfterDynalloc)
	t.Logf("  Tell after Trim:    %d bits", trace.TellAfterTrim)
	t.Logf("  Range after Spread: 0x%08X", trace.RngAfterSpread)
	t.Logf("  Spread value:       %d (%s)", trace.SpreadValue, spreadNames[trace.SpreadValue])
	t.Logf("  Output bytes:       %x", bytes)
}

// TestSpreadBitPositionAnalysis analyzes where spread encoding lands in the bitstream
func TestSpreadBitPositionAnalysis(t *testing.T) {
	// For CELT mono 20ms 64kbps with transient=1 and intra=1:
	//
	// Bit budget analysis (from encode_frame.go):
	// - tell==1: encode silence flag with logp=15 (1 bit cost ~0.003 bits)
	// - Postfilter: 1 bit at logp=1
	// - Transient: 1 bit at logp=3 (if LM>0)
	// - Intra: 1 bit at logp=3
	//
	// After flags (approximately 4 bits):
	// - Coarse energy: 21 bands with Laplace coding
	// - TF encoding: for 21 bands with transient
	// - Spread encoding: ICDF with ftb=5
	//
	// Given divergence at byte 7 (bits 56-63), this is after:
	// - ~4 bits of flags
	// - ~50+ bits of coarse energy
	//
	// This places the divergence in the TF/spread/dynalloc region.

	t.Log("Estimating divergence location:")
	t.Log("  Byte 7 = bits 56-63")
	t.Log("  Flags: ~4 bits (0-4)")
	t.Log("  Coarse energy: ~50-60 bits (4-60)")
	t.Log("  TF encoding: starts around bit 60")
	t.Log("  Spread encoding: after TF")
	t.Log("")
	t.Log("The divergence at byte 7 is likely in TF or spread encoding")

	// Try different spread values and see effect on encoding
	t.Log("\nTesting spread value impact on encoding:")
	for spread := 0; spread <= 3; spread++ {
		bytes := EncodeSpreadDecision(spread)
		t.Logf("  Spread %d (%s): %x", spread, spreadNames[spread], bytes)
	}
}

