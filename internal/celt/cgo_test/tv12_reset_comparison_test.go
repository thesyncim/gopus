// Package cgo compares resampler reset behavior vs no-reset to match libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerResetComparison tests if NOT resetting matches libopus better.
func TestTV12ResamplerResetComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create libopus decoder as reference
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process all packets through libopus to get reference output for packet 826
	for i := 0; i <= 825; i++ {
		libDec.DecodeFloat(packets[i], 1920)
	}
	libOut826, libN := libDec.DecodeFloat(packets[826], 1920)
	t.Logf("Libopus packet 826: first 5 = [%.6f, %.6f, %.6f, %.6f, %.6f]",
		libOut826[0], libOut826[1], libOut826[2], libOut826[3], libOut826[4])

	// Create two gopus decoders: one with reset (default), one without
	goDec1, _ := gopus.NewDecoder(48000, 1) // Will reset (default behavior)

	// Process packets 0-825 through gopus
	for i := 0; i <= 825; i++ {
		goDec1.DecodeFloat32(packets[i])
	}

	// Get SILK decoder and NB resampler state BEFORE packet 826
	silkDec := goDec1.GetSILKDecoder()
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)

	// Capture the pre-reset state
	preResetSIIR := nbRes.GetSIIR()
	preResetSFIR := nbRes.GetSFIR()
	preResetDelayBuf := nbRes.GetDelayBuf()

	t.Logf("Decoder 1 NB resampler ID: %d", nbRes.GetDebugID())
	t.Logf("NB resampler state before packet 826:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", preResetSIIR[0], preResetSIIR[1], preResetSIIR[2])
	t.Logf("  sFIR[0:3] = [%d, %d, %d]", preResetSFIR[0], preResetSFIR[1], preResetSFIR[2])
	if len(preResetDelayBuf) >= 3 {
		t.Logf("  delayBuf[0:3] = [%d, %d, %d]", preResetDelayBuf[0], preResetDelayBuf[1], preResetDelayBuf[2])
	}

	// Enable debug to capture state at Process() call time
	nbRes.EnableDebug(true)

	// Decode packet 826 with default behavior (reset enabled)
	silkDec.DebugClearResetFlag()
	goOut1, _ := goDec1.DecodeFloat32(packets[826])
	t.Logf("\nWith reset: first 5 = [%.6f, %.6f, %.6f, %.6f, %.6f]",
		goOut1[0], goOut1[1], goOut1[2], goOut1[3], goOut1[4])

	// Check if reset was called
	wasReset1 := silkDec.DebugResetCalled()
	t.Logf("Decoder 1 reset called: %v", wasReset1)

	// Check state captured at Process() call time
	processStartState1 := nbRes.GetDebugProcessCallSIIR()
	t.Logf("Decoder 1 NB resampler state at Process() start:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", processStartState1[0], processStartState1[1], processStartState1[2])

	// Check input captured at Process() call time
	inputFirst10_1 := nbRes.GetDebugInputFirst10()
	t.Logf("Decoder 1 resampler input (first 10):")
	t.Logf("  [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		inputFirst10_1[0], inputFirst10_1[1], inputFirst10_1[2], inputFirst10_1[3], inputFirst10_1[4],
		inputFirst10_1[5], inputFirst10_1[6], inputFirst10_1[7], inputFirst10_1[8], inputFirst10_1[9])

	// Check delayBuf at Process() start
	delayBuf1 := nbRes.GetDebugDelayBufFirst8()
	t.Logf("Decoder 1 delayBuf at Process() start:")
	t.Logf("  [%d, %d, %d, %d, %d, %d, %d, %d]",
		delayBuf1[0], delayBuf1[1], delayBuf1[2], delayBuf1[3],
		delayBuf1[4], delayBuf1[5], delayBuf1[6], delayBuf1[7])

	// Check state AFTER decode
	stateAfterDecode1 := nbRes.GetSIIR()
	t.Logf("Decoder 1 NB resampler state AFTER packet 826:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", stateAfterDecode1[0], stateAfterDecode1[1], stateAfterDecode1[2])
	t.Logf("Decoder 1 Process() call count: %d, last process ID: %d", nbRes.GetDebugProcessCallCount(), nbRes.GetDebugLastProcessID())

	// Create another decoder and decode WITHOUT reset
	goDec2, _ := gopus.NewDecoder(48000, 1)
	for i := 0; i <= 825; i++ {
		goDec2.DecodeFloat32(packets[i])
	}

	// Get the SILK decoder and manually set the resampler state to preserved (pre-reset) state
	silkDec2 := goDec2.GetSILKDecoder()
	nbRes2 := silkDec2.GetResampler(silk.BandwidthNarrowband)

	// Capture state before decode
	stateBeforeDecode := nbRes2.GetSIIR()
	t.Logf("Decoder 2 NB resampler ID: %d", nbRes2.GetDebugID())
	t.Logf("\nDecoder 2 NB resampler state before packet 826:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", stateBeforeDecode[0], stateBeforeDecode[1], stateBeforeDecode[2])

	// Disable reset for this decode
	silkDec2.SetDisableResamplerReset(true)
	silkDec2.DebugClearResetFlag()

	// Enable debug to capture state at Process() call time
	nbRes2.EnableDebug(true)

	// CRITICAL: Check state IMMEDIATELY before the resampler.Process() call
	// We need to verify the state just before processing, not just before Opus decode
	t.Logf("Decoder 2 NB state JUST before decode: sIIR=[%d,%d,%d]",
		nbRes2.GetSIIR()[0], nbRes2.GetSIIR()[1], nbRes2.GetSIIR()[2])

	goOut2, _ := goDec2.DecodeFloat32(packets[826])

	// Check state captured at Process() call time
	processStartState2 := nbRes2.GetDebugProcessCallSIIR()
	t.Logf("Decoder 2 NB resampler state at Process() start:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", processStartState2[0], processStartState2[1], processStartState2[2])

	// Check input captured at Process() call time
	inputFirst10_2 := nbRes2.GetDebugInputFirst10()
	t.Logf("Decoder 2 resampler input (first 10):")
	t.Logf("  [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		inputFirst10_2[0], inputFirst10_2[1], inputFirst10_2[2], inputFirst10_2[3], inputFirst10_2[4],
		inputFirst10_2[5], inputFirst10_2[6], inputFirst10_2[7], inputFirst10_2[8], inputFirst10_2[9])

	// Check delayBuf at Process() start
	delayBuf2 := nbRes2.GetDebugDelayBufFirst8()
	t.Logf("Decoder 2 delayBuf at Process() start:")
	t.Logf("  [%d, %d, %d, %d, %d, %d, %d, %d]",
		delayBuf2[0], delayBuf2[1], delayBuf2[2], delayBuf2[3],
		delayBuf2[4], delayBuf2[5], delayBuf2[6], delayBuf2[7])

	// Check if reset was called despite being disabled
	wasReset := silkDec2.DebugResetCalled()
	t.Logf("Decoder 2 reset called (should be false): %v", wasReset)

	// Check state AFTER decode
	stateAfterDecode := nbRes2.GetSIIR()
	t.Logf("Decoder 2 NB resampler state AFTER packet 826:")
	t.Logf("  sIIR[0:3] = [%d, %d, %d]", stateAfterDecode[0], stateAfterDecode[1], stateAfterDecode[2])
	t.Logf("Decoder 2 Process() call count: %d, last process ID: %d", nbRes2.GetDebugProcessCallCount(), nbRes2.GetDebugLastProcessID())
	t.Logf("\nWithout reset: first 5 = [%.6f, %.6f, %.6f, %.6f, %.6f]",
		goOut2[0], goOut2[1], goOut2[2], goOut2[3], goOut2[4])

	// Check if slices share memory
	t.Logf("\nMemory check:")
	t.Logf("  goOut1 len=%d cap=%d ptr=%p", len(goOut1), cap(goOut1), &goOut1[0])
	t.Logf("  goOut2 len=%d cap=%d ptr=%p", len(goOut2), cap(goOut2), &goOut2[0])

	// Calculate SNR for both
	minLen := len(goOut1)
	if libN < minLen {
		minLen = libN
	}

	var sumSqErr1, sumSqErr2, sumSqSig float64
	for j := 0; j < minLen; j++ {
		diff1 := goOut1[j] - libOut826[j]
		diff2 := goOut2[j] - libOut826[j]
		sumSqErr1 += float64(diff1 * diff1)
		sumSqErr2 += float64(diff2 * diff2)
		sumSqSig += float64(libOut826[j] * libOut826[j])
	}
	snr1 := 10 * math.Log10(sumSqSig/sumSqErr1)
	snr2 := 10 * math.Log10(sumSqSig/sumSqErr2)

	t.Logf("\nSNR comparison for packet 826:")
	t.Logf("  With reset:    SNR = %.1f dB", snr1)
	t.Logf("  Without reset: SNR = %.1f dB", snr2)

	if snr2 > snr1 {
		t.Log("\n>>> NOT resetting the resampler produces better results!")
		t.Log(">>> This suggests libopus does NOT reset on bandwidth change.")
	} else {
		t.Log("\n>>> Resetting produces better results.")
		t.Log(">>> The issue may be elsewhere (sMid handling, native SILK decode, etc.)")
	}
}
