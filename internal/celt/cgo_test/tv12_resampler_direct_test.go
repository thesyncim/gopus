// Package cgo tests the resampler directly with known input
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
)

// TestResamplerDirectCompare directly compares gopus and libopus resampler
// with identical int16 input to isolate resampler differences.
func TestResamplerDirectCompare(t *testing.T) {
	// Test 12kHz -> 48kHz (MB bandwidth, matches TV12 packet 137)
	fsIn := 12000
	fsOut := 48000

	// Get libopus resampler parameters
	libParams := GetLibopusResamplerParams(fsIn, fsOut)

	t.Logf("Libopus resampler params for %dHz -> %dHz:", fsIn, fsOut)
	t.Logf("  inputDelay: %d", libParams.InputDelay)
	t.Logf("  invRatioQ16: %d", libParams.InvRatioQ16)
	t.Logf("  batchSize: %d", libParams.BatchSize)

	// Create gopus resampler
	goResampler := silk.NewLibopusResampler(fsIn, fsOut)

	t.Logf("\nGopus resampler params:")
	t.Logf("  inputDelay: %d", goResampler.InputDelay())
	t.Logf("  invRatioQ16: %d", goResampler.InvRatioQ16())
	t.Logf("  batchSize: %d", goResampler.BatchSize())

	// Verify parameters match
	if libParams.InputDelay != goResampler.InputDelay() {
		t.Errorf("inputDelay mismatch: lib=%d, go=%d", libParams.InputDelay, goResampler.InputDelay())
	}
	if libParams.InvRatioQ16 != goResampler.InvRatioQ16() {
		t.Errorf("invRatioQ16 mismatch: lib=%d, go=%d", libParams.InvRatioQ16, goResampler.InvRatioQ16())
	}

	// Generate test input (20ms = 240 samples at 12kHz)
	// First sample is sMid[1] = -323 (like TV12 packet 137)
	inLen := 240
	input := make([]int16, inLen)
	input[0] = -323 // sMid[1] from TV12
	for i := 1; i < inLen; i++ {
		// Rest is a simple pattern
		input[i] = int16(1000 * math.Sin(float64(i)*2*math.Pi/40))
	}

	// Process with libopus
	libOutput := ProcessLibopusResampler(input, fsIn, fsOut)

	// Process with gopus
	goInput := make([]float32, inLen)
	for i, v := range input {
		goInput[i] = float32(v) / 32768.0
	}
	goOutputFloat := goResampler.Process(goInput)
	goOutput := make([]int16, len(goOutputFloat))
	for i, v := range goOutputFloat {
		goOutput[i] = int16(v * 32768.0)
	}

	// Compare outputs
	t.Logf("\nOutput comparison:")
	var sumSqErr, sumSqSig float64
	var maxDiff int
	maxDiffIdx := 0

	minLen := len(libOutput)
	if len(goOutput) < minLen {
		minLen = len(goOutput)
	}

	for i := 0; i < minLen; i++ {
		diff := int(goOutput[i]) - int(libOutput[i])
		if diff < 0 {
			diff = -diff
		}
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOutput[i]) * float64(libOutput[i])
	}

	snr := 10 * math.Log10(sumSqSig/sumSqErr)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999.0
	}

	t.Logf("SNR: %.1f dB", snr)
	t.Logf("Max diff: %d at sample %d", maxDiff, maxDiffIdx)
	t.Logf("Output lengths: lib=%d, go=%d", len(libOutput), len(goOutput))

	// Show first 10 samples
	t.Log("\nFirst 10 samples:")
	for i := 0; i < 10 && i < minLen; i++ {
		t.Logf("  [%3d] lib=%6d go=%6d diff=%d", i, libOutput[i], goOutput[i], int(goOutput[i])-int(libOutput[i]))
	}

	// Show samples around max diff
	if maxDiff > 1 {
		t.Log("\nSamples around max diff:")
		start := maxDiffIdx - 3
		if start < 0 {
			start = 0
		}
		end := maxDiffIdx + 5
		if end > minLen {
			end = minLen
		}
		for i := start; i < end; i++ {
			marker := ""
			if i == maxDiffIdx {
				marker = " <-- MAX"
			}
			t.Logf("  [%3d] lib=%6d go=%6d diff=%d%s", i, libOutput[i], goOutput[i], int(goOutput[i])-int(libOutput[i]), marker)
		}
	}

	// Test should pass if SNR > 50 dB (very close match)
	if snr < 50 {
		t.Errorf("Resampler output mismatch: SNR=%.1f dB (expected >50 dB)", snr)
	}
}

