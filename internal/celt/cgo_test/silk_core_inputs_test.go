// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestSilkDecodeCoreSameInputs tests if gopus and libopus silk_decode_core
// produce the same output when given identical inputs.
// This isolates whether the divergence is in decode_core itself or in state accumulation.
func TestSilkDecodeCoreSameInputs(t *testing.T) {
	// Test with synthetic controlled inputs
	fsKHz := 8
	nbSubfr := 4
	frameLength := 160
	subfrLength := 40
	ltpMemLength := 160
	lpcOrder := 10

	// Create controlled state and control structures
	outBuf := make([]int16, 480) // Max outBuf size
	sLPCQ14Buf := make([]int32, 16)
	gainsQ16 := []int32{1 << 20, 1 << 20, 1 << 20, 1 << 20}
	predCoefQ12 := make([]int16, 32)
	ltpCoefQ14 := make([]int16, 20)
	pitchL := []int{50, 50, 50, 50}
	ltpScaleQ14 := int32(1 << 14) // 1.0
	pulses := make([]int16, frameLength)

	// Initialize predCoefQ12 with simple values
	for i := 0; i < lpcOrder; i++ {
		predCoefQ12[i] = int16(1000 - i*100)
		predCoefQ12[lpcOrder+i] = int16(1000 - i*100)
	}

	// Initialize LTP coefficients
	ltpCoefQ14[2] = 1 << 14 // Center tap = 1.0

	// Unvoiced test (signalType = 1)
	signalType := int8(1)
	quantOffsetType := int8(0)
	nlsfInterpCoefQ2 := int8(4)
	seed := int8(0)

	// Call libopus decode_core
	libOut := SilkDecodeCore(
		fsKHz, nbSubfr, frameLength, subfrLength, ltpMemLength, lpcOrder,
		gainsQ16[0], 0, 0,
		signalType, quantOffsetType, nlsfInterpCoefQ2, seed,
		outBuf, sLPCQ14Buf,
		gainsQ16, predCoefQ12, ltpCoefQ14, pitchL, ltpScaleQ14,
		pulses,
	)

	// Now test gopus with the same inputs
	// Note: gopus decode_core is internal, so we test via full packet decode
	t.Logf("libopus decode_core output (first 10 samples): %v", libOut[:10])

	// For a true comparison, we need to call gopus decode_core with the same state
	// But silk.silkDecodeCore is not exported. Let's verify by decoding a packet.

	// Load actual packet for comparison
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// Decode packet 4 frame 2 with both implementations
	pkt4 := packets[4]
	toc := gopus.ParseTOC(pkt4[0])
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	delay := 5

	// Fresh decoders
	goDecFresh := silk.NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(pkt4[1:])
	goOutput, err := goDecFresh.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	libDecFresh, _ := NewLibopusDecoder(config.SampleRate, 1)
	if libDecFresh == nil {
		t.Fatal("Could not create libopus decoder")
	}
	libPcm, _ := libDecFresh.DecodeFloat(pkt4, 960)
	libDecFresh.Destroy()

	// Compare Frame 2 outputs
	t.Log("\nFrame 2 comparison (samples 320-329):")
	for i := 320; i < 330; i++ {
		goVal := int(goOutput[i] * 32768)
		libVal := int(libPcm[i+delay] * 32768)
		diff := goVal - libVal
		t.Logf("  [%d] go=%d, lib=%d, diff=%d", i, goVal, libVal, diff)
	}

	// The key question: is the difference in Frame 2 caused by:
	// 1. Different inputs (state from Frame 0 and 1)?
	// 2. Different decode_core algorithm?

	// Let's check Frame 0 and Frame 1 first
	t.Log("\nFrame 0 comparison (samples 0-9):")
	for i := 0; i < 10; i++ {
		goVal := int(goOutput[i] * 32768)
		libVal := int(libPcm[i+delay] * 32768)
		diff := goVal - libVal
		if diff != 0 {
			t.Logf("  [%d] go=%d, lib=%d, diff=%d <-- DIFF", i, goVal, libVal, diff)
		}
	}

	t.Log("\nFrame 1 comparison (samples 160-169):")
	for i := 160; i < 170; i++ {
		goVal := int(goOutput[i] * 32768)
		libVal := int(libPcm[i+delay] * 32768)
		diff := goVal - libVal
		if diff != 0 {
			t.Logf("  [%d] go=%d, lib=%d, diff=%d <-- DIFF", i, goVal, libVal, diff)
		}
	}

	// Count exact matches per frame
	for frame := 0; frame < 3; frame++ {
		start := frame * 160
		end := start + 160
		exact := 0
		for i := start; i < end && i < len(goOutput); i++ {
			goVal := int(goOutput[i] * 32768)
			libVal := int(libPcm[i+delay] * 32768)
			if goVal == libVal {
				exact++
			}
		}
		t.Logf("\nFrame %d: %d/160 exact matches", frame, exact)
	}
}
