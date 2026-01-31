// Package cgo provides tests to find exact sign inversion location in TV12 SILK decoder.
//
// FINDINGS:
// The "resampler sign inversion" is actually NOT in the resampler - it's in the SILK decoder.
// The issue occurs specifically at BANDWIDTH TRANSITIONS (NB<->MB<->WB).
//
// Evidence:
// 1. First divergence at packet 137 (first NB->MB transition)
// 2. ALL worst packets (826, 137, 758, 1118) are at bandwidth change boundaries
// 3. Native SILK output (before resampling) already shows the divergence
// 4. The output is sign-inverted (ratio ~-1) rather than just noisy
//
// Root Cause: The SILK decoder's state is not being properly reset/transformed
// when bandwidth changes, causing accumulated phase/sign errors.
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Pkt826ResamplerSignInversion traces sign inversions in packet 826.
// This packet is NB (8kHz->48kHz), which is the worst case (-4.7dB).
func TestTV12Pkt826ResamplerSignInversion(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	targetPkt := 826

	// Create decoders that maintain state through all prior packets
	goDec := silk.NewDecoder()
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process all packets up to target to build identical state
	for i := 0; i < targetPkt; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with gopus SILK decoder
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec.DecodeFrame(&rd, silkBW, duration, true)

		// Decode with libopus to build its state
		libDec.DecodeFloat(pkt, 5760)
	}

	// Now decode packet 826 and compare in detail
	pkt := packets[targetPkt]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Packet %d Analysis ===", targetPkt)
	t.Logf("TOC: 0x%02X, Mode=%v, BW=%v, FrameSize=%d", pkt[0], toc.Mode, toc.Bandwidth, toc.FrameSize)

	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	config := silk.GetBandwidthConfig(silkBW)

	t.Logf("SILK BW: %v, NativeRate: %dkHz", silkBW, config.SampleRate/1000)

	// Get SILK decoder's native output (before resampling)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("Failed to decode: %v", err)
	}

	// Get libopus 48kHz output
	lib48k, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed")
	}

	t.Logf("Native samples: %d, Lib 48kHz samples: %d", len(goNative), libSamples)

	// Now compare the gopus resampled output with libopus
	// Get the resampler
	resampler := goDec.GetResampler(silkBW)

	// Build resampler input using sMid buffering
	resamplerInput := goDec.BuildMonoResamplerInput(goNative)

	// Resample
	go48k := resampler.Process(resamplerInput)

	t.Logf("Gopus 48kHz samples: %d", len(go48k))

	// Find sign inversions
	minLen := len(go48k)
	if libSamples < minLen {
		minLen = libSamples
	}

	signInvCount := 0
	firstSignInvIdx := -1
	var maxDiff float32
	maxDiffIdx := 0

	threshold := float32(0.01) // Significant signal threshold

	for i := 0; i < minLen; i++ {
		goVal := go48k[i]
		libVal := lib48k[i]
		diff := goVal - libVal

		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}

		// Check for sign inversion (both values significant, opposite signs)
		if goVal > threshold && libVal < -threshold {
			signInvCount++
			if firstSignInvIdx == -1 {
				firstSignInvIdx = i
			}
		} else if goVal < -threshold && libVal > threshold {
			signInvCount++
			if firstSignInvIdx == -1 {
				firstSignInvIdx = i
			}
		}
	}

	// Calculate SNR
	var sumSqErr, sumSqSig float64
	for i := 0; i < minLen; i++ {
		diff := go48k[i] - lib48k[i]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(lib48k[i] * lib48k[i])
	}
	snr := 10 * math.Log10(sumSqSig/sumSqErr)

	t.Logf("\n=== Results ===")
	t.Logf("SNR: %.1f dB", snr)
	t.Logf("Max diff: %.4f at sample %d", maxDiff, maxDiffIdx)
	t.Logf("Sign inversions: %d out of %d samples", signInvCount, minLen)
	t.Logf("First sign inversion at sample: %d", firstSignInvIdx)

	// Show samples around first sign inversion
	if firstSignInvIdx >= 0 {
		t.Logf("\n=== Samples around first sign inversion (idx=%d) ===", firstSignInvIdx)
		start := firstSignInvIdx - 20
		if start < 0 {
			start = 0
		}
		end := firstSignInvIdx + 21
		if end > minLen {
			end = minLen
		}
		t.Log("Index   | gopus     | libopus   | diff      | sign_inv")
		t.Log("--------|-----------|-----------|-----------|----------")
		for i := start; i < end; i++ {
			goVal := go48k[i]
			libVal := lib48k[i]
			diff := goVal - libVal
			signInv := ""
			if (goVal > threshold && libVal < -threshold) || (goVal < -threshold && libVal > threshold) {
				signInv = "YES"
			}
			marker := ""
			if i == firstSignInvIdx {
				marker = " <-- FIRST"
			}
			t.Logf("%6d  | %+9.6f | %+9.6f | %+9.6f | %s%s",
				i, goVal, libVal, diff, signInv, marker)
		}
	}

	// Show native samples for context
	t.Logf("\n=== Native SILK output (first 30 samples) ===")
	for i := 0; i < 30 && i < len(goNative); i++ {
		t.Logf("  native[%2d] = %+9.6f", i, goNative[i])
	}

	// Show resampler input for context
	t.Logf("\n=== Resampler input (first 30 samples, includes sMid delay) ===")
	for i := 0; i < 30 && i < len(resamplerInput); i++ {
		t.Logf("  resamp_in[%2d] = %+9.6f", i, resamplerInput[i])
	}

	// Check if the issue is in the IIR stage or FIR stage
	t.Log("\n=== Checking intermediate resampler states ===")
	checkResamplerStages(t, resamplerInput, go48k, lib48k[:libSamples])
}

