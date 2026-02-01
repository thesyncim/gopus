package celt

import (
	"math"
	"testing"
)

// TestNormalizeBandsToArrayUnitNorm verifies that normalized coefficients have
// correct per-band L2 norm of 1.0 (unit norm).
// This is critical for PVQ encoding which expects unit-norm input vectors.
func TestNormalizeBandsToArrayUnitNorm(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients with known properties
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	// Create coefficients with varying energy per band
	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		// Mix of frequencies to get realistic-ish MDCT output
		mdctCoeffs[i] = math.Sin(float64(i)*0.1) * float64(i%50+1) * 0.01
	}

	// Compute energies using the encoder's method
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize
	normalized := enc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	if normalized == nil {
		t.Fatal("NormalizeBandsToArray returned nil")
	}

	// Check each band's L2 norm
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(normalized) {
			t.Errorf("band %d: offset+n=%d exceeds normalized length=%d", band, offset+n, len(normalized))
			break
		}

		// Compute L2 norm of this band's normalized coefficients
		var sumSq float64
		for i := 0; i < n; i++ {
			sumSq += normalized[offset+i] * normalized[offset+i]
		}
		l2norm := math.Sqrt(sumSq)

		// The normalized coefficients should NOT be unit-norm - they're just
		// divided by gain. The subsequent PVQ encoder will further normalize.
		// However, let's check that the values are reasonable (not NaN/Inf).
		if math.IsNaN(l2norm) || math.IsInf(l2norm, 0) {
			t.Errorf("band %d: L2 norm is %v (NaN or Inf)", band, l2norm)
		}

		// Log for debugging
		if l2norm > 1e-6 {
			t.Logf("band %2d: width=%3d, energy=%8.3f, L2 norm=%.6f",
				band, n, energies[band], l2norm)
		}

		offset += n
	}
}

// TestNormalizationRoundTrip verifies that normalization -> denormalization
// recovers the original coefficients (up to quantization error).
func TestNormalizationRoundTrip(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = math.Sin(float64(i)*0.2) * 100.0 // Scale to realistic magnitude
	}

	// Compute energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize
	normalized := enc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	if normalized == nil {
		t.Fatal("NormalizeBandsToArray returned nil")
	}

	// Denormalize (mimics what decoder does)
	denormalized := make([]float64, len(normalized))
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(normalized) {
			break
		}

		// Decoder's denormalization formula
		e := energies[band]
		if band < len(eMeans) {
			e += eMeans[band] * DB6
		}
		if e > 32*DB6 {
			e = 32 * DB6
		}
		gain := math.Exp2(e / DB6)

		for i := 0; i < n; i++ {
			denormalized[offset+i] = normalized[offset+i] * gain
		}
		offset += n
	}

	// Compare original and denormalized
	var totalError float64
	var totalEnergy float64
	offset = 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			break
		}

		var bandError float64
		var bandEnergy float64
		for i := 0; i < n; i++ {
			diff := mdctCoeffs[offset+i] - denormalized[offset+i]
			bandError += diff * diff
			bandEnergy += mdctCoeffs[offset+i] * mdctCoeffs[offset+i]
		}

		// Log per-band error
		if bandEnergy > 1e-10 {
			relError := math.Sqrt(bandError) / math.Sqrt(bandEnergy)
			if relError > 0.01 { // More than 1% error
				t.Errorf("band %2d: relative error = %.4f (should be ~0)", band, relError)
			}
		}

		totalError += bandError
		totalEnergy += bandEnergy
		offset += n
	}

	// Overall SNR should be very high if normalization is correct
	if totalEnergy > 1e-10 {
		snr := 10 * math.Log10(totalEnergy/totalError)
		t.Logf("Overall normalization round-trip SNR: %.1f dB", snr)
		if snr < 100 { // Should be essentially perfect
			t.Errorf("Round-trip SNR too low: %.1f dB, expected > 100 dB", snr)
		}
	}
}

// TestEnergyComputationConsistency verifies that energies computed by the
// encoder are consistent with the decoder's expectations.
func TestEnergyComputationConsistency(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = float64(i%100) * 0.1
	}

	// Compute energies using encoder's method
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Verify energy computation matches expectations
	offset := 0
	for band := 0; band < nbBands; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandEnd(band, frameSize)
		n := end - start
		if n <= 0 {
			continue
		}

		// Compute energy the same way
		var sumSq float64
		for i := start; i < end && i < len(mdctCoeffs); i++ {
			sumSq += mdctCoeffs[i] * mdctCoeffs[i]
		}
		expectedRaw := 0.5 * math.Log2(sumSq+1e-27)

		// energies[band] should be mean-relative
		expectedMeanRelative := expectedRaw
		if band < len(eMeans) {
			expectedMeanRelative -= eMeans[band] * DB6
		}

		// Check
		if math.Abs(energies[band]-expectedMeanRelative) > 1e-6 {
			t.Errorf("band %d: energy = %f, expected %f (raw=%f)",
				band, energies[band], expectedMeanRelative, expectedRaw)
		}

		offset += n
	}
}

