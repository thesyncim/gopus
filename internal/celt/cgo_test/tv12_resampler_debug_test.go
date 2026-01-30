// Package cgo debugs resampler state on bandwidth transitions.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerDebug traces resampler state through the stream.
func TestTV12ResamplerDebug(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder directly to access internal state
	silkDec := silk.NewDecoder()

	t.Log("=== Tracing SILK decoder bandwidth changes ===")

	prevBW := silk.Bandwidth(255) // Invalid initial value
	bwChanges := 0

	for i := 0; i <= 828; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Only process SILK packets
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		_ = silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Log bandwidth changes
		if silkBW != prevBW {
			bwChanges++
			var prevName string
			switch prevBW {
			case silk.BandwidthNarrowband:
				prevName = "NB"
			case silk.BandwidthMediumband:
				prevName = "MB"
			case silk.BandwidthWideband:
				prevName = "WB"
			default:
				prevName = "none"
			}
			var currName string
			switch silkBW {
			case silk.BandwidthNarrowband:
				currName = "NB"
			case silk.BandwidthMediumband:
				currName = "MB"
			case silk.BandwidthWideband:
				currName = "WB"
			}
			t.Logf("BW change #%d at packet %d: %s -> %s (new rate: %dkHz)",
				bwChanges, i, prevName, currName,
				config.SampleRate/1000)
		}
		prevBW = silkBW

		// Decode the packet using DecodeWithDecoder
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		_, err := silkDec.DecodeWithDecoder(&rd, silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
			continue
		}
	}

	t.Logf("\nTotal bandwidth changes: %d", bwChanges)
}

// TestTV12CompareSilkAtNativeRate compares SILK output at native rate (before resampling).
// This helps isolate whether the issue is in SILK decoding or resampling.
func TestTV12CompareSilkAtNativeRate(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder at NATIVE rate for packet 826 (which is NB = 8kHz)
	libDec8k, _ := NewLibopusDecoder(8000, 1)
	if libDec8k == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec8k.Destroy()

	// Also create 12kHz decoder for MB packets
	libDec12k, _ := NewLibopusDecoder(12000, 1)
	if libDec12k == nil {
		t.Skip("Could not create 12k libopus decoder")
	}
	defer libDec12k.Destroy()

	// Process all SILK packets to build state
	for i := 0; i <= 828; i++ {
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

		// Choose the right libopus decoder based on bandwidth
		var libDec *LibopusDecoder
		var nativeRate int
		if silkBW == silk.BandwidthNarrowband {
			libDec = libDec8k
			nativeRate = 8000
		} else {
			libDec = libDec12k
			nativeRate = 12000
		}

		// Decode with gopus SILK decoder (native rate)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goNative, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
		if err != nil {
			continue
		}

		// Decode with libopus at native rate
		libPcm, libSamples := libDec.DecodeFloat(pkt, len(goNative)*2)

		// Compute SNR at native rate
		minLen := len(goNative)
		if libSamples < minLen {
			minLen = libSamples
		}

		var sumSqErr, sumSqSig float64
		for j := 0; j < minLen; j++ {
			diff := goNative[j] - libPcm[j]
			sumSqErr += float64(diff * diff)
			sumSqSig += float64(libPcm[j] * libPcm[j])
		}
		snr := 10 * math.Log10(sumSqSig/sumSqErr)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		// Only report packets with issues
		if i >= 824 && i <= 828 {
			t.Logf("Packet %d: BW=%v (%dkHz), NativeSNR=%.1f dB, goLen=%d, libLen=%d",
				i, silkBW, nativeRate/1000, snr, len(goNative), libSamples)
		}
	}
}