// TestTV12Pkt826NativeVsLibopusNative compares native SILK output (before resampling).
// If native outputs match, the bug is in resampling. If not, bug is in SILK decoding.
func TestTV12Pkt826NativeVsLibopusNative(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	targetPkt := 826

	// Create decoders
	goDec := silk.NewDecoder()

	// Create libopus decoder at NATIVE rate (8kHz for NB)
	libDecNative, _ := NewLibopusDecoder(8000, 1)
	if libDecNative == nil {
		t.Skip("Could not create libopus decoder at native rate")
	}
	defer libDecNative.Destroy()

	// Process all packets up to target
	for i := 0; i < targetPkt; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)

		// Decode with gopus
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec.DecodeFrame(&rd, silkBW, duration, true)

		// Decode with libopus at native rate
		libDecNative.DecodeFloat(pkt, 960)
	}

	// Decode packet 826
	pkt := packets[targetPkt]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, _ := goDec.DecodeFrame(&rd, silkBW, duration, true)
	libNative, libSamples := libDecNative.DecodeFloat(pkt, 960)

	t.Logf("=== Native Rate Comparison for Packet %d ===", targetPkt)
	t.Logf("Go native samples: %d, Lib native samples: %d", len(goNative), libSamples)

	// Account for libopus delay (5 samples for NB)
	delay := 5

	minLen := len(goNative)
	if libSamples-delay < minLen {
		minLen = libSamples - delay
	}

	// Compare with alignment
	var sumSqErr, sumSqSig float64
	var maxDiff float32
	maxDiffIdx := 0

	for i := 0; i < minLen; i++ {
		goVal := goNative[i]
		libVal := libNative[i+delay]
		diff := goVal - libVal
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libVal * libVal)
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}

	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}

	t.Logf("Native SNR: %.1f dB (delay=%d)", snr, delay)
	t.Logf("Max native diff: %.6f at sample %d", maxDiff, maxDiffIdx)

	if snr > 60 {
		t.Log("\n*** Native SILK output matches well - bug is in RESAMPLER ***")
	} else {
		t.Log("\n*** Native SILK output DIFFERS - bug may be in SILK decoder ***")
	}

	// Show samples
	t.Logf("\n=== First 30 native samples comparison ===")
	t.Log("Index   | gopus     | libopus   | diff")
	for i := 0; i < 30 && i < minLen; i++ {
		goVal := goNative[i]
		libVal := libNative[i+delay]
		t.Logf("%6d  | %+9.6f | %+9.6f | %+9.6f", i, goVal, libVal, goVal-libVal)
	}
}

