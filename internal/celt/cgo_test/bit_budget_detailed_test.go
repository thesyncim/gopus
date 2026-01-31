// Package cgo provides detailed bit budget tracing tests.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestBitBudgetDetailedTrace creates a detailed trace of encoding stages.
func TestBitBudgetDetailedTrace(t *testing.T) {
	// Test with CBR for deterministic comparison
	bitrate := 64000
	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		ti := float64(i) / 48000.0
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}
	samples32 := make([]float32, frameSize)
	for i, v := range samples {
		samples32[i] = float32(v)
	}

	// === Encode with gopus (CBR) ===
	t.Log("=== Gopus CBR Encoding ===")
	gopusEnc := celt.NewEncoder(channels)
	gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetVBR(false)

	gopusPacket, err := gopusEnc.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// === Encode with libopus (CBR) ===
	t.Log("=== Libopus CBR Encoding ===")
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false) // CBR mode
	libEnc.SetSignal(OpusSignalMusic)

	libPacket, libLen := libEnc.EncodeFloat(samples32, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}
	libPayload := libPacket[1:] // skip TOC

	t.Logf("Packet sizes: gopus=%d, libopus=%d", len(gopusPacket), len(libPayload))
	t.Logf("Final range: gopus=0x%08x, libopus=0x%08x", gopusEnc.FinalRange(), libEnc.GetFinalRange())

	// Find and analyze divergence
	t.Log("\n=== Byte-by-byte Comparison ===")
	maxLen := len(gopusPacket)
	if len(libPayload) < maxLen {
		maxLen = len(libPayload)
	}

	divergeAt := -1
	for i := 0; i < maxLen; i++ {
		if gopusPacket[i] != libPayload[i] {
			divergeAt = i
			break
		}
	}

	if divergeAt >= 0 {
		t.Logf("First divergence at byte %d", divergeAt)
		// Show bytes around divergence
		start := divergeAt - 5
		if start < 0 {
			start = 0
		}
		end := divergeAt + 5
		if end > maxLen {
			end = maxLen
		}
		t.Logf("Bytes %d-%d:", start, end-1)
		for i := start; i < end; i++ {
			var gopusByte, libByte byte
			if i < len(gopusPacket) {
				gopusByte = gopusPacket[i]
			}
			if i < len(libPayload) {
				libByte = libPayload[i]
			}
			marker := ""
			if gopusByte != libByte {
				marker = " <-- DIVERGENCE"
			}
			t.Logf("  [%3d] gopus=0x%02x, libopus=0x%02x%s", i, gopusByte, libByte, marker)
		}
	} else {
		t.Log("Packets are identical!")
	}

	// Binary analysis of the divergence byte
	if divergeAt >= 0 && divergeAt < len(gopusPacket) && divergeAt < len(libPayload) {
		t.Log("\n=== Binary Analysis of Divergent Byte ===")
		gopusByte := gopusPacket[divergeAt]
		libByte := libPayload[divergeAt]
		t.Logf("Byte %d:", divergeAt)
		t.Logf("  gopus:   0x%02x = 0b%08b", gopusByte, gopusByte)
		t.Logf("  libopus: 0x%02x = 0b%08b", libByte, libByte)
		xorDiff := gopusByte ^ libByte
		t.Logf("  XOR diff: 0x%02x = 0b%08b", xorDiff, xorDiff)

		// Count leading identical bits
		identicalBits := 0
		for bit := 7; bit >= 0; bit-- {
			if (gopusByte & (1 << bit)) == (libByte & (1 << bit)) {
				identicalBits++
			} else {
				break
			}
		}
		t.Logf("  First differing bit: bit %d (0-indexed from LSB)", 7-identicalBits)
	}

	// Decode both with libopus to check quality
	t.Log("\n=== Decode Test ===")
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	// Decode gopus packet
	toc := byte((31 << 3) | 0) // CELT 20ms
	gopusWithTOC := append([]byte{toc}, gopusPacket...)
	gopusDecoded, gopusSamples := libDec.DecodeFloat(gopusWithTOC, frameSize)
	if gopusSamples < 0 {
		t.Logf("gopus decode failed: %d", gopusSamples)
	} else {
		t.Logf("gopus decoded %d samples", gopusSamples)
	}

	// Create new decoder for libopus packet (reset state)
	libDec2, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder2 failed: %v", err)
	}
	defer libDec2.Destroy()

	libDecoded, libSamples := libDec2.DecodeFloat(libPacket, frameSize)
	if libSamples < 0 {
		t.Logf("libopus decode failed: %d", libSamples)
	} else {
		t.Logf("libopus decoded %d samples", libSamples)
	}

	// Calculate SNR for both
	if gopusSamples > 0 && libSamples > 0 {
		delay := 120 // typical CELT delay
		var signalPower, gopusNoise, libNoise float64
		for i := delay; i < frameSize-delay; i++ {
			ref := float64(samples32[i])
			signalPower += ref * ref
			gopusNoise += math.Pow(ref-float64(gopusDecoded[i]), 2)
			libNoise += math.Pow(ref-float64(libDecoded[i]), 2)
		}
		gopusSNR := 10 * math.Log10(signalPower/(gopusNoise+1e-10))
		libSNR := 10 * math.Log10(signalPower/(libNoise+1e-10))
		t.Logf("\nSNR comparison:")
		t.Logf("  gopus-encoded:   %.2f dB", gopusSNR)
		t.Logf("  libopus-encoded: %.2f dB", libSNR)
	}
}