// TestTV12ResamplerInternals traces the exact resampler internals for packet 137
func TestTV12ResamplerInternals(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create standalone SILK decoder
	silkDec := silk.NewDecoder()

	// Process packets 0-136 to build state
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Now decode packet 137 (first MB packet after NB)
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("Packet 137: BW=%d (MB 12kHz), FrameSize=%d", toc.Bandwidth, toc.FrameSize)

	// Get sMid state
	sMid := silkDec.GetSMid()
	t.Logf("sMid: [%d, %d]", sMid[0], sMid[1])

	// Decode frame
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("Native samples: %d", len(nativeSamples))

	// Build resampler input
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("Resampler input length: %d", len(resamplerInput))

	// Convert to int16 like Process() does
	in := make([]int16, len(resamplerInput))
	for i, s := range resamplerInput {
		scaled := s * 32768.0
		if scaled > 32767 {
			in[i] = 32767
		} else if scaled < -32768 {
			in[i] = -32768
		} else {
			in[i] = int16(scaled)
		}
	}
	t.Logf("First 15 int16 input:")
	for i := 0; i < 15 && i < len(in); i++ {
		t.Logf("  [%d] %d", i, in[i])
	}

	// Create a fresh resampler for 12kHz->48kHz
	resampler := silk.NewLibopusResampler(12000, 48000)
	t.Logf("\nResampler config:")
	t.Logf("  fsInKHz: %d", resampler.FsInKHz())
	t.Logf("  fsOutKHz: %d", resampler.FsOutKHz())
	t.Logf("  inputDelay: %d", resampler.InputDelay())
	t.Logf("  invRatioQ16: %d (0x%x)", resampler.InvRatioQ16(), resampler.InvRatioQ16())
	t.Logf("  batchSize: %d", resampler.BatchSize())

	// Now manually simulate what Process() does
	inLen := int32(len(in))
	fsInKHz := int32(12)
	fsOutKHz := int32(48)
	inputDelay := int32(4)

	outLen := int(inLen) * int(fsOutKHz) / int(fsInKHz)
	t.Logf("\nExpected output length: %d", outLen)

	nSamples := fsInKHz - inputDelay // = 8
	t.Logf("nSamples (first part): %d", nSamples)

	// The delay buffer gets populated with zeros + first nSamples
	delayBuf := make([]int16, 12)
	copy(delayBuf[inputDelay:], in[:nSamples])

	t.Logf("\nDelay buffer after copy:")
	for i := 0; i < 12; i++ {
		t.Logf("  [%d] %d", i, delayBuf[i])
	}

	// Now call Process() on the full input
	output := resampler.Process(resamplerInput)
	t.Logf("\nProcess() output length: %d", len(output))

	// Check all output samples
	var nonZeroCount int
	var firstNonZeroIdx int = -1
	var lastNonZeroIdx int = -1
	var maxAbsVal float32
	var maxAbsIdx int
	for i, s := range output {
		if s != 0 {
			nonZeroCount++
			if firstNonZeroIdx < 0 {
				firstNonZeroIdx = i
			}
			lastNonZeroIdx = i
		}
		abs := s
		if abs < 0 {
			abs = -abs
		}
		if abs > maxAbsVal {
			maxAbsVal = abs
			maxAbsIdx = i
		}
	}
	t.Logf("Non-zero samples: %d / %d", nonZeroCount, len(output))
	t.Logf("First non-zero at: %d", firstNonZeroIdx)
	t.Logf("Last non-zero at: %d", lastNonZeroIdx)
	t.Logf("Max abs value: %.6f at index %d", maxAbsVal, maxAbsIdx)

	t.Logf("\nFirst 50 output samples:")
	for i := 0; i < 50 && i < len(output); i++ {
		t.Logf("  [%d] %.6f", i, output[i])
	}

	if firstNonZeroIdx > 50 && firstNonZeroIdx < len(output) {
		t.Logf("\nAround first non-zero (%d):", firstNonZeroIdx)
		for i := firstNonZeroIdx - 3; i < firstNonZeroIdx+5 && i < len(output); i++ {
			if i >= 0 {
				t.Logf("  [%d] %.6f", i, output[i])
			}
		}
	}

	// Compare with libopus if available
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec != nil {
		defer libDec.Destroy()

		// Process packets 0-136 with libopus
		for i := 0; i <= 136; i++ {
			libDec.DecodeFloat(packets[i], 1920)
		}

		// Decode packet 137 with libopus
		libOut, libSamples := libDec.DecodeFloat(pkt, 1920)
		t.Logf("\n=== LibOpus output ===")
		t.Logf("Samples: %d", libSamples)
		t.Logf("First 50:")
		for i := 0; i < 50 && i < libSamples; i++ {
			t.Logf("  [%d] %.6f", i, libOut[i])
		}

		// Compare
		t.Logf("\n=== Comparison (first 50) ===")
		for i := 0; i < 50 && i < libSamples && i < len(output); i++ {
			diff := output[i] - libOut[i]
			marker := ""
			if diff != 0 && output[i] == 0 {
				marker = " <-- ZERO IN GO"
			}
			t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f%s", i, output[i], libOut[i], diff, marker)
		}
	}
}