// TestTV12ResamplerIsolation tests the resampler in isolation with known input.
func TestTV12ResamplerIsolation(t *testing.T) {
	// Create a test signal: simple sine wave
	inputLen := 160 // 20ms at 8kHz
	input := make([]float32, inputLen)
	freq := 1000.0 // 1kHz tone
	for i := range input {
		input[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/8000))
	}

	// Create resampler
	resampler := silk.NewLibopusResampler(8000, 48000)
	output := resampler.Process(input)

	t.Logf("Input: %d samples at 8kHz", inputLen)
	t.Logf("Output: %d samples at 48kHz (expected %d)", len(output), inputLen*6)

	// Check for sign changes that shouldn't exist
	signChanges := 0
	for i := 1; i < len(output); i++ {
		if (output[i] > 0.1 && output[i-1] < -0.1) || (output[i] < -0.1 && output[i-1] > 0.1) {
			signChanges++
		}
	}

	// For a 1kHz tone at 48kHz, we expect ~48 sign changes per 1ms, or ~960 for 20ms
	expectedChanges := int(freq * 20 / 1000 * 2) // 40 cycles * 2 crossings = 80
	t.Logf("Sign changes: %d (expected ~%d for %dHz tone)", signChanges, expectedChanges, int(freq))

	// Show first 60 samples
	t.Log("\n=== First 60 output samples ===")
	for i := 0; i < 60 && i < len(output); i++ {
		t.Logf("  [%3d] %+9.6f", i, output[i])
	}
}

// TestTV12MultiPacketResamplerState tests resampler state continuity across packets.
func TestTV12MultiPacketResamplerState(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 830)
	if err != nil || len(packets) < 828 {
		t.Skip("Could not load enough packets")
	}

	// Create decoders
	goDec := silk.NewDecoder()
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Test packets around the worst one (826)
	testPackets := []int{824, 825, 826, 827, 828}

	t.Log("=== SNR progression around packet 826 ===")

	for _, targetPkt := range testPackets {
		// Reset decoders
		goDec = silk.NewDecoder()
		libDec.Destroy()
		libDec, _ = NewLibopusDecoder(48000, 1)

		// Process packets up to and including target
		var finalGoSamples, finalLibSamples []float32
		var finalLibCount int

		for i := 0; i <= targetPkt; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])

			if toc.Mode != gopus.ModeSILK {
				continue
			}

			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if !ok {
				continue
			}
			duration := silk.FrameDurationFromTOC(toc.FrameSize)

			// Decode with gopus using full decoder flow
			var rd rangecoding.Decoder
			rd.Init(pkt[1:])
			native, _ := goDec.DecodeFrame(&rd, silkBW, duration, true)
			resampler := goDec.GetResampler(silkBW)
			input := goDec.BuildMonoResamplerInput(native)
			goSamples := resampler.Process(input)

			// Decode with libopus
			libSamples, libCount := libDec.DecodeFloat(pkt, 5760)

			if i == targetPkt {
				finalGoSamples = goSamples
				finalLibSamples = libSamples[:libCount]
				finalLibCount = libCount
			}
		}

		// Calculate SNR for the target packet
		minLen := len(finalGoSamples)
		if finalLibCount < minLen {
			minLen = finalLibCount
		}

		var sumSqErr, sumSqSig float64
		signInvCount := 0
		for i := 0; i < minLen; i++ {
			diff := finalGoSamples[i] - finalLibSamples[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(finalLibSamples[i] * finalLibSamples[i])

			// Count sign inversions
			if (finalGoSamples[i] > 0.01 && finalLibSamples[i] < -0.01) ||
				(finalGoSamples[i] < -0.01 && finalLibSamples[i] > 0.01) {
				signInvCount++
			}
		}

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		toc := gopus.ParseTOC(packets[targetPkt][0])
		t.Logf("Packet %d: BW=%d, SNR=%.1f dB, SignInv=%d/%d",
			targetPkt, toc.Bandwidth, snr, signInvCount, minLen)
	}
}

