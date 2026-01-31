package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFineEnergyQuantizationCompare compares gopus fine energy encoding with libopus.
func TestFineEnergyQuantizationCompare(t *testing.T) {
	const nbBands = 21
	const channels = 1
	const start = 0
	const end = nbBands

	// Create test energies and simulate coarse quantization residuals
	// In practice, error = targetEnergy - coarseQuantizedEnergy
	targetEnergies := make([]float64, nbBands)
	coarseQuantized := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		// Simulate some band energies (in log2 scale, where 1.0 = 6dB)
		targetEnergies[i] = 5.0 + 0.3*float64(i) + 0.2*math.Sin(float64(i)*0.5)
		// Simulate coarse quantization (rounded to integer 6dB steps)
		coarseQuantized[i] = math.Floor(targetEnergies[i] + 0.5)
	}

	// Compute error (residual) for each band
	errorGopus := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		errorGopus[i] = targetEnergies[i] - coarseQuantized[i]
	}

	// Create fine bits allocation (typical values from bit allocation)
	fineBits := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		// Allocate more bits to lower bands (typical CELT behavior)
		if i < 5 {
			fineBits[i] = 3
		} else if i < 10 {
			fineBits[i] = 2
		} else if i < 15 {
			fineBits[i] = 1
		} else {
			fineBits[i] = 0
		}
	}

	t.Log("=== Fine Energy Quantization Comparison ===")

	// === GOPUS encoding ===
	enc := celt.NewEncoder(channels)
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	// Make copies for gopus since it modifies in place
	coarseGopus := make([]float64, nbBands)
	copy(coarseGopus, coarseQuantized)

	enc.EncodeFineEnergy(targetEnergies, coarseGopus, nbBands, fineBits)

	re.Done()
	gopusTell := re.Tell()

	t.Logf("Gopus: encoded %d bits", gopusTell)

	// === LIBOPUS encoding ===
	// Convert to float32 for libopus
	oldEBandsLib := make([]float32, nbBands)
	errorLib := make([]float32, nbBands)
	for i := 0; i < nbBands; i++ {
		oldEBandsLib[i] = float32(coarseQuantized[i])
		errorLib[i] = float32(errorGopus[i])
	}

	libResult, err := LibopusQuantFineEnergy(oldEBandsLib, errorLib, fineBits, start, end, channels)
	if err != nil || libResult == nil {
		t.Fatalf("LibopusQuantFineEnergy failed: %v", err)
	}

	t.Logf("Libopus: encoded %d bytes", len(libResult.EncodedBytes))

	// Compare updated energies
	t.Log("\n=== Updated Quantized Energies ===")
	mismatches := 0
	for i := 0; i < nbBands; i++ {
		gopusVal := coarseGopus[i]
		libVal := float64(libResult.UpdatedEnergies[i])
		diff := math.Abs(gopusVal - libVal)
		match := "OK"
		if diff > 0.001 {
			match = "MISMATCH"
			mismatches++
		}
		if i < 10 || diff > 0.001 {
			t.Logf("  Band %2d: fineBits=%d gopus=%.6f libopus=%.6f diff=%.6f %s",
				i, fineBits[i], gopusVal, libVal, diff, match)
		}
	}

	if mismatches > 0 {
		t.Errorf("Found %d mismatches in fine energy quantization", mismatches)
	} else {
		t.Log("All fine energy values match!")
	}
}