// TestTV12ShiftAnalysis checks if gopus output matches libopus with a time shift
func TestTV12ShiftAnalysis(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder
	goDec := silk.NewDecoder()

	// Create libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-136 to build state
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode with libopus
		libDec.DecodeFloat(pkt, 1920)

		// Decode with gopus
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				goDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			}
		}
	}

	// Now decode packet 137 (first MB after NB)
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	goOut, _ := goDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	libOut, libSamples := libDec.DecodeFloat(pkt, 1920)

	t.Logf("Packet 137 output lengths: go=%d lib=%d", len(goOut), libSamples)

	// Find first non-zero in gopus
	goFirstNonZero := -1
	for i, v := range goOut {
		if v != 0 {
			goFirstNonZero = i
			break
		}
	}
	t.Logf("Gopus first non-zero at: %d", goFirstNonZero)

	// Test different shift values
	t.Logf("\n=== Testing correlation with different shifts ===")
	for shift := 0; shift <= 50; shift += 5 {
		var sumSqErr, sumSqSig float64
		compareLen := libSamples - shift
		if compareLen > len(goOut) {
			compareLen = len(goOut)
		}
		if compareLen <= 0 {
			continue
		}

		for i := 0; i < compareLen; i++ {
			goSample := float64(0)
			if i < len(goOut) {
				goSample = float64(goOut[i])
			}
			libSample := float64(0)
			if i+shift < libSamples {
				libSample = float64(libOut[i+shift])
			}
			diff := goSample - libSample
			sumSqErr += diff * diff
			sumSqSig += libSample * libSample
		}
		if sumSqSig > 0 {
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			t.Logf("  Shift %d: SNR=%.1f dB", shift, snr)
		}
	}

	// Also test negative shifts (gopus ahead of libopus)
	t.Logf("\n=== Testing negative shifts (gopus ahead) ===")
	for shift := -50; shift < 0; shift += 5 {
		var sumSqErr, sumSqSig float64
		startGo := -shift
		compareLen := len(goOut) - startGo
		if compareLen > libSamples {
			compareLen = libSamples
		}
		if compareLen <= 0 {
			continue
		}

		for i := 0; i < compareLen; i++ {
			goSample := float64(goOut[i+startGo])
			libSample := float64(libOut[i])
			diff := goSample - libSample
			sumSqErr += diff * diff
			sumSqSig += libSample * libSample
		}
		if sumSqSig > 0 {
			snr := 10 * math.Log10(sumSqSig/sumSqErr)
			t.Logf("  Shift %d: SNR=%.1f dB", shift, snr)
		}
	}
}

// TestTV12DirectResamplerCompare directly compares gopus and libopus resamplers
func TestTV12DirectResamplerCompare(t *testing.T) {
	// Create test input: 240 samples at 12kHz (20ms)
	// First 4 samples are zeros (like the delay buffer), then real data
	input := make([]int16, 240)
	for i := 0; i < 4; i++ {
		input[i] = 0
	}
	// Fill rest with a simple pattern
	for i := 4; i < len(input); i++ {
		input[i] = int16((i - 4) * 100) // Ramp from 0 to 23500
	}

	t.Logf("Input first 15: %v", input[:15])

	// Create gopus resampler
	goResampler := silk.NewLibopusResampler(12000, 48000)

	// Convert to float32 for gopus
	floatInput := make([]float32, len(input))
	for i, v := range input {
		floatInput[i] = float32(v) / 32768.0
	}

	// Process with gopus
	goOutput := goResampler.Process(floatInput)

	t.Logf("Gopus output length: %d", len(goOutput))
	t.Logf("Gopus first 50:")
	for i := 0; i < 50 && i < len(goOutput); i++ {
		t.Logf("  [%d] %.6f (int16: %d)", i, goOutput[i], int16(goOutput[i]*32768))
	}

	// Find first non-zero
	for i, v := range goOutput {
		if v != 0 {
			t.Logf("Gopus first non-zero at index %d: %.6f", i, v)
			break
		}
	}

	// Now process with libopus resampler directly (via CGO)
	libOutput := ProcessLibopusResampler(input, 12000, 48000)
	if len(libOutput) == 0 {
		t.Skip("Could not process with libopus resampler")
	}
	libSamples := len(libOutput)

	t.Logf("\nLibopus output length: %d", libSamples)
	t.Logf("Libopus first 50:")
	for i := 0; i < 50 && i < libSamples; i++ {
		t.Logf("  [%d] %.6f (int16: %d)", i, float64(libOutput[i])/32768.0, libOutput[i])
	}

	// Find first non-zero
	for i := 0; i < libSamples; i++ {
		if libOutput[i] != 0 {
			t.Logf("Libopus first non-zero at index %d: %d", i, libOutput[i])
			break
		}
	}

	// Compare
	t.Logf("\n=== Comparison (first 50) ===")
	for i := 0; i < 50 && i < libSamples && i < len(goOutput); i++ {
		goInt := int16(goOutput[i] * 32768)
		diff := goInt - libOutput[i]
		marker := ""
		if goInt == 0 && libOutput[i] != 0 {
			marker = " <-- MISMATCH"
		}
		t.Logf("  [%d] go=%d lib=%d diff=%d%s", i, goInt, libOutput[i], diff, marker)
	}
}