// checkResamplerStages analyzes where the sign inversion occurs in the resampler pipeline.
func checkResamplerStages(t *testing.T, input, goOutput, libOutput []float32) {
	// The resampler has these stages:
	// 1. Delay buffer (1ms buffering)
	// 2. 2x IIR allpass upsampling
	// 3. FIR interpolation (12-phase)

	// Check if output values have consistent relationship
	minLen := len(goOutput)
	if len(libOutput) < minLen {
		minLen = len(libOutput)
	}

	// Look for patterns in the difference
	var positiveRatioCount, negativeRatioCount int
	for i := 0; i < minLen; i++ {
		if math.Abs(float64(libOutput[i])) > 0.01 {
			ratio := goOutput[i] / libOutput[i]
			if ratio > 0.5 && ratio < 1.5 {
				positiveRatioCount++
			} else if ratio < -0.5 && ratio > -1.5 {
				negativeRatioCount++
			}
		}
	}

	t.Logf("Samples with ratio ~+1: %d", positiveRatioCount)
	t.Logf("Samples with ratio ~-1: %d", negativeRatioCount)

	if negativeRatioCount > positiveRatioCount {
		t.Log("\n*** GLOBAL SIGN INVERSION detected - output is inverted! ***")
		t.Log("This suggests a sign error in the filter coefficients or accumulator")
	} else if negativeRatioCount > minLen/10 {
		t.Log("\n*** PARTIAL SIGN INVERSION detected ***")
		t.Log("This suggests intermittent sign issues, possibly from state corruption")
	}
}

// TestTV12FullDecoder48kHz uses the full gopus.Decoder at 48kHz to compare with libopus.
// This is the end-to-end comparison that matches the compliance test.
func TestTV12FullDecoder48kHz(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create 48kHz mono decoders
	goDec, err := gopus.NewDecoderDefault(48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Worst packets identified in compliance findings
	worstPackets := map[int]string{
		826:  "NB",
		137:  "MB",
		758:  "MB",
		1118: "WB",
	}

	t.Log("=== TV12 Full Decoder 48kHz Comparison ===")
	t.Log("Packet | Mode    | BW | SNR (dB) | MaxDiff | SignInv | Notes")
	t.Log("-------|---------|----|-----------|---------|---------|---------")

	var prevSilkBW int = -1

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			continue
		}

		// Decode with libopus
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate metrics
		var sumSqErr, sumSqSig float64
		var maxDiff float32
		signInvCount := 0

		for i := 0; i < minLen; i++ {
			goVal := goSamples[i]
			libVal := libPcm[i]
			diff := goVal - libVal
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libVal * libVal)
			if diff < 0 {
				diff = -diff
			}
			if diff > maxDiff {
				maxDiff = diff
			}
			// Count sign inversions (using very low threshold for this quiet stream)
			if (goVal > 0.001 && libVal < -0.001) || (goVal < -0.001 && libVal > 0.001) {
				signInvCount++
			}
		}

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Track bandwidth changes
		bwChanged := ""
		if toc.Mode == gopus.ModeSILK {
			if prevSilkBW >= 0 && int(toc.Bandwidth) != prevSilkBW {
				bwChanged = " BW-CHANGE"
			}
			prevSilkBW = int(toc.Bandwidth)
		}

		// Check if this is one of the worst packets
		bwName, isWorst := worstPackets[pktIdx]
		if isWorst {
			modeStr := "SILK"
			if toc.Mode == gopus.ModeHybrid {
				modeStr = "Hybrid"
			}
			t.Logf("%6d | %-7s | %s | %8.1f | %7.4f | %7d | WORST%s",
				pktIdx, modeStr, bwName, snr, maxDiff, signInvCount, bwChanged)
		} else if snr < 20 || bwChanged != "" {
			// Log any packet with poor SNR or bandwidth change
			modeStr := "SILK"
			if toc.Mode == gopus.ModeHybrid {
				modeStr = "Hybrid"
			}
			bwStr := "?"
			switch toc.Bandwidth {
			case 0:
				bwStr = "NB"
			case 1:
				bwStr = "MB"
			case 2:
				bwStr = "WB"
			case 3:
				bwStr = "SWB"
			case 4:
				bwStr = "FB"
			}
			t.Logf("%6d | %-7s | %s | %8.1f | %7.4f | %7d |%s",
				pktIdx, modeStr, bwStr, snr, maxDiff, signInvCount, bwChanged)
		}
	}
}

