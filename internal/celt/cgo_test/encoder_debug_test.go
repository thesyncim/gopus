// Package cgo provides debugging tests for the gopus encoder.
// These tests investigate why Q ~ -100 (decoded audio doesn't match input).
package cgo

import (
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// eMeansLocal is a local copy of the eMeans table for test calculations
var eMeansLocal = [25]float64{
	6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
	4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
	4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
	4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
	3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
}

// DB6Local is 6dB in log2 units
const DB6Local = 1.0

// TestEncodeDecodeRoundtripDebug creates a minimal encode-decode roundtrip test
// to investigate why decoded audio doesn't match input.
func TestEncodeDecodeRoundtripDebug(t *testing.T) {
	sampleRate := 48000
	channels := 1
	frameSize := 960 // 20ms frame

	// Generate a simple sine wave
	samples := make([]float32, frameSize*channels)
	freq := 440.0
	amplitude := 0.5
	for i := 0; i < frameSize; i++ {
		tSec := float64(i) / float64(sampleRate)
		samples[i] = float32(amplitude * math.Sin(2*math.Pi*freq*tSec))
	}

	// Compute input energy and stats
	var inputEnergy float64
	var inputPeak float64
	for _, s := range samples {
		inputEnergy += float64(s) * float64(s)
		if math.Abs(float64(s)) > inputPeak {
			inputPeak = math.Abs(float64(s))
		}
	}
	inputRMS := math.Sqrt(inputEnergy / float64(len(samples)))
	t.Logf("Input: %d samples, RMS=%.6f, peak=%.6f, energy=%.6f", len(samples), inputRMS, inputPeak, inputEnergy)

	// Encode with gopus
	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(64000)
	enc.SetFrameSize(frameSize)

	data := make([]byte, 1275)
	n, err := enc.Encode(samples, data)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded: %d bytes", n)
	t.Logf("First 16 bytes: %02x", data[:minInt2(16, n)])

	// Decode with libopus
	dec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer dec.Destroy()

	decoded, samplesPerChannel := dec.DecodeFloat(data[:n], frameSize)
	if samplesPerChannel <= 0 {
		t.Fatalf("DecodeFloat failed: %d", samplesPerChannel)
	}
	t.Logf("Decoded: %d samples per channel (%d total)", samplesPerChannel, samplesPerChannel*channels)

	// Compute output energy and stats
	var outputEnergy float64
	var outputPeak float64
	outputSamples := decoded[:samplesPerChannel*channels]
	for _, s := range outputSamples {
		outputEnergy += float64(s) * float64(s)
		if math.Abs(float64(s)) > outputPeak {
			outputPeak = math.Abs(float64(s))
		}
	}
	outputRMS := math.Sqrt(outputEnergy / float64(len(outputSamples)))
	t.Logf("Output: %d samples, RMS=%.6f, peak=%.6f, energy=%.6f", len(outputSamples), outputRMS, outputPeak, outputEnergy)

	// Energy ratio
	if inputEnergy > 0 {
		energyRatio := outputEnergy / inputEnergy
		t.Logf("Energy ratio (output/input): %.6f (%.1f%%)", energyRatio, energyRatio*100)
	}

	// Compute correlation
	compareLen := minInt2(len(samples), len(outputSamples))
	var sumXY, sumXX, sumYY float64
	for i := 0; i < compareLen; i++ {
		x := float64(samples[i])
		y := float64(outputSamples[i])
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
	}
	if sumXX > 0 && sumYY > 0 {
		corr := sumXY / math.Sqrt(sumXX*sumYY)
		t.Logf("Correlation: %.6f", corr)
	}

	// Compute SNR
	var sigPow, noisePow float64
	for i := 0; i < compareLen; i++ {
		sig := float64(samples[i])
		noise := float64(outputSamples[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("SNR: %.2f dB", snr)

	// Show first few samples
	t.Log("\nFirst 20 samples comparison:")
	t.Log("Index\tInput\t\tOutput\t\tDiff")
	for i := 0; i < minInt2(20, compareLen); i++ {
		diff := outputSamples[i] - samples[i]
		t.Logf("%d\t%.6f\t%.6f\t%.6f", i, samples[i], outputSamples[i], diff)
	}
}

// TestEncoderMDCTCoefficientsDebug traces the MDCT coefficients through encoding.
func TestEncoderMDCTCoefficientsDebug(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate a simple DC signal (constant amplitude)
	// This should have energy concentrated in the lowest MDCT bin
	amplitude := 0.5
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = amplitude
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	t.Logf("Pre-emphasis output length: %d", len(preemph))

	// Show pre-emphasis output stats
	var preemphEnergy float64
	var preemphPeak float64
	for _, s := range preemph {
		preemphEnergy += s * s
		if math.Abs(s) > preemphPeak {
			preemphPeak = math.Abs(s)
		}
	}
	t.Logf("Pre-emphasis: peak=%.2f, energy=%.6f", preemphPeak, preemphEnergy)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)
	t.Logf("MDCT coefficients: %d", len(mdctCoeffs))

	// Show MDCT coefficient stats
	var mdctEnergy float64
	var mdctPeak float64
	mdctPeakIdx := 0
	for i, c := range mdctCoeffs {
		mdctEnergy += c * c
		if math.Abs(c) > mdctPeak {
			mdctPeak = math.Abs(c)
			mdctPeakIdx = i
		}
	}
	t.Logf("MDCT: peak=%.6f at bin %d, energy=%.6f", mdctPeak, mdctPeakIdx, mdctEnergy)

	// Show first 10 MDCT coefficients
	t.Log("\nFirst 10 MDCT coefficients:")
	for i := 0; i < minInt2(10, len(mdctCoeffs)); i++ {
		t.Logf("  [%d] = %.6f", i, mdctCoeffs[i])
	}

	// Compute band energies
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	energies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("\nBand energies (%d bands):", nbBands)
	for band := 0; band < minInt2(10, nbBands); band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		t.Logf("  Band %d: energy=%.4f (width=%d bins)", band, energies[band], width)
	}

	// Now encode a full frame and see what happens
	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("\nEncoded frame: %d bytes", len(encoded))

	// Decode with libopus and compare
	dec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer dec.Destroy()

	// Create TOC byte for CELT fullband 20ms mono
	toc := byte(0x78) // CELT 20ms FB mono
	packet := append([]byte{toc}, encoded...)

	decoded, samplesDecoded := dec.DecodeFloat(packet, frameSize)
	if samplesDecoded <= 0 {
		t.Fatalf("DecodeFloat failed: %d", samplesDecoded)
	}

	// Compute decoded stats
	var decodedEnergy float64
	var decodedPeak float64
	for i := 0; i < samplesDecoded; i++ {
		s := decoded[i]
		decodedEnergy += float64(s) * float64(s)
		if math.Abs(float64(s)) > decodedPeak {
			decodedPeak = math.Abs(float64(s))
		}
	}
	t.Logf("\nDecoded: %d samples, peak=%.6f, energy=%.6f", samplesDecoded, decodedPeak, decodedEnergy)

	// Compare input and output
	var sumXY, sumXX, sumYY float64
	for i := 0; i < minInt2(frameSize, samplesDecoded); i++ {
		x := samples[i]
		y := float64(decoded[i])
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
	}
	if sumXX > 0 && sumYY > 0 {
		corr := sumXY / math.Sqrt(sumXX*sumYY)
		t.Logf("Correlation: %.6f", corr)
	}
}

// TestEncoderVsDecoderNormalization checks if normalization/denormalization is symmetric.
func TestEncoderVsDecoderNormalization(t *testing.T) {
	frameSize := 960
	channels := 1

	// Create a test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Compute original band energies
	origEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("Original band energies (log2 scale):")
	for band := 0; band < minInt2(10, nbBands); band++ {
		t.Logf("  Band %d: %.4f", band, origEnergies[band])
	}

	// Normalize the coefficients using original energies
	normalized := encoder.NormalizeBandsToArray(mdctCoeffs, origEnergies, nbBands, frameSize)

	// Check normalized coefficients have unit energy per band
	t.Log("\nNormalized band energies (should be ~1.0):")
	offset := 0
	for band := 0; band < minInt2(10, nbBands); band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		var bandEnergy float64
		for i := 0; i < width && offset+i < len(normalized); i++ {
			bandEnergy += normalized[offset+i] * normalized[offset+i]
		}
		t.Logf("  Band %d: L2 norm = %.6f", band, math.Sqrt(bandEnergy))
		offset += width
	}

	// Now check what happens with denormalization
	// Simulate decoder: denormalize using the same energies
	denormalized := make([]float64, len(normalized))
	offset = 0
	for band := 0; band < nbBands; band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		if offset+width > len(normalized) {
			break
		}

		// Compute gain same way decoder does
		e := origEnergies[band]
		if band < len(eMeansLocal) {
			e += eMeansLocal[band] * DB6Local
		}
		if e > 32*DB6Local {
			e = 32 * DB6Local
		}
		gain := math.Exp2(e / DB6Local)

		for i := 0; i < width; i++ {
			denormalized[offset+i] = normalized[offset+i] * gain
		}
		offset += width
	}

	// Compare original MDCT with denormalized
	var maxDiff float64
	maxDiffIdx := 0
	for i := 0; i < minInt2(len(mdctCoeffs), len(denormalized)); i++ {
		diff := math.Abs(mdctCoeffs[i] - denormalized[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}

	t.Logf("\nNormalize->Denormalize roundtrip:")
	t.Logf("  Max difference: %.6f at index %d", maxDiff, maxDiffIdx)
	if maxDiff < 1e-10 {
		t.Log("  GOOD: Normalization is reversible")
	} else {
		t.Log("  WARNING: Normalization has significant error")
		// Show some examples
		t.Log("\nFirst 10 coefficients comparison:")
		for i := 0; i < minInt2(10, len(mdctCoeffs)); i++ {
			t.Logf("  [%d] orig=%.6f, denorm=%.6f, diff=%.6f",
				i, mdctCoeffs[i], denormalized[i], mdctCoeffs[i]-denormalized[i])
		}
	}
}

// TestEnergyEncodingRoundtrip tests if energy encoding/decoding is consistent.
func TestEnergyEncodingRoundtrip(t *testing.T) {
	frameSize := 960
	channels := 1

	// Create a test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Apply pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Compute band energies
	origEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("Original energies (log2 scale):")
	for band := 0; band < minInt2(10, nbBands); band++ {
		t.Logf("  Band %d: %.4f", band, origEnergies[band])
	}

	// Encode energies using coarse energy encoding
	// (This is what would be written to the bitstream)
	// For this test, we simulate what would happen
	t.Log("\n(Energy encoding test would require range coder simulation)")

	// For now, let's check if the energy magnitudes are reasonable
	t.Log("\nEnergy magnitudes check:")
	for band := 0; band < minInt2(10, nbBands); band++ {
		e := origEnergies[band]
		if band < len(eMeansLocal) {
			e += eMeansLocal[band] * DB6Local
		}
		gain := math.Exp2(e / DB6Local)
		t.Logf("  Band %d: logE=%.2f, gain=%.4f", band, e, gain)
	}
}

// TestFullEncodingPathDebug traces through the complete encoding path.
func TestFullEncodingPathDebug(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate 1 second of test signal (multiple frames)
	duration := 1.0 // seconds
	totalSamples := int(duration * float64(sampleRate))
	samples := make([]float32, totalSamples*channels)

	// Simple sine wave at 440 Hz
	for i := 0; i < totalSamples; i++ {
		tSec := float64(i) / float64(sampleRate)
		samples[i] = float32(0.5 * math.Sin(2*math.Pi*440*tSec))
	}

	// Create gopus encoder
	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewEncoder failed: %v", err)
	}
	enc.SetBitrate(64000)
	enc.SetFrameSize(frameSize)

	// Encode all frames
	var packets [][]byte
	var totalBytes int
	numFrames := totalSamples / frameSize

	for frame := 0; frame < numFrames; frame++ {
		start := frame * frameSize * channels
		end := start + frameSize*channels
		data := make([]byte, 1275)

		n, err := enc.Encode(samples[start:end], data)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", frame, err)
		}
		packets = append(packets, data[:n])
		totalBytes += n
	}

	t.Logf("Encoded %d frames, total %d bytes (avg %.1f bytes/frame)",
		len(packets), totalBytes, float64(totalBytes)/float64(len(packets)))

	// Decode all frames with libopus
	dec, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer dec.Destroy()

	var decoded []float32
	for i, pkt := range packets {
		out, samplesDecoded := dec.DecodeFloat(pkt, frameSize)
		if samplesDecoded <= 0 {
			t.Fatalf("Decode frame %d failed: %d", i, samplesDecoded)
		}
		decoded = append(decoded, out[:samplesDecoded*channels]...)
	}
	t.Logf("Decoded: %d samples", len(decoded))

	// Skip pre-skip (312 samples)
	preSkip := 312 * channels
	if len(decoded) > preSkip {
		decoded = decoded[preSkip:]
	}

	// Compute SNR and correlation for the entire stream
	compareLen := minInt2(len(samples), len(decoded))

	var sumXY, sumXX, sumYY float64
	var sigPow, noisePow float64
	for i := 0; i < compareLen; i++ {
		x := float64(samples[i])
		y := float64(decoded[i])
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
		sigPow += x * x
		noisePow += (y - x) * (y - x)
	}

	corr := sumXY / math.Sqrt(sumXX*sumYY)
	snr := 10 * math.Log10(sigPow/noisePow)

	t.Logf("Overall correlation: %.6f", corr)
	t.Logf("Overall SNR: %.2f dB", snr)

	// Show per-frame statistics
	t.Log("\nPer-frame SNR (first 10 frames):")
	for frame := 0; frame < minInt2(10, numFrames); frame++ {
		start := frame * frameSize * channels
		end := minInt2(start+frameSize*channels, len(samples))
		if end > len(decoded)+preSkip {
			end = len(decoded) + preSkip
		}

		var frameSig, frameNoise float64
		for i := start; i < end && i-preSkip < len(decoded); i++ {
			if i-preSkip < 0 {
				continue
			}
			x := float64(samples[i])
			y := float64(decoded[i-preSkip])
			frameSig += x * x
			frameNoise += (y - x) * (y - x)
		}

		frameSNR := 10 * math.Log10(frameSig/frameNoise)
		t.Logf("  Frame %d: SNR=%.2f dB", frame, frameSNR)
	}
}

