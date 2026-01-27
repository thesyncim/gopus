// Package cgo provides tracing tests for the gopus encoder.
// These tests trace the signal through each encoding stage to find where it gets destroyed.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestEncodeEnergyTrace traces the energy values through encoding.
func TestEncodeEnergyTrace(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Compute input energy
	var inputEnergy float64
	for _, s := range samples {
		inputEnergy += s * s
	}
	inputRMS := math.Sqrt(inputEnergy / float64(len(samples)))
	t.Logf("INPUT: %d samples, RMS=%.6f, energy=%.6f", len(samples), inputRMS, inputEnergy)

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Step 1: Pre-emphasis
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	var preemphEnergy float64
	for _, s := range preemph {
		preemphEnergy += s * s
	}
	preemphRMS := math.Sqrt(preemphEnergy / float64(len(preemph)))
	t.Logf("PRE-EMPHASIS: %d samples, RMS=%.6f, energy=%.6f", len(preemph), preemphRMS, preemphEnergy)

	// Step 2: MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)
	var mdctEnergy float64
	for _, c := range mdctCoeffs {
		mdctEnergy += c * c
	}
	mdctRMS := math.Sqrt(mdctEnergy / float64(len(mdctCoeffs)))
	t.Logf("MDCT: %d coeffs, RMS=%.6f, energy=%.6f", len(mdctCoeffs), mdctRMS, mdctEnergy)

	// Step 3: Band energies
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Logf("\nBAND ENERGIES (log2 scale, %d bands):", nbBands)
	for band := 0; band < nbBands; band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		t.Logf("  Band %d: logE=%.4f width=%d", band, bandEnergies[band], width)
	}

	// Check what the decoder would interpret these as
	t.Logf("\nDECODER INTERPRETATION (gain = 2^((logE + eMeans)*DB6)):")
	var eMeans = [25]float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	for band := 0; band < minInt3(10, nbBands); band++ {
		e := bandEnergies[band]
		if band < len(eMeans) {
			e += eMeans[band] * 1.0 // DB6 = 1.0
		}
		gain := math.Exp2(e)
		t.Logf("  Band %d: e=%.4f, gain=%.6f", band, e, gain)
	}
}

// TestEncodeNormalizationTrace traces the normalization process.
func TestEncodeNormalizationTrace(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Pre-emphasis and MDCT
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	// Band energies
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize bands
	normalized := encoder.NormalizeBands(mdctCoeffs, bandEnergies, nbBands, frameSize)

	t.Log("NORMALIZED BAND SHAPES:")
	for band := 0; band < minInt3(5, nbBands); band++ {
		shape := normalized[band]
		if len(shape) == 0 {
			continue
		}

		// Compute L2 norm
		var l2norm float64
		for _, v := range shape {
			l2norm += v * v
		}
		l2norm = math.Sqrt(l2norm)

		t.Logf("  Band %d: len=%d, L2norm=%.6f", band, len(shape), l2norm)
		if len(shape) <= 10 {
			t.Logf("    values: %v", shape)
		} else {
			t.Logf("    first 5: %v ...", shape[:5])
		}
	}

	// Test roundtrip: normalize then denormalize
	t.Log("\nROUNDTRIP TEST (normalize -> denormalize):")

	var eMeans = [25]float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	offset := 0
	for band := 0; band < minInt3(5, nbBands); band++ {
		width := celt.ScaledBandWidth(band, frameSize)
		if offset+width > len(mdctCoeffs) {
			break
		}

		shape := normalized[band]
		if len(shape) == 0 || len(shape) != width {
			offset += width
			continue
		}

		// Compute gain the same way decoder does
		e := bandEnergies[band]
		if band < len(eMeans) {
			e += eMeans[band]
		}
		if e > 32 {
			e = 32
		}
		gain := math.Exp2(e)

		// Denormalize
		denorm := make([]float64, width)
		for i := 0; i < width; i++ {
			denorm[i] = shape[i] * gain
		}

		// Compare with original
		var maxDiff float64
		for i := 0; i < width; i++ {
			diff := math.Abs(mdctCoeffs[offset+i] - denorm[i])
			if diff > maxDiff {
				maxDiff = diff
			}
		}

		t.Logf("  Band %d: origMax=%.6f, denormMax=%.6f, maxDiff=%.6f",
			band, maxInSlice(mdctCoeffs[offset:offset+width]), maxInSlice(denorm), maxDiff)

		offset += width
	}
}

// TestEncodeVsPVQ tests what happens in PVQ encoding.
func TestEncodeVsPVQ(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	// Pre-emphasis and MDCT
	preemph := encoder.ApplyPreemphasisWithScaling(samples)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)

	// Band energies
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	bandEnergies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize bands
	normalized := encoder.NormalizeBands(mdctCoeffs, bandEnergies, nbBands, frameSize)

	t.Log("PVQ ENCODING ANALYSIS:")

	// Test PVQ on band 0
	band := 0
	shape := normalized[band]
	width := celt.ScaledBandWidth(band, frameSize)
	t.Logf("Band %d: width=%d, shape len=%d", band, width, len(shape))

	if len(shape) > 0 {
		// Convert to pulses with different K values
		for _, k := range []int{2, 4, 8, 16, 32} {
			pulses := vectorToPulsesLocal(shape, k)

			// Compute L1 norm of pulses
			var l1 int
			for _, p := range pulses {
				if p < 0 {
					l1 -= p
				} else {
					l1 += p
				}
			}

			// Reconstruct from pulses
			recon := pulsesToVectorLocal(pulses)

			// Compute correlation with original shape
			var sumXY, sumXX, sumYY float64
			for i := 0; i < len(shape); i++ {
				x := shape[i]
				y := recon[i]
				sumXY += x * y
				sumXX += x * x
				sumYY += y * y
			}
			corr := sumXY / math.Sqrt(sumXX*sumYY)

			t.Logf("  K=%d: L1=%d, correlation=%.6f", k, l1, corr)
		}
	}
}