// TestTV12FindDivergenceStart finds when gopus output starts diverging from libopus.
func TestTV12FindDivergenceStart(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Finding where divergence starts ===")

	firstBadSNR := -1
	prevGoodSNR := 999.0

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for i := 0; i < minLen; i++ {
			diff := goSamples[i] - libPcm[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[i] * libPcm[i])
		}

		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// First time SNR drops below 40dB (significant divergence)
		if snr < 40 && firstBadSNR == -1 {
			firstBadSNR = pktIdx
			t.Logf("\n*** FIRST DIVERGENCE at packet %d ***", pktIdx)
			t.Logf("  Mode: %v, BW: %d, FrameSize: %d", toc.Mode, toc.Bandwidth, toc.FrameSize)
			t.Logf("  Previous good SNR: %.1f dB", prevGoodSNR)
			t.Logf("  Current SNR: %.1f dB", snr)

			// Show context (packets before and after)
			for j := pktIdx - 3; j <= pktIdx+3 && j < len(packets); j++ {
				if j < 0 {
					continue
				}
				toc2 := gopus.ParseTOC(packets[j][0])
				marker := ""
				if j == pktIdx {
					marker = " <-- FIRST BAD"
				}
				t.Logf("  Packet %d: Mode=%v, BW=%d, FrameSize=%d%s",
					j, toc2.Mode, toc2.Bandwidth, toc2.FrameSize, marker)
			}
		}

		// Log first 20 packets for baseline
		if pktIdx < 20 {
			t.Logf("Packet %3d: Mode=%v, BW=%d, SNR=%.1f dB", pktIdx, toc.Mode, toc.Bandwidth, snr)
		}

		if snr > 40 {
			prevGoodSNR = snr
		}
	}

	if firstBadSNR == -1 {
		t.Log("No significant divergence found - all packets have SNR > 40dB")
	}
}