// TestCELTEncoderDirectDebug tests the CELT encoder directly without the Opus wrapper.
func TestCELTEncoderDirectDebug(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	// Create CELT encoder directly
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Encode
	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("CELT encoded: %d bytes", len(encoded))

	// Create CELT decoder directly
	decoder := celt.NewDecoder(channels)

	// Decode
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}
	t.Logf("CELT decoded: %d samples", len(decoded))

	// Compute comparison metrics
	compareLen := minInt2(frameSize, len(decoded))
	var sumXY, sumXX, sumYY float64
	var sigPow, noisePow float64
	for i := 0; i < compareLen; i++ {
		x := samples[i]
		y := decoded[i]
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
		sigPow += x * x
		noisePow += (y - x) * (y - x)
	}

	corr := sumXY / math.Sqrt(sumXX*sumYY)
	snr := 10 * math.Log10(sigPow/noisePow)

	t.Logf("Correlation (CELT internal): %.6f", corr)
	t.Logf("SNR (CELT internal): %.2f dB", snr)

	// Show sample comparison
	t.Log("\nFirst 20 samples:")
	for i := 0; i < minInt2(20, compareLen); i++ {
		t.Logf("  [%d] in=%.6f, out=%.6f, diff=%.6f", i, samples[i], decoded[i], decoded[i]-samples[i])
	}
}