// TestFineEnergyQuantIndexCompare compares the actual quantization indices.
func TestFineEnergyQuantIndexCompare(t *testing.T) {
	const nbBands = 21
	const channels = 1

	// Test various error values with different bit allocations
	testCases := []struct {
		name     string
		error    float64
		fineBits int
	}{
		{"zero error, 1 bit", 0.0, 1},
		{"zero error, 2 bits", 0.0, 2},
		{"zero error, 3 bits", 0.0, 3},
		{"positive small, 1 bit", 0.2, 1},
		{"positive small, 2 bits", 0.2, 2},
		{"positive small, 3 bits", 0.2, 3},
		{"negative small, 1 bit", -0.2, 1},
		{"negative small, 2 bits", -0.2, 2},
		{"negative small, 3 bits", -0.2, 3},
		{"positive large, 2 bits", 0.45, 2},
		{"negative large, 2 bits", -0.45, 2},
		{"exactly 0.5, 2 bits", 0.5, 2},
		{"exactly -0.5, 2 bits", -0.5, 2},
	}

	t.Log("=== Fine Energy Index Comparison ===")

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create single-band test
			targetEnergy := 5.0 + tc.error
			coarseQuantized := 5.0

			// GOPUS
			fineBits := make([]int, nbBands)
			fineBits[0] = tc.fineBits

			enc := celt.NewEncoder(channels)
			buf := make([]byte, 64)
			re := &rangecoding.Encoder{}
			re.Init(buf)
			enc.SetRangeEncoder(re)

			targetArr := make([]float64, nbBands)
			coarseArr := make([]float64, nbBands)
			targetArr[0] = targetEnergy
			coarseArr[0] = coarseQuantized

			enc.EncodeFineEnergy(targetArr, coarseArr, nbBands, fineBits)
			gopusBytes := re.Done()

			// LIBOPUS
			oldEBandsLib := make([]float32, nbBands)
			errorLib := make([]float32, nbBands)
			oldEBandsLib[0] = float32(coarseQuantized)
			errorLib[0] = float32(tc.error)

			libResult, _ := LibopusQuantFineEnergy(oldEBandsLib, errorLib, fineBits, 0, nbBands, channels)
			if libResult == nil {
				t.Fatal("LibopusQuantFineEnergy failed")
			}

			// Compare results
			gopusUpdated := coarseArr[0]
			libUpdated := float64(libResult.UpdatedEnergies[0])
			diff := math.Abs(gopusUpdated - libUpdated)

			t.Logf("error=%.3f bits=%d: gopus=%.6f libopus=%.6f diff=%.6f",
				tc.error, tc.fineBits, gopusUpdated, libUpdated, diff)

			// Also compare encoded bytes
			t.Logf("  gopus bytes: %02x", gopusBytes)
			t.Logf("  libopus bytes: %02x", libResult.EncodedBytes)

			if diff > 0.0001 {
				t.Errorf("Mismatch! gopus=%.6f libopus=%.6f diff=%.6f",
					gopusUpdated, libUpdated, diff)
			}
		})
	}
}

// TestEnergyFinaliseCompare compares gopus EncodeEnergyFinalise with libopus.
func TestEnergyFinaliseCompare(t *testing.T) {
	const nbBands = 21
	const channels = 1
	const start = 0
	const end = nbBands

	// Create test data simulating after fine energy encoding
	targetEnergies := make([]float64, nbBands)
	quantizedEnergies := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		targetEnergies[i] = 5.0 + 0.1*float64(i)
		// After fine encoding, residual is typically small
		quantizedEnergies[i] = targetEnergies[i] + 0.1*(float64(i%5)-2)
	}

	// Create fine quant and priority (typical values)
	fineQuant := make([]int, nbBands)
	finePriority := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		if i < 10 {
			fineQuant[i] = 2
			finePriority[i] = 0
		} else if i < 15 {
			fineQuant[i] = 1
			finePriority[i] = 1
		} else {
			fineQuant[i] = 0
			finePriority[i] = 0
		}
	}

	bitsLeft := 10 // Bits available for finalise step

	t.Log("=== Energy Finalise Comparison ===")

	// === GOPUS encoding ===
	enc := celt.NewEncoder(channels)
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	// Make copies
	quantGopus := make([]float64, nbBands)
	copy(quantGopus, quantizedEnergies)

	enc.EncodeEnergyFinalise(targetEnergies, quantGopus, nbBands, fineQuant, finePriority, bitsLeft)

	gopusBytes := re.Done()
	t.Logf("Gopus: encoded %d bytes", len(gopusBytes))

	// === LIBOPUS encoding ===
	oldEBandsLib := make([]float32, nbBands)
	errorLib := make([]float32, nbBands)
	for i := 0; i < nbBands; i++ {
		oldEBandsLib[i] = float32(quantizedEnergies[i])
		errorLib[i] = float32(targetEnergies[i] - quantizedEnergies[i])
	}

	libResult, err := LibopusQuantEnergyFinalise(oldEBandsLib, errorLib, fineQuant, finePriority, bitsLeft, start, end, channels)
	if err != nil || libResult == nil {
		t.Fatalf("LibopusQuantEnergyFinalise failed: %v", err)
	}

	t.Logf("Libopus: encoded %d bytes", len(libResult.EncodedBytes))

	// Compare updated energies
	t.Log("\n=== Updated Energies After Finalise ===")
	mismatches := 0
	for i := 0; i < nbBands; i++ {
		gopusVal := quantGopus[i]
		libVal := float64(libResult.UpdatedEnergies[i])
		diff := math.Abs(gopusVal - libVal)
		match := "OK"
		if diff > 0.0001 {
			match = "MISMATCH"
			mismatches++
		}
		if i < 10 || diff > 0.0001 {
			t.Logf("  Band %2d: fineQuant=%d prio=%d gopus=%.6f libopus=%.6f diff=%.6f %s",
				i, fineQuant[i], finePriority[i], gopusVal, libVal, diff, match)
		}
	}

	// Compare bytes
	t.Log("\n=== Encoded Bytes ===")
	t.Logf("  gopus bytes: %02x", gopusBytes)
	t.Logf("  libopus bytes: %02x", libResult.EncodedBytes)

	if mismatches > 0 {
		t.Errorf("Found %d mismatches in energy finalise", mismatches)
	} else {
		t.Log("All energy finalise values match!")
	}
}