// TestCheckEncoderEnergyOutput checks the actual encoded energy values.
func TestCheckEncoderEnergyOutput(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Compute input stats
	var inputEnergy float64
	for _, s := range samples {
		inputEnergy += s * s
	}
	t.Logf("Input signal: amplitude=%.2f, total_energy=%.6f", amplitude, inputEnergy)

	// Encode the frame
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded: %d bytes", len(encoded))

	// Decode with gopus decoder
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Compute output stats
	var outputEnergy float64
	for _, s := range decoded {
		outputEnergy += s * s
	}
	t.Logf("Output: total_energy=%.6f", outputEnergy)
	t.Logf("Energy ratio (output/input): %.6f", outputEnergy/inputEnergy)

	// Show correlation
	var sumXY, sumXX, sumYY float64
	for i := 0; i < minInt3(frameSize, len(decoded)); i++ {
		x := samples[i]
		y := decoded[i]
		sumXY += x * y
		sumXX += x * x
		sumYY += y * y
	}
	corr := sumXY / math.Sqrt(sumXX*sumYY)
	t.Logf("Correlation: %.6f", corr)

	// If correlation is negative, the signal is inverted
	if corr < -0.5 {
		t.Log("WARNING: Negative correlation suggests signal inversion!")
	} else if corr < 0.1 {
		t.Log("WARNING: Near-zero correlation suggests uncorrelated output!")
	}
}

// TestTraceEncodedPacketStructure examines what's in the encoded packet.
func TestTraceEncodedPacketStructure(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/48000)
	}

	// Encode the frame
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)

	encoded, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}

	t.Logf("Encoded packet: %d bytes", len(encoded))
	t.Logf("First 32 bytes: %02x", encoded[:minInt3(32, len(encoded))])

	// Parse the first byte (which contains flags)
	// Bit 0: silence flag
	// Bit 1: postfilter flag
	// Bit 2: transient flag
	// Bit 3: intra flag
	if len(encoded) > 0 {
		firstByte := encoded[0]
		silenceFlag := firstByte & 0x01
		postfilterFlag := (firstByte >> 1) & 0x01
		// These are just guesses based on typical CELT structure

		t.Logf("First byte (0x%02x): silence=%d, postfilter=%d",
			firstByte, silenceFlag, postfilterFlag)
	}

	// Now decode and trace what the decoder sees
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Compute decoded energy
	var energy float64
	for _, s := range decoded {
		energy += s * s
	}
	t.Logf("Decoded energy: %.10f", energy)

	// If energy is tiny, the decoder is essentially outputting silence
	if energy < 0.0001 {
		t.Log("WARNING: Decoded output has essentially zero energy!")
		t.Log("This suggests the encoded energies are wrong or the packet is corrupted")
	}
}

func minInt3(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInSlice(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	m := math.Abs(s[0])
	for _, v := range s[1:] {
		if math.Abs(v) > m {
			m = math.Abs(v)
		}
	}
	return m
}

// vectorToPulsesLocal converts a normalized float vector to an integer pulse vector.
func vectorToPulsesLocal(shape []float64, k int) []int {
	n := len(shape)
	if n == 0 || k <= 0 {
		return make([]int, n)
	}

	pulses := make([]int, n)

	// Compute L1 norm of shape
	var l1norm float64
	for _, x := range shape {
		l1norm += math.Abs(x)
	}

	if l1norm < 1e-15 {
		pulses[0] = k
		return pulses
	}

	// Scale to make L1 norm = k
	scale := float64(k) / l1norm

	currentL1 := 0
	for i, x := range shape {
		scaled := x * scale
		sign := 1
		if scaled < 0 {
			sign = -1
			scaled = -scaled
		}
		rounded := int(math.Floor(scaled + 0.5))
		if rounded < 0 {
			rounded = 0
		}
		pulses[i] = sign * rounded
		currentL1 += rounded
	}

	// Adjust to get exactly k pulses
	for currentL1 < k {
		bestIdx := 0
		for i := range pulses {
			if pulses[i] >= 0 {
				pulses[i]++
			} else {
				pulses[i]--
			}
			currentL1++
			if currentL1 >= k {
				break
			}
		}
		_ = bestIdx
	}

	return pulses
}

// pulsesToVectorLocal converts pulses back to a normalized vector.
func pulsesToVectorLocal(pulses []int) []float64 {
	n := len(pulses)
	v := make([]float64, n)

	var l1 float64
	for i, p := range pulses {
		v[i] = float64(p)
		if p < 0 {
			l1 -= float64(p)
		} else {
			l1 += float64(p)
		}
	}

	// Normalize to L2 unit norm
	if l1 > 0 {
		var l2 float64
		for _, x := range v {
			l2 += x * x
		}
		l2 = math.Sqrt(l2)
		if l2 > 0 {
			for i := range v {
				v[i] /= l2
			}
		}
	}

	return v
}