// TestCompareGoDecoderVsLibopusDecoder compares the two decoders on gopus-encoded packets.
func TestCompareGoDecoderVsLibopusDecoder(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	// Create CELT encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Encode
	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded: %d bytes", len(encoded))
	t.Logf("First 16 bytes: %02x", encoded[:minInt2(16, len(encoded))])

	// Decode with gopus CELT decoder
	goDecoder := celt.NewDecoder(channels)
	goDecoded, err := goDecoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Go DecodeFrame failed: %v", err)
	}
	t.Logf("Go decoded: %d samples", len(goDecoded))

	// Decode with libopus (need to add TOC byte)
	// CELT fullband 20ms mono = 0x78
	toc := byte(0x78)
	packet := append([]byte{toc}, encoded...)

	libDecoder, err := NewLibopusDecoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDecoder.Destroy()

	libDecoded, libSamples := libDecoder.DecodeFloat(packet, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}
	t.Logf("libopus decoded: %d samples", libSamples)

	// Compare the two decoder outputs
	t.Log("\nComparing gopus vs libopus decoder outputs:")

	compareLen := minInt2(len(goDecoded), libSamples)
	var goLibSig, goLibNoise float64
	var maxDiff float64
	maxDiffIdx := 0

	for i := 0; i < compareLen; i++ {
		goVal := goDecoded[i]
		libVal := float64(libDecoded[i])

		diff := math.Abs(goVal - libVal)
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}

		goLibSig += libVal * libVal
		goLibNoise += (goVal - libVal) * (goVal - libVal)
	}

	goLibSNR := 10 * math.Log10(goLibSig/goLibNoise)
	t.Logf("Go vs Lib decoder SNR: %.2f dB", goLibSNR)
	t.Logf("Max diff: %.6f at index %d", maxDiff, maxDiffIdx)

	// Now compare each decoder's output vs original input
	var goInputSig, goInputNoise float64
	var libInputSig, libInputNoise float64

	for i := 0; i < minInt2(frameSize, compareLen); i++ {
		orig := samples[i]
		goVal := goDecoded[i]
		libVal := float64(libDecoded[i])

		goInputSig += orig * orig
		goInputNoise += (goVal - orig) * (goVal - orig)

		libInputSig += orig * orig
		libInputNoise += (libVal - orig) * (libVal - orig)
	}

	goInputSNR := 10 * math.Log10(goInputSig/goInputNoise)
	libInputSNR := 10 * math.Log10(libInputSig/libInputNoise)

	t.Logf("Go decoder vs original: %.2f dB", goInputSNR)
	t.Logf("Lib decoder vs original: %.2f dB", libInputSNR)

	// Show first few samples
	t.Log("\nFirst 10 samples comparison:")
	t.Log("Index\tOriginal\tGo\t\tLib\t\tGo-Lib")
	for i := 0; i < minInt2(10, compareLen); i++ {
		orig := samples[i]
		goVal := goDecoded[i]
		libVal := libDecoded[i]
		t.Logf("%d\t%.6f\t%.6f\t%.6f\t%.6f",
			i, orig, goVal, libVal, goVal-float64(libVal))
	}
}

// minInt is a local min function (to avoid redeclaration with other test files)
func minInt2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
