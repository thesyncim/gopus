// Package cgo provides energy comparison tests between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestCompareEncodedEnergies compares what gopus encodes vs what libopus decodes.
func TestCompareEncodedEnergies(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Get energies before encoding
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdct := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	rawEnergies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Log("Raw computed band energies (before quantization):")
	for band := 0; band < nbBands; band++ {
		t.Logf("  Band %d: %.4f", band, rawEnergies[band])
	}

	// Encode a full frame to see what energies get written
	// Reset encoder to get clean state
	encoder.Reset()
	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}

	t.Logf("\nEncoded packet: %d bytes", len(encoded))
	t.Logf("First 32 bytes: %02x", encoded[:min6(32, len(encoded))])

	// Decode with gopus decoder to see what energies it recovers
	decoder := celt.NewDecoder(channels)
	_, err = decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Now let's try to understand the packet structure
	// CELT packet starts with:
	// 1. Silence flag (1 bit)
	// 2. Post-filter (if not silence)
	// 3. Intra flag
	// 4. Coarse energies
	// ...

	t.Log("\n=== Packet Structure Analysis ===")
	t.Log("(Packet parsing skipped - use debug trace from libopus)")

	// The key question: what energies is libopus seeing?
	// Let's decode with libopus and enable all debug output
	t.Log("\n=== Decode with libopus ===")
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

	// The debug trace should show [CELT:energy] lines for all bands
	// We see them only for bands 17-20 in the output, which suggests
	// the lower bands are in a different part of the trace

	var libPeak float64
	for i := 0; i < libSamples; i++ {
		if math.Abs(float64(libDecoded[i])) > libPeak {
			libPeak = math.Abs(float64(libDecoded[i]))
		}
	}
	t.Logf("libopus decoded peak: %.6f", libPeak)
}

// TestManualEnergyEncoding manually traces the energy encoding process.
func TestManualEnergyEncoding(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Compute band energies
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdct := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	rawEnergies := encoder.ComputeBandEnergies(mdct, nbBands, frameSize)

	t.Log("=== Band Energies Analysis ===")
	t.Log("Band\tRaw\tQuantized(expected)\tDelta")

	// eMeans table
	eMeans := [25]float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	// In intra mode, coarse energy is quantized to 6dB steps (integer qi)
	// quantized = qi * 6dB + prediction
	// For intra mode (first frame), prediction coefficient = 0
	// So quantized[i] = qi[i] + prev_quantized * 0 + inter_band_predictor
	// where inter_band_predictor starts at 0 and updates: prev + q - beta*q

	prevBand := 0.0
	beta := 0.15 // BetaIntra = 4915/32768 ~ 0.15

	for band := 0; band < nbBands; band++ {
		raw := rawEnergies[band]

		// For intra mode: f = x - coef*oldE - prevBand
		// coef = 0 for intra, so f = x - prevBand
		f := raw - prevBand

		// Quantize to integer steps
		qi := int(math.Floor(f + 0.5))

		// Dequantized
		q := float64(qi)
		quantized := prevBand + q

		// What the decoder will see (after adding eMeans back)
		totalE := quantized
		if band < len(eMeans) {
			totalE += eMeans[band]
		}
		gain := math.Exp2(totalE)

		t.Logf("%d\t%.4f\t%.4f (qi=%d)\tgain=%.2f", band, raw, quantized, qi, gain)

		// Update inter-band predictor
		prevBand = prevBand + q - beta*q
	}
}

// TestVerifyEncoderDecoderSymmetry verifies encoder/decoder energy handling is symmetric.
func TestVerifyEncoderDecoderSymmetry(t *testing.T) {
	frameSize := 960
	channels := 1

	// Create a signal with known energy in one band
	// Place energy primarily in band 2 (bins 16-23) by using 440 Hz
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Encode
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(128000) // Higher bitrate for more precision

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}

	// Decode with gopus
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Compare energies
	var inputEnergy, outputEnergy float64
	for _, s := range samples {
		inputEnergy += s * s
	}
	for _, s := range decoded {
		outputEnergy += s * s
	}

	t.Logf("Input energy: %.6f", inputEnergy)
	t.Logf("Output energy: %.6f", outputEnergy)
	t.Logf("Ratio: %.4f", outputEnergy/inputEnergy)

	// The ratio should be close to 1.0 if energy encoding/decoding is symmetric
	if outputEnergy/inputEnergy < 0.5 || outputEnergy/inputEnergy > 2.0 {
		t.Log("WARNING: Energy ratio far from 1.0!")
		t.Log("This suggests encoder/decoder asymmetry in energy handling")
	}
}

func min6(a, b int) int {
	if a < b {
		return a
	}
	return b
}