// TestFineEnergyFormula tests the exact formula used in gopus vs libopus.
func TestFineEnergyFormula(t *testing.T) {
	// Test the quantization formula: q = floor((error + 0.5) * scale)
	// And the offset formula: offset = (q + 0.5) / scale - 0.5

	testCases := []struct {
		error    float64
		fineBits int
	}{
		{0.0, 1},
		{0.0, 2},
		{0.0, 3},
		{0.25, 1},
		{0.25, 2},
		{0.25, 3},
		{-0.25, 1},
		{-0.25, 2},
		{-0.25, 3},
		{0.49, 2},
		{-0.49, 2},
	}

	t.Log("=== Fine Energy Formula Verification ===")
	t.Log("Formula: q = floor((error + 0.5) * scale)")
	t.Log("Offset:  offset = (q + 0.5) / scale - 0.5")

	for _, tc := range testCases {
		shiftBits := tc.fineBits
		scale := float64(int(1) << shiftBits)

		// Gopus formula (from energy_encode.go)
		// q := int(math.Floor((fine/DB6+0.5)*scale + 1e-9))
		// where fine = error and DB6 = 1.0
		qGopus := int(math.Floor((tc.error + 0.5) * scale))

		// Clamp
		if qGopus < 0 {
			qGopus = 0
		}
		if qGopus >= int(scale) {
			qGopus = int(scale) - 1
		}

		// Offset: (q+0.5)/scale - 0.5
		offsetGopus := (float64(qGopus) + 0.5) / scale - 0.5

		// Libopus formula (float path from quant_bands.c)
		// q2 = (int)floor((error*(1<<prev)+.5f)*extra);
		// With prev=0, extra = 1 << extra_quant[i]
		// q2 = floor((error + 0.5) * extra)
		qLib := int(math.Floor((tc.error + 0.5) * scale))

		// Clamp
		if qLib < 0 {
			qLib = 0
		}
		if qLib >= int(scale) {
			qLib = int(scale) - 1
		}

		// offset = (q2+.5f)*(1<<(14-extra_quant[i]))*(1.f/16384) - .5f
		// = (q2 + 0.5) / (1 << extra_quant[i]) - 0.5
		offsetLib := (float64(qLib) + 0.5) / scale - 0.5

		match := "OK"
		if qGopus != qLib || math.Abs(offsetGopus-offsetLib) > 1e-10 {
			match = "MISMATCH"
		}

		t.Logf("error=%.3f bits=%d scale=%.0f: q_gopus=%d q_lib=%d offset_gopus=%.6f offset_lib=%.6f %s",
			tc.error, tc.fineBits, scale, qGopus, qLib, offsetGopus, offsetLib, match)

		if match == "MISMATCH" {
			t.Errorf("Formula mismatch for error=%.3f bits=%d", tc.error, tc.fineBits)
		}
	}
}