// TestBitBudgetAllocationInputs traces the inputs to bit allocation.
func TestBitBudgetAllocationInputs(t *testing.T) {
	// The key question: What are the exact inputs to clt_compute_allocation?
	//
	// libopus (line 2626):
	// codedBands = clt_compute_allocation(mode, start, end, offsets, cap,
	//    alloc_trim, &st->intensity, &dual_stereo, bits, &balance, pulses,
	//    fine_quant, fine_priority, C, LM, enc, 1, st->lastCodedBands, signalBandwidth);
	//
	// Key parameters:
	// - bits = (nbCompressedBytes*8 << BITRES) - ec_tell_frac(enc) - 1 - anti_collapse_rsv
	// - offsets[] from dynalloc
	// - cap[] from init_caps
	// - alloc_trim from analysis
	// - intensity from stereo analysis
	// - LM from frame size

	frameSize := 960
	channels := 1
	lm := 3
	nbBands := 21
	bitrate := 64000

	t.Log("=== Allocation Input Analysis ===")

	// Compute CBR bytes
	cbrBytes := cbrPayloadBytesLocal(bitrate, frameSize, channels)
	totalBits := cbrBytes * 8
	t.Logf("Total bits: %d", totalBits)

	// Estimate bits used before allocation (flags + coarse energy + TF + spread + dynalloc + trim)
	// Based on typical encoding:
	// - Flags: ~4 bits
	// - Coarse energy: ~70-100 bits (Laplace coded, 21 bands)
	// - TF: ~20-30 bits
	// - Spread: ~2 bits
	// - Dynalloc: ~5-20 bits
	// - Trim: ~3 bits
	// Total overhead: ~100-160 bits
	overheadEstimate := 130 // bits
	t.Logf("Estimated overhead before allocation: ~%d bits", overheadEstimate)

	// Anti-collapse reserve (for transient LM>=2)
	antiCollapseRsv := 1 << 3 // 8 in Q3
	t.Logf("Anti-collapse reserve: %d (Q3)", antiCollapseRsv)

	// Bits available for allocation (in Q3)
	bitsForAllocQ3 := (totalBits << 3) - (overheadEstimate << 3) - antiCollapseRsv
	t.Logf("Bits for allocation (Q3): %d", bitsForAllocQ3)
	t.Logf("Bits for allocation (Q0): %d", bitsForAllocQ3>>3)

	// Initialize caps
	caps := initCapsLocal(nbBands, lm, channels)
	t.Log("\nCaps (Q3):")
	for i := 0; i < 10; i++ { // Show first 10 bands
		t.Logf("  Band %d: cap=%d", i, caps[i])
	}

	// Default offsets (no dynalloc boost)
	offsets := make([]int, nbBands)

	// Default trim
	trim := 5

	// Compute allocation
	result := celt.ComputeAllocationWithEncoder(
		nil,
		bitsForAllocQ3>>3, // bits in Q0
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
	t.Logf("  Balance: %d (Q3)", result.Balance)
	t.Logf("  Intensity: %d", result.Intensity)
	t.Logf("  Dual stereo: %v", result.DualStereo)

	// Sum allocated bits
	totalPVQ := 0
	totalFine := 0
	for i := 0; i < nbBands; i++ {
		totalPVQ += result.BandBits[i]
		totalFine += result.FineBits[i]
	}
	t.Logf("  Total PVQ bits (Q3): %d", totalPVQ)
	t.Logf("  Total fine bits: %d", totalFine)
	t.Logf("  Total allocated: %d bits", (totalPVQ>>3)+totalFine)
}
