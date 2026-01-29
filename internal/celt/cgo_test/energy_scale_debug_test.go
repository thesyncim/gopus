package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestEnergyScaleDebug(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Energy Scale Investigation ===")

	// Low-level CELT encoder
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetBitrate(64000)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Step 1: Pre-emphasis
	preemph := celtEnc.ApplyPreemphasis(pcm)
	t.Logf("Pre-emphasis: RMS=%.6f, max=%.6f", computeRMS(preemph), computeMax(preemph))

	// Step 2: MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)
	t.Logf("MDCT coeffs: RMS=%.6f, max=%.6f", computeRMS(mdctCoeffs), computeMax(mdctCoeffs))

	// Step 3: Compute band energies
	energies := celtEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Band energies (log dB):")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %d: %.4f dB (linear=%.6f)", i, energies[i], math.Pow(10, energies[i]/10))
	}

	// Full high-level encoder
	t.Log("")
	t.Log("=== High-level encoder ===")
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	gopusPacket, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("Gopus packet: %d bytes", len(gopusPacket))

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Decode both packets with libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	// Add TOC to gopus packet (CELT FB 20ms)
	toc := byte(0xF8) // CELT fullband 20ms
	gopusWithTOC := append([]byte{toc}, gopusPacket...)

	gopusDecoded, _ := libDec.DecodeFloat(gopusWithTOC, frameSize)

	// Fresh decoder for libopus packet
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()
	libDecoded, _ := libDec2.DecodeFloat(libPacket, frameSize)

	// Compare output levels
	t.Log("")
	t.Log("=== Decoded output levels ===")
	t.Logf("Original: RMS=%.6f", computeRMS(pcm))
	t.Logf("Gopus decoded: RMS=%.6f", computeRMS32(gopusDecoded[:frameSize]))
	t.Logf("Libopus decoded: RMS=%.6f", computeRMS32(libDecoded[:frameSize]))

	// Ratio analysis
	gopusRMS := computeRMS32(gopusDecoded[:frameSize])
	libRMS := computeRMS32(libDecoded[:frameSize])
	origRMS := computeRMS(pcm)

	t.Logf("Gopus/Original ratio: %.2f (%.1f dB)", gopusRMS/origRMS, 20*math.Log10(gopusRMS/origRMS))
	t.Logf("Libopus/Original ratio: %.2f (%.1f dB)", libRMS/origRMS, 20*math.Log10(libRMS/origRMS))

	// What energy difference would cause this?
	energyDiffDB := 20 * math.Log10(gopusRMS/origRMS)
	t.Logf("")
	t.Logf("Energy difference between gopus and original: %.1f dB", energyDiffDB)
	t.Logf("This suggests energies are off by about %.1f dB in encoding", energyDiffDB)
}

func computeRMS(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

func computeRMS32(x []float32) float64 {
	var sum float64
	for _, v := range x {
		sum += float64(v * v)
	}
	return math.Sqrt(sum / float64(len(x)))
}

func computeMax(x []float64) float64 {
	maxVal := 0.0
	for _, v := range x {
		if math.Abs(v) > maxVal {
			maxVal = math.Abs(v)
		}
	}
	return maxVal
}
