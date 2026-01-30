// Package cgo provides CGO tests for energy encoding divergence analysis.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestByte8DivergenceAnalysis analyzes the byte-by-byte divergence between gopus and libopus.
func TestByte8DivergenceAnalysis(t *testing.T) {
	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate test signal - 440 Hz sine
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm32[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	// Configure for CELT-only mode
	libEnc.SetComplexity(5)
	libEnc.SetBitrate(bitrate)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	// Get final range
	finalRange := libEnc.GetFinalRange()

	// Extract CELT payload (skip TOC byte)
	libPayload := libPacket[1:libLen]
	t.Logf("libopus CELT payload (%d bytes):", libLen-1)
	t.Logf("  Bytes 0-15: %02X", libPayload[:min8(16, len(libPayload))])
	t.Logf("  Final range: 0x%08X", finalRange)

	// Encode with gopus
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		pcm64[i] = float64(pcm32[i])
	}

	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(bitrate)

	gopusPayload, err := encoder.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("\ngopus CELT payload (%d bytes):", len(gopusPayload))
	t.Logf("  Bytes 0-15: %02X", gopusPayload[:min8(16, len(gopusPayload))])
	t.Logf("  Final range: 0x%08X", encoder.FinalRange())

	// Find first divergence
	minLen := len(gopusPayload)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if libPayload[i] != gopusPayload[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff >= 0 {
		t.Logf("\n=== DIVERGENCE at byte %d ===", firstDiff)
		t.Logf("  libopus: 0x%02X", libPayload[firstDiff])
		t.Logf("  gopus:   0x%02X", gopusPayload[firstDiff])

		// Show context
		start := firstDiff - 2
		if start < 0 {
			start = 0
		}
		end := firstDiff + 5
		if end > minLen {
			end = minLen
		}
		t.Logf("  Context libopus: %02X", libPayload[start:end])
		t.Logf("  Context gopus:   %02X", gopusPayload[start:end])
	} else {
		t.Log("\n=== PACKETS MATCH ===")
	}

	// Analyze packet structure
	t.Log("\n=== Packet Structure Analysis ===")
	t.Log("Byte 0-7: silence(1bit), postfilter(1bit), transient(3bit?), intra(3bit?), coarse energy")
	t.Log("Byte 8+: TF decisions, spread, fine energy, PVQ bands")

	// Decode both packets to verify
	t.Log("\n=== Decode Verification ===")

	// Decode gopus payload with libopus decoder
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	toc := byte(0xF8) // CELT-only mono 20ms
	fullPacket := append([]byte{toc}, gopusPayload...)
	libDecoded, decSamples := libDec.DecodeFloat(fullPacket, frameSize)

	if decSamples < 0 {
		t.Logf("libopus decode of gopus packet failed: %d", decSamples)
	} else {
		// Compute SNR
		var signalPower, noisePower float64
		for i := 0; i < frameSize; i++ {
			signalPower += float64(pcm32[i]) * float64(pcm32[i])
			diff := float64(pcm32[i]) - float64(libDecoded[i])
			noisePower += diff * diff
		}
		if noisePower > 0 {
			snr := 10 * math.Log10(signalPower/noisePower)
			t.Logf("Self-decode SNR (gopus->libopus): %.2f dB", snr)
		}

		// Compute correlation
		var sumXY, sumX2, sumY2 float64
		for i := 0; i < frameSize; i++ {
			x := float64(pcm32[i])
			y := float64(libDecoded[i])
			sumXY += x * y
			sumX2 += x * x
			sumY2 += y * y
		}
		correlation := sumXY / math.Sqrt(sumX2*sumY2)
		t.Logf("Correlation: %.4f", correlation)

		// Energy ratio
		energyRatio := sumY2 / sumX2
		t.Logf("Energy ratio (decoded/input): %.4f", energyRatio)
	}
}

// TestCoarseEnergyComparison2 compares coarse energy encoding between gopus and libopus.
func TestCoarseEnergyComparison2(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate test signal
	pcm := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	pcm64 := make([]float64, frameSize)
	for i := range pcm {
		pcm64[i] = float64(pcm[i])
	}

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Logf("Frame size: %d, LM: %d, nbBands: %d", frameSize, lm, nbBands)

	// Compute gopus energies
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	preemph := encoder.ApplyPreemphasisWithScaling(pcm64)
	mdct := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	energies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Log("\n=== Band Energies (gopus, mean-relative) ===")
	for i := 0; i < nbBands && i < 10; i++ {
		t.Logf("  Band %2d: %.6f", i, energies[i])
	}

	// Compute libopus-style energies
	mdctF32 := make([]float32, len(mdct))
	for i := range mdct {
		mdctF32[i] = float32(mdct[i])
	}
	libEnergies := ComputeLibopusBandEnergies(mdctF32, nbBands, frameSize, lm)

	t.Log("\n=== Band Energies (libopus-style, mean-relative) ===")
	for i := 0; i < nbBands && i < 10; i++ {
		diff := float64(libEnergies[i]) - energies[i]
		t.Logf("  Band %2d: %.6f (diff: %+.6f)", i, libEnergies[i], diff)
	}

	// Also show raw energies for verification
	libRawEnergies := ComputeLibopusBandEnergiesRaw(mdctF32, nbBands, frameSize, lm)
	t.Log("\n=== Band Energies (libopus raw, absolute log2) ===")
	for i := 0; i < 5; i++ {
		emeans := GetLibopusEMeans(i)
		t.Logf("  Band %2d: raw=%.6f, eMeans=%.6f, diff=%.6f", i, libRawEnergies[i], emeans, float64(libRawEnergies[i])-float64(emeans))
	}
}

// TestFullEncoderPipelineComparison2 does a step-by-step comparison.
func TestFullEncoderPipelineComparison2(t *testing.T) {
	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate test signal
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
		pcm32[i] = float32(pcm64[i])
	}

	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(bitrate)

	// Step through pipeline
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Logf("=== Encoder Pipeline Analysis ===")
	t.Logf("Frame: %d samples, LM=%d, nbBands=%d", frameSize, lm, nbBands)

	// 1. Pre-emphasis
	dcRejected := encoder.ApplyDCReject(pcm64)
	preemph := encoder.ApplyPreemphasisWithScaling(dcRejected)
	t.Logf("Pre-emphasis applied, first 5 values: [%.4f, %.4f, %.4f, %.4f, %.4f]",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])

	// 2. MDCT
	mdct := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	t.Logf("MDCT computed, %d coefficients", len(mdct))
	t.Logf("First 5 MDCT: [%.4f, %.4f, %.4f, %.4f, %.4f]",
		mdct[0], mdct[1], mdct[2], mdct[3], mdct[4])

	// 3. Band energies
	energies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)
	t.Logf("Band energies (first 5): [%.4f, %.4f, %.4f, %.4f, %.4f]",
		energies[0], energies[1], energies[2], energies[3], energies[4])

	// 4. Full encoding
	encoder.Reset()
	encoded, err := encoder.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("Encoding failed: %v", err)
	}

	t.Logf("\nEncoded: %d bytes", len(encoded))
	t.Logf("Bytes 0-15: %02X", encoded[:min8(16, len(encoded))])

	// Compare with libopus
	libEnc, _ := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:libLen]

	t.Logf("\nlibopus bytes 0-15: %02X", libPayload[:min8(16, len(libPayload))])

	// Show difference summary
	matches := 0
	for i := 0; i < min8(len(encoded), len(libPayload)); i++ {
		if encoded[i] == libPayload[i] {
			matches++
		}
	}
	t.Logf("\nMatching bytes: %d/%d", matches, min8(len(encoded), len(libPayload)))
}

func min8(a, b int) int {
	if a < b {
		return a
	}
	return b
}