// TestResamplerTV12Input tests with the actual TV12 packet 137 input
func TestResamplerTV12Input(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 138)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create libopus decoder to get the exact input samples it would feed to resampler
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create gopus decoder
	goDec := silk.NewDecoder()

	// Decode packets 0-136 to build state
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		libDec.DecodeFloat(pkt, 1920)

		toc := parseSimpleTOC(pkt[0])
		if toc.mode == 0 { // SILK
			silkBW := toc.bw
			if silkBW <= 2 {
				goBW, _ := silk.BandwidthFromOpus(silkBW)
				goDec.Decode(pkt[1:], goBW, toc.frameSize, true)
			}
		}
	}

	// Now both decoders have identical state up to packet 136
	// Packet 137 is MB (12kHz) - first bandwidth change

	pkt := packets[137]
	toc := parseSimpleTOC(pkt[0])
	t.Logf("Packet 137: mode=%d, bw=%d, frameSize=%d", toc.mode, toc.bw, toc.frameSize)

	// Get libopus output for packet 137
	libOut, libSamples := libDec.DecodeFloat(pkt, 1920)
	t.Logf("Libopus output: %d samples", libSamples)

	// Get gopus output for packet 137
	goBW, _ := silk.BandwidthFromOpus(toc.bw)
	goOut, err := goDec.Decode(pkt[1:], goBW, toc.frameSize, true)
	if err != nil {
		t.Fatalf("Gopus decode error: %v", err)
	}
	t.Logf("Gopus output: %d samples", len(goOut))

	// Compare
	minLen := len(goOut)
	if libSamples < minLen {
		minLen = libSamples
	}

	var sumSqErr, sumSqSig float64
	var maxDiff float32
	maxDiffIdx := 0

	for i := 0; i < minLen; i++ {
		diff := goOut[i] - libOut[i]
		sumSqErr += float64(diff * diff)
		sumSqSig += float64(libOut[i] * libOut[i])
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

	t.Logf("SNR: %.1f dB, MaxDiff: %.6f at sample %d", snr, maxDiff, maxDiffIdx)

	// Show FIRST 20 samples
	t.Log("\nFirst 20 samples:")
	for i := 0; i < 20 && i < minLen; i++ {
		t.Logf("  [%3d] lib=%+.6f go=%+.6f diff=%+.6f", i, libOut[i], goOut[i], goOut[i]-libOut[i])
	}

	// Show samples around max diff
	t.Log("\nSamples around max diff:")
	start := maxDiffIdx - 5
	if start < 0 {
		start = 0
	}
	end := maxDiffIdx + 10
	if end > minLen {
		end = minLen
	}
	for i := start; i < end; i++ {
		marker := ""
		if i == maxDiffIdx {
			marker = " <-- MAX"
		}
		t.Logf("  [%3d] lib=%+.6f go=%+.6f diff=%+.6f%s", i, libOut[i], goOut[i], goOut[i]-libOut[i], marker)
	}
}

type simpleTOC struct {
	mode      int
	bw        int
	frameSize int
}

func parseSimpleTOC(toc byte) simpleTOC {
	config := int((toc >> 3) & 0x1F)
	var mode, bw, frameSize int

	switch {
	case config <= 11:
		mode = 0 // SILK
		switch {
		case config <= 3:
			bw = 0 // NB
		case config <= 7:
			bw = 1 // MB
		default:
			bw = 2 // WB
		}
		switch config & 3 {
		case 0:
			frameSize = 480
		case 1:
			frameSize = 960
		case 2:
			frameSize = 1920
		case 3:
			frameSize = 2880
		}
	case config <= 15:
		mode = 1 // Hybrid
		if config <= 13 {
			bw = 3 // SWB
		} else {
			bw = 4 // FB
		}
		if config&1 == 0 {
			frameSize = 480
		} else {
			frameSize = 960
		}
	default:
		mode = 2 // CELT
		bw = config - 16 + 3
		frameSize = 960
	}

	return simpleTOC{mode: mode, bw: bw, frameSize: frameSize}
}