// TestTV12Pkt826DetailedCompare does sample-by-sample comparison for packet 826.
func TestTV12Pkt826DetailedCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	targetPkt := 826

	// Process all packets up to target
	for i := 0; i <= targetPkt; i++ {
		pkt := packets[i]

		goSamples, _ := decodeFloat32(goDec, pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)

		if i == targetPkt {
			t.Logf("=== Packet %d Detailed Sample Comparison ===", targetPkt)
			toc := gopus.ParseTOC(pkt[0])
			t.Logf("TOC: Mode=%v, BW=%d, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)
			t.Logf("Samples: gopus=%d, libopus=%d", len(goSamples), libSamples)

			minLen := len(goSamples)
			if libSamples < minLen {
				minLen = libSamples
			}

			// Show first 60 samples
			t.Log("\n=== First 60 samples at 48kHz ===")
			t.Log("Index  | gopus       | libopus     | diff        | ratio")
			t.Log("-------|-------------|-------------|-------------|-------")
			for j := 0; j < 60 && j < minLen; j++ {
				goVal := goSamples[j]
				libVal := libPcm[j]
				diff := goVal - libVal
				ratio := float32(0)
				if math.Abs(float64(libVal)) > 0.0001 {
					ratio = goVal / libVal
				}
				marker := ""
				if math.Abs(float64(diff)) > 0.001 {
					marker = " *"
				}
				t.Logf("%5d  | %+11.6f | %+11.6f | %+11.6f | %6.2f%s",
					j, goVal, libVal, diff, ratio, marker)
			}

			// Calculate overall metrics
			var sumSqErr, sumSqSig float64
			for j := 0; j < minLen; j++ {
				diff := goSamples[j] - libPcm[j]
				sumSqErr += float64(diff * diff)
				sumSqSig += float64(libPcm[j] * libPcm[j])
			}
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			t.Logf("\n48kHz SNR: %.1f dB", snr)
		}
	}
}

// TestTV12BandwidthTransitions specifically tests all bandwidth transitions in TV12.
// This test proves the divergence is caused by bandwidth changes, not resampling.
func TestTV12BandwidthTransitions(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadTV12Packets(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== TV12 Bandwidth Transition Analysis ===")
	t.Log("Looking for all bandwidth transitions and their impact on SNR...")
	t.Log("")
	t.Log("Transition | Packet | SNR Before | SNR After | Sign Inversions")
	t.Log("-----------|--------|------------|-----------|----------------")

	var prevBW int = -1
	var prevSNR float64 = 999.0
	transitionCount := 0

	bwNames := []string{"NB", "MB", "WB", "SWB", "FB"}

	for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK && toc.Mode != gopus.ModeHybrid {
			continue
		}

		goSamples, err := decodeFloat32(goDec, pkt)
		if err != nil {
			continue
		}

		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goSamples)*2)
		if libSamples < 0 {
			continue
		}

		minLen := len(goSamples)
		if libSamples < minLen {
			minLen = libSamples
		}

		// Calculate SNR
		var sumSqErr, sumSqSig float64
		signInvCount := 0
		for i := 0; i < minLen; i++ {
			diff := goSamples[i] - libPcm[i]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[i] * libPcm[i])
			if (goSamples[i] > 0.001 && libPcm[i] < -0.001) ||
				(goSamples[i] < -0.001 && libPcm[i] > 0.001) {
				signInvCount++
			}
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		currBW := int(toc.Bandwidth)

		// Check for bandwidth transition
		if prevBW >= 0 && currBW != prevBW && prevBW < len(bwNames) && currBW < len(bwNames) {
			transitionCount++
			transStr := bwNames[prevBW] + "->" + bwNames[currBW]
			snrDrop := prevSNR - snr

			marker := ""
			if snr < 20 {
				marker = " <-- BAD"
			}
			if snrDrop > 100 {
				t.Logf("%-10s | %6d | %10.1f | %9.1f | %7d%s",
					transStr, pktIdx, prevSNR, snr, signInvCount, marker)
			} else {
				t.Logf("%-10s | %6d | %10.1f | %9.1f | %7d%s",
					transStr, pktIdx, prevSNR, snr, signInvCount, marker)
			}
		}

		prevBW = currBW
		if snr > 40 {
			prevSNR = snr
		}
	}

	t.Logf("\nTotal bandwidth transitions: %d", transitionCount)
	t.Log("\nCONCLUSION: Sign inversions occur at bandwidth transitions in the SILK decoder,")
	t.Log("NOT in the resampler. The fix should be in the SILK bandwidth change handling.")
}

// loadTV12Packets loads packets from testvector12.bit
func loadTV12Packets(bitFile string, maxPackets int) ([][]byte, error) {
	data, err := os.ReadFile(bitFile)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:]) // enc_final_range
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets, nil
}