// TestTV12IsolateResamplerIssue tests if the issue is in resampling alone.
func TestTV12IsolateResamplerIssue(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Get the native output for packet 826
	t.Log("=== Decoding packet 826 at native rate ===")

	// First, build state by decoding packets 0-825
	for i := 0; i < 826; i++ {
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
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		silkDec.DecodeFrame(&rd, silkBW, duration, true)
	}

	// Now decode packet 826 at native rate
	pkt826 := packets[826]
	toc826 := gopus.ParseTOC(pkt826[0])
	t.Logf("Packet 826: TOC=0x%02X, BW=%d, Mode=%d", pkt826[0], toc826.Bandwidth, toc826.Mode)

	silkBW826, _ := silk.BandwidthFromOpus(int(toc826.Bandwidth))
	duration826 := silk.FrameDurationFromTOC(toc826.FrameSize)

	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	goNative826, err := silkDec.DecodeFrame(&rd, silkBW826, duration826, true)
	if err != nil {
		t.Fatalf("Failed to decode packet 826: %v", err)
	}

	t.Logf("Packet 826 native output: %d samples at %dkHz",
		len(goNative826), silk.GetBandwidthConfig(silkBW826).SampleRate/1000)

	// Now create a FRESH resampler and resample
	freshResampler := silk.NewLibopusResampler(8000, 48000)
	freshOutput := freshResampler.Process(goNative826)
	t.Logf("Fresh resampler output: %d samples", len(freshOutput))

	// Now get the resampler from the decoder (which has state) and compare
	stateResampler := silkDec.GetResampler(silkBW826)
	stateOutput := stateResampler.Process(goNative826)
	t.Logf("State resampler output: %d samples", len(stateOutput))

	// Compare fresh vs state resampler outputs
	minLen := len(freshOutput)
	if len(stateOutput) < minLen {
		minLen = len(stateOutput)
	}

	var sumSqErr, sumSqSig float64
	var maxDiff float32
	maxDiffIdx := 0
	for i := 0; i < minLen; i++ {
		diff := freshOutput[i] - stateOutput[i]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(freshOutput[i] * freshOutput[i])
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

	t.Logf("\nFresh vs State resampler comparison:")
	t.Logf("  SNR: %.1f dB", snr)
	t.Logf("  MaxDiff: %.6f at sample %d", maxDiff, maxDiffIdx)

	if maxDiff > 0.001 {
		t.Log("\nSamples around max diff:")
		start := maxDiffIdx - 5
		if start < 0 {
			start = 0
		}
		end := maxDiffIdx + 6
		if end > minLen {
			end = minLen
		}
		for i := start; i < end; i++ {
			marker := ""
			if i == maxDiffIdx {
				marker = " <-- MAX"
			}
			t.Logf("  [%4d] fresh=%+9.6f state=%+9.6f diff=%+9.6f%s",
				i, freshOutput[i], stateOutput[i], freshOutput[i]-stateOutput[i], marker)
		}
	}
}