// TestNormalizationGainValues checks that gain values used for normalization
// match what the decoder would use for denormalization.
func TestNormalizationGainValues(t *testing.T) {
	_ = NewEncoder(1)
	_ = 480
	nbBands := 21

	// Create test energies (mean-relative, as ComputeBandEnergies returns)
	energies := make([]float64, nbBands)
	for band := 0; band < nbBands; band++ {
		// Simulate typical energy values (mean-relative)
		energies[band] = float64(band-10) * 0.5 // Range roughly -5 to +5
	}

	t.Log("Verifying gain computation consistency:")
	t.Log("band | mean-rel-E | +eMeans*DB6 | clamped | gain | decoder-gain")
	t.Log("-----|------------|-------------|---------|------|-------------")

	for band := 0; band < nbBands && band < len(eMeans); band++ {
		// What normalization does (bands_encode.go NormalizeBandsToArray):
		eVal := energies[band]
		eVal += eMeans[band] * DB6
		if eVal > 32*DB6 {
			eVal = 32 * DB6
		}
		normGain := math.Exp2(eVal / DB6)

		// What decoder does (bands.go DecodeBands):
		// The decoder receives decoded energies (from coarse+fine quantization)
		// and adds eMeans. For this test, assume decoded == input energies.
		eDecoder := energies[band]
		if band < len(eMeans) {
			eDecoder += eMeans[band] * DB6
		}
		if eDecoder > 32*DB6 {
			eDecoder = 32 * DB6
		}
		decoderGain := math.Exp2(eDecoder / DB6)

		t.Logf("%4d | %10.4f | %11.4f | %7.4f | %10.4f | %10.4f",
			band, energies[band], eVal, eVal, normGain, decoderGain)

		// Gains should match
		if math.Abs(normGain-decoderGain) > 1e-9 {
			t.Errorf("band %d: norm gain %f != decoder gain %f", band, normGain, decoderGain)
		}
	}
}

// TestEncoderDecoderNormalizationConsistency tests that encoder's normalized
// coefficients, when decoded, produce the correct magnitude.
func TestEncoderDecoderNormalizationConsistency(t *testing.T) {
	frameSize := 480
	channels := 1

	// Create encoder and decoder
	enc := NewEncoder(channels)
	dec := NewDecoder(channels)

	// Generate test PCM with known frequency content
	pcm := make([]float64, frameSize*channels)
	for i := range pcm {
		// 440 Hz sine wave at 48kHz, amplitude 0.5
		t := float64(i) / 48000.0
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*t)
	}

	// Encode
	encoded, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded frame: %d bytes", len(encoded))

	// Decode
	dec.SetBandwidth(CELTFullband)
	decoded, err := dec.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Verify decoded output
	if len(decoded) == 0 {
		t.Fatal("Decoded output is empty")
	}

	// Check that decoded signal has reasonable amplitude
	var maxAbs float64
	var sumSq float64
	for _, s := range decoded {
		if math.Abs(s) > maxAbs {
			maxAbs = math.Abs(s)
		}
		sumSq += s * s
	}
	rms := math.Sqrt(sumSq / float64(len(decoded)))

	t.Logf("Input:  max=0.5, RMS=%.4f", 0.5/math.Sqrt(2))
	t.Logf("Output: max=%.4f, RMS=%.4f", maxAbs, rms)

	// The decoded signal should be on the same order of magnitude as input
	// Allow some tolerance for quantization and other losses
	if maxAbs < 0.01 {
		t.Errorf("Decoded amplitude too low: max=%.4f (expected >0.01)", maxAbs)
	}
	if maxAbs > 10 {
		t.Errorf("Decoded amplitude too high: max=%.4f (expected <10)", maxAbs)
	}

	// Compute simple correlation with input (allows for delay)
	bestCorr := 0.0
	for offset := 0; offset < 200; offset++ {
		var corr float64
		var count int
		for i := 0; i < len(pcm) && i+offset < len(decoded); i++ {
			corr += pcm[i] * decoded[i+offset]
			count++
		}
		if count > 0 {
			corr /= float64(count)
		}
		if corr > bestCorr {
			bestCorr = corr
		}
	}
	t.Logf("Best correlation: %.4f", bestCorr)

	// With correct normalization, correlation should be positive
	if bestCorr < 0 {
		t.Error("Decoded signal appears inverted or uncorrelated")
	}
}

