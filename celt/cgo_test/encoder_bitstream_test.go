//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides bitstream comparison tests for the gopus encoder.
// These tests compare what gopus encodes vs what libopus expects.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestCompareBitstreamWithLibopusEncoder encodes the same signal with both
// gopus and libopus, then compares the bitstreams.
func TestCompareBitstreamWithLibopusEncoder(t *testing.T) {
	// We can't easily encode with libopus in this test setup,
	// but we can look at what the decoder expects.

	frameSize := 960
	channels := 1

	// Generate a simple constant (DC) signal
	// This should be easy to encode and decode
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.3 // constant
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded DC signal: %d bytes", len(encoded))
	t.Logf("First 16 bytes: %02x", encoded[:minInt4(16, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Check if decoded is constant
	var decodedMean, decodedVar float64
	for _, s := range decoded {
		decodedMean += s
	}
	decodedMean /= float64(len(decoded))
	for _, s := range decoded {
		diff := s - decodedMean
		decodedVar += diff * diff
	}
	decodedVar /= float64(len(decoded))

	t.Logf("Decoded DC: mean=%.6f (expected %.6f), variance=%.10f", decodedMean, 0.3, decodedVar)

	// Also decode with libopus and see what it produces
	toc := byte(0xF8) // CELT fullband 20ms mono (config 31 = 0x1F, shifted left 3 = 0xF8)
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Check libopus decoded stats
	var libMean float64
	for i := 0; i < libSamples; i++ {
		libMean += float64(libDecoded[i])
	}
	libMean /= float64(libSamples)

	t.Logf("Libopus decoded DC: mean=%.10f (expected ~0.3)", libMean)

	// If libopus mean is near zero, the packet is being interpreted wrong
	if math.Abs(libMean) < 0.01 && math.Abs(decodedMean-0.3) < 0.1 {
		t.Log("ISSUE IDENTIFIED: gopus decoder works, libopus decoder doesn't!")
		t.Log("This indicates a bitstream format incompatibility.")
	}
}

// TestSilencePacketComparison creates a silence packet and compares.
func TestSilencePacketComparison(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate silence
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.0
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded silence: %d bytes", len(encoded))
	t.Logf("First 16 bytes: %02x", encoded[:minInt4(16, len(encoded))])

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Check decoded energy
	var decodedEnergy float64
	for _, s := range decoded {
		decodedEnergy += s * s
	}
	t.Logf("Gopus decoded silence energy: %.10f", decodedEnergy)

	// Decode with libopus
	toc := byte(0xF8) // CELT fullband 20ms mono (config 31 = 0x1F << 3)
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	var libEnergy float64
	for i := 0; i < libSamples; i++ {
		libEnergy += float64(libDecoded[i]) * float64(libDecoded[i])
	}
	t.Logf("Libopus decoded silence energy: %.10f", libEnergy)
}

// TestTraceMDCTEnergy traces where the energy goes in the MDCT coefficients.
func TestTraceMDCTEnergy(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a 440 Hz sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	t.Logf("Pre-emphasis output samples: %d", len(preemph))

	// Show pre-emphasis scaling effect
	var preemphMax float64
	for _, s := range preemph {
		if math.Abs(s) > preemphMax {
			preemphMax = math.Abs(s)
		}
	}
	t.Logf("Pre-emphasis max absolute value: %.4f (input max was %.4f)", preemphMax, amplitude)
	t.Logf("Amplification factor: %.2fx", preemphMax/amplitude)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)
	t.Logf("MDCT coefficients: %d", len(mdctCoeffs))

	// Find which MDCT bin corresponds to 440 Hz
	// MDCT bin k corresponds to frequency: k * sampleRate / (2 * N)
	// For 440 Hz: k = 440 * 2 * 960 / 48000 = 17.6
	expectedBin := int(freq * 2 * float64(frameSize) / 48000)
	t.Logf("440 Hz should be around bin %d", expectedBin)

	// Show MDCT bins around 440 Hz
	t.Log("MDCT bins around 440 Hz:")
	for bin := maxInt3(0, expectedBin-5); bin <= minInt4(len(mdctCoeffs)-1, expectedBin+5); bin++ {
		t.Logf("  Bin %d: %.6f", bin, mdctCoeffs[bin])
	}

	// Show which band this falls into
	mode := celt.GetModeConfig(frameSize)
	offset := 0
	for band := 0; band < mode.EffBands; band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		if offset <= expectedBin && expectedBin < offset+width {
			t.Logf("Bin %d is in band %d (offset=%d, width=%d)", expectedBin, band, offset, width)
			break
		}
		offset += width
	}
}

// TestCheckPreEmphasisScaling checks if pre-emphasis is scaling correctly.
func TestCheckPreEmphasisScaling(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a simple sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Input RMS
	var inputEnergy float64
	for _, s := range samples {
		inputEnergy += s * s
	}
	inputRMS := math.Sqrt(inputEnergy / float64(len(samples)))

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Pre-emphasis RMS
	var preemphEnergy float64
	for _, s := range preemph {
		preemphEnergy += s * s
	}
	preemphRMS := math.Sqrt(preemphEnergy / float64(len(preemph)))

	t.Logf("Input RMS: %.6f", inputRMS)
	t.Logf("Pre-emphasis RMS: %.6f", preemphRMS)
	t.Logf("Scaling factor: %.2fx", preemphRMS/inputRMS)

	// The libopus pre-emphasis typically doesn't change the magnitude this much
	// This huge amplification is suspicious
	if preemphRMS/inputRMS > 1000 {
		t.Log("WARNING: Pre-emphasis is amplifying by >1000x!")
		t.Log("This suggests the pre-emphasis scaling factor is wrong.")
	}
}

// TestComputeBandEnergyFormula checks the band energy computation formula.
func TestComputeBandEnergyFormula(t *testing.T) {
	frameSize := 960
	channels := 1

	// Create a signal where we know what the energy should be
	// Use DC (constant) which should have all energy in the first MDCT bin
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 1.0 // unit amplitude DC
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	// Manually compute band 0 energy
	mode := celt.GetModeConfig(frameSize)
	band0Width := celt.ScaledBandWidth(0, frameSize)

	var band0EnergyLinear float64
	for i := 0; i < band0Width; i++ {
		band0EnergyLinear += mdctCoeffs[i] * mdctCoeffs[i]
	}
	band0EnergyLinear = math.Sqrt(band0EnergyLinear) // sqrt of sum of squares

	// What the encoder computes
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Logf("Band 0 width: %d", band0Width)
	t.Logf("Band 0 linear energy (sqrt of sum of squares): %.6f", band0EnergyLinear)
	t.Logf("Band 0 encoder energy (log2 scale): %.6f", bandEnergies[0])

	// Convert log2 to linear
	// If energy is in log2 units of amplitude (not power), then:
	// linear = 2^logE
	linearFromLog := math.Exp2(bandEnergies[0])
	t.Logf("Linear from log2: %.6f", linearFromLog)

	// Check if they match
	if math.Abs(linearFromLog-band0EnergyLinear)/band0EnergyLinear > 0.1 {
		t.Logf("WARNING: Energy formula mismatch!")
		t.Logf("  Computed linear: %.6f", band0EnergyLinear)
		t.Logf("  From encoder: %.6f", linearFromLog)
	}
}

func minInt4(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt3(a, b int) int {
	if a > b {
		return a
	}
	return b
}