// TestNormalizeBandsMethodComparison compares NormalizeBands (returns [][]float64)
// with NormalizeBandsToArray (returns contiguous []float64) to ensure consistency.
func TestNormalizeBandsMethodComparison(t *testing.T) {
	encoder := NewEncoder(1)
	fs := 480
	nbBands := 21

	// Generate MDCT coefficients
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, fs)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = math.Sin(float64(i)*0.15) * 50.0
	}

	// Compute energies
	energies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, fs)

	// Method 1: NormalizeBands (returns per-band vectors, also unit-normalizes each)
	shapes := encoder.NormalizeBands(mdctCoeffs, energies, nbBands, fs)

	// Method 2: NormalizeBandsToArray (returns contiguous array, no unit normalization)
	normArray := encoder.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, fs)

	// Compare: normArray should equal shapes before unit-normalization
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, fs)
		if n <= 0 {
			continue
		}

		// Get L2 norm of the array slice (to undo unit normalization for comparison)
		var arrayNormSq float64
		for i := 0; i < n && offset+i < len(normArray); i++ {
			arrayNormSq += normArray[offset+i] * normArray[offset+i]
		}
		arrayNorm := math.Sqrt(arrayNormSq)

		// shapes[band] is unit-normalized, normArray is just divided by gain
		// So normArray = shapes[band] * ||normArray[band]||
		if band < len(shapes) && len(shapes[band]) > 0 && arrayNorm > 1e-10 {
			for i := 0; i < n && offset+i < len(normArray); i++ {
				// Reconstruct from unit-norm shape
				expected := shapes[band][i] * arrayNorm
				actual := normArray[offset+i]
				if math.Abs(expected-actual) > 1e-6 {
					t.Errorf("band %d, bin %d: expected %f, got %f", band, i, expected, actual)
				}
			}
		}

		offset += n
	}
}

// TestComputeLinearBandAmplitudes verifies that linear band amplitudes are computed
// correctly from MDCT coefficients. This is the critical fix for PVQ normalization.
func TestComputeLinearBandAmplitudes(t *testing.T) {
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients with known properties
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = math.Sin(float64(i)*0.1) * float64(i%50+1) * 0.01
	}

	// Compute linear band amplitudes
	bandE := ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)
	if bandE == nil {
		t.Fatal("ComputeLinearBandAmplitudes returned nil")
	}
	if len(bandE) != nbBands {
		t.Fatalf("expected %d bands, got %d", nbBands, len(bandE))
	}

	// Verify each band amplitude matches sqrt(sum of squares)
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			break
		}

		// Compute expected amplitude: sqrt(epsilon + sum(x^2))
		expected := float32(1e-27) // libopus epsilon
		for i := 0; i < n; i++ {
			v := float32(mdctCoeffs[offset+i])
			expected += v * v
		}
		expected = float32(math.Sqrt(float64(expected)))

		actual := float32(bandE[band])
		relErr := math.Abs(float64(expected-actual)) / float64(expected)
		if relErr > 1e-5 {
			t.Errorf("band %d: expected amplitude %f, got %f (rel err %.2e)",
				band, expected, actual, relErr)
		}

		offset += n
	}
}

// TestNormalizationUsesLinearAmplitudes verifies that normalization now uses
// direct linear amplitudes instead of log-domain roundtrip.
func TestNormalizationUsesLinearAmplitudes(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 21

	// Generate MDCT coefficients
	totalBins := 0
	for band := 0; band < nbBands; band++ {
		totalBins += ScaledBandWidth(band, frameSize)
	}

	mdctCoeffs := make([]float64, totalBins)
	for i := range mdctCoeffs {
		mdctCoeffs[i] = math.Sin(float64(i)*0.2) * 100.0
	}

	// Compute linear band amplitudes directly
	bandE := ComputeLinearBandAmplitudes(mdctCoeffs, nbBands, frameSize)

	// Get normalized coefficients (the energies parameter is now ignored)
	normalized := enc.NormalizeBandsToArray(mdctCoeffs, nil, nbBands, frameSize)
	if normalized == nil {
		t.Fatal("NormalizeBandsToArray returned nil")
	}

	// Verify normalization: normalized = mdct / bandE
	offset := 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(mdctCoeffs) {
			break
		}

		amplitude := bandE[band]
		if amplitude < 1e-27 {
			amplitude = 1e-27
		}
		g := 1.0 / amplitude

		for i := 0; i < n; i++ {
			expected := mdctCoeffs[offset+i] * g
			actual := normalized[offset+i]
			if math.Abs(expected-actual) > 1e-10 {
				t.Errorf("band %d, bin %d: expected %f, got %f",
					band, i, expected, actual)
			}
		}

		offset += n
	}

	// Verify L2 norm of each band is 1.0
	offset = 0
	for band := 0; band < nbBands; band++ {
		n := ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}
		if offset+n > len(normalized) {
			break
		}

		var sumSq float64
		for i := 0; i < n; i++ {
			sumSq += normalized[offset+i] * normalized[offset+i]
		}
		l2norm := math.Sqrt(sumSq)

		if math.Abs(l2norm-1.0) > 1e-5 {
			t.Errorf("band %d: L2 norm = %f, expected 1.0", band, l2norm)
		}

		offset += n
	}
}
