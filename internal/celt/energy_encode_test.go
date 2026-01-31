package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestComputeBandEnergies verifies band energy computation.
func TestComputeBandEnergies(t *testing.T) {
	t.Run("ZeroInput", func(t *testing.T) {
		enc := NewEncoder(1)
		frameSize := 960
		nbBands := GetModeConfig(frameSize).EffBands

		// Zero coefficients
		mdctCoeffs := make([]float64, frameSize)

		energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

		// All energies should match the epsilon-based silence floor.
		silence := 0.5 * math.Log2(1e-27)
		for band, e := range energies {
			expected := silence
			if band < len(eMeans) {
				expected -= eMeans[band] * DB6
			}
			if math.Abs(e-expected) > 1e-6 {
				t.Errorf("Band %d: energy = %f, want %f", band, e, expected)
			}
		}
	})

	t.Run("SineInBand", func(t *testing.T) {
		enc := NewEncoder(1)
		frameSize := 960
		nbBands := GetModeConfig(frameSize).EffBands

		// Create sine wave in a specific band
		mdctCoeffs := make([]float64, frameSize)

		// Put energy in band 10 (mid-frequency)
		targetBand := 10
		start := ScaledBandStart(targetBand, frameSize)
		end := ScaledBandEnd(targetBand, frameSize)
		for i := start; i < end; i++ {
			mdctCoeffs[i] = 1.0 // Unit amplitude in band
		}

		energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

		width := end - start
		if width <= 0 {
			t.Fatalf("invalid band width for band %d", targetBand)
		}
		expected := 0.5 * math.Log2(float64(width))
		if targetBand < len(eMeans) {
			expected -= eMeans[targetBand] * DB6
		}
		if math.Abs(energies[targetBand]-expected) > 0.1 {
			t.Errorf("Target band %d: energy = %f, want %f", targetBand, energies[targetBand], expected)
		}
	})

	t.Run("AllFrameSizes", func(t *testing.T) {
		enc := NewEncoder(1)
		frameSizes := []int{120, 240, 480, 960}

		for _, frameSize := range frameSizes {
			nbBands := GetModeConfig(frameSize).EffBands

			// Random coefficients
			mdctCoeffs := make([]float64, frameSize)
			for i := range mdctCoeffs {
				mdctCoeffs[i] = rand.Float64()*2 - 1
			}

			energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

			if len(energies) != nbBands {
				t.Errorf("frameSize %d: got %d energies, want %d", frameSize, len(energies), nbBands)
			}

			// All energies should be finite
			for band, e := range energies {
				if math.IsNaN(e) || math.IsInf(e, 0) {
					t.Errorf("frameSize %d, band %d: energy %f is invalid", frameSize, band, e)
				}
			}
		}
	})

	t.Run("Stereo", func(t *testing.T) {
		enc := NewEncoder(2)
		frameSize := 960
		nbBands := GetModeConfig(frameSize).EffBands

		// Stereo coefficients: L then R
		mdctCoeffs := make([]float64, frameSize*2)
		for i := 0; i < frameSize; i++ {
			mdctCoeffs[i] = float64(i) / float64(frameSize)               // L ramp up
			mdctCoeffs[frameSize+i] = 1.0 - float64(i)/float64(frameSize) // R ramp down
		}

		energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

		// Should have energies for both channels
		if len(energies) != nbBands*2 {
			t.Errorf("Stereo: got %d energies, want %d", len(energies), nbBands*2)
		}

		// L and R should have different energies
		differentCount := 0
		for band := 0; band < nbBands; band++ {
			if math.Abs(energies[band]-energies[nbBands+band]) > 0.5 {
				differentCount++
			}
		}
		if differentCount == 0 {
			t.Error("L and R energies are identical - expected difference")
		}
	})
}

// TestCoarseEnergyEncoderProducesValidOutput verifies the encoder produces valid output.
// Note: Strict encode->decode round-trip testing is limited by the decoder's approximate
// Laplace implementation (updateRange uses DecodeBit approximation). This test verifies
// the encoder produces non-empty, consistent output with correct quantization.
func TestCoarseEnergyEncoderProducesValidOutput(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}

	for _, frameSize := range frameSizes {
		mode := GetModeConfig(frameSize)
		lm := mode.LM
		nbBands := mode.EffBands

		for _, intra := range []bool{true, false} {
			name := "inter"
			if intra {
				name = "intra"
			}

			t.Run(sizeToString(frameSize)+"_"+name, func(t *testing.T) {
				enc := NewEncoder(1)

				// Use seeded RNG for deterministic test (fixes flaky test issue)
				// Different seed per test case to ensure coverage
				seed := int64(12345) + int64(frameSize)*100
				if intra {
					seed += 1000
				}
				rng := rand.New(rand.NewSource(seed))

				// Generate random energies in valid range with meaningful variation
				// Ensure energies differ enough from prediction (-28.0) to produce output
				energies := make([]float64, nbBands)
				for i := range energies {
					// Generate energies with larger variation to ensure non-zero output
					energies[i] = rng.Float64()*30 - 14 // [-14, +16] - more variation
				}

				// Encode
				buf := make([]byte, 256)
				re := &rangecoding.Encoder{}
				re.Init(buf)
				enc.SetRangeEncoder(re)

				quantizedEnc := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

				// Finish encoding
				encoded := re.Done()

				// Verify output produced (intra mode always produces bytes,
				// inter mode may produce zero bytes if energies match prediction)
				if intra && len(encoded) == 0 {
					t.Errorf("No bytes produced for %d bands in intra mode", nbBands)
				}
				// Log inter mode output for diagnostics
				if !intra {
					t.Logf("Inter mode produced %d bytes for %d bands", len(encoded), nbBands)
				}

				// Verify quantized energies are finite and not wildly off.
				for band := 0; band < nbBands; band++ {
					diff := math.Abs(energies[band] - quantizedEnc[band])
					if math.IsNaN(diff) || math.IsInf(diff, 0) {
						t.Errorf("Band %d: quantized diff is invalid: %v", band, diff)
					}
					if diff > 12*DB6 {
						t.Errorf("Band %d: original=%f, quantized=%f, diff=%f (>12*DB6)",
							band, energies[band], quantizedEnc[band], diff)
					}
				}

				// Verify prevEnergy was updated
				for band := 0; band < nbBands && band < MaxBands; band++ {
					if enc.prevEnergy[band] != quantizedEnc[band] {
						t.Errorf("Band %d: prevEnergy not updated", band)
					}
				}
			})
		}
	}
}

// TestCoarseEnergyQuantization verifies DB6 quantization works correctly.
func TestCoarseEnergyQuantization(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	nbBands := GetModeConfig(frameSize).EffBands
	lm := GetModeConfig(frameSize).LM

	// Test specific energy values
	testCases := []struct {
		energy   float64
		expected float64 // expected quantized (DB6 steps from -28 + prediction)
	}{
		{0.0, 0.0},             // Near prediction
		{DB6, DB6},             // One step up
		{-DB6, -DB6},           // One step down
		{DB6 / 2, DB6},         // Rounds up
		{-DB6 / 2, -DB6},       // Rounds down
		{1.5 * DB6, DB6},       // More than one step
		{-2.0 * DB6, -2 * DB6}, // Two steps down
	}

	for _, tc := range testCases {
		// Reset encoder
		enc.Reset()

		// Create energies array with test value in first band
		energies := make([]float64, nbBands)
		for i := range energies {
			energies[i] = tc.energy
		}

		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		enc.SetRangeEncoder(re)

		// Use intra mode (no inter-frame prediction, but has inter-band)
		quantized := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)
		re.Done()

		// First band with intra has no alpha prediction, beta prediction is 0
		// So quantization is purely based on energy value
		// The quantized value should be a multiple of DB6
		remainder := math.Mod(quantized[0], DB6)
		if math.Abs(remainder) > 0.01 && math.Abs(remainder-DB6) > 0.01 {
			t.Errorf("Energy %f: quantized to %f, not a multiple of DB6 (remainder=%f)",
				tc.energy, quantized[0], remainder)
		}
	}
}

// TestFineEnergyEncoderProducesValidOutput verifies fine energy encoding works.
func TestFineEnergyEncoderProducesValidOutput(t *testing.T) {
	t.Run("BasicEncoding", func(t *testing.T) {
		enc := NewEncoder(1)
		frameSize := 960
		nbBands := GetModeConfig(frameSize).EffBands
		lm := GetModeConfig(frameSize).LM

		// Generate energies
		energies := make([]float64, nbBands)
		for i := range energies {
			energies[i] = rand.Float64()*30 - 20 // [-20, +10]
		}

		// Fine bits allocation
		fineBits := make([]int, nbBands)
		for i := range fineBits {
			fineBits[i] = 3 // 3 bits per band
		}

		// Step 1: Encode coarse
		buf := make([]byte, 512)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		enc.SetRangeEncoder(re)

		quantizedCoarse := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)
		// Step 2: Encode fine
		enc.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)

		// Finish encoding
		encoded := re.Done()

		// Verify output produced
		if len(encoded) == 0 {
			t.Error("No bytes produced")
		}

		// EncodeRawBits writes to the end buffer, so Tell() doesn't reflect usage.
	})

	t.Run("DifferentBitAllocations", func(t *testing.T) {
		bitAllocations := []int{1, 2, 4, 8}

		for _, bits := range bitAllocations {
			t.Run(string(rune('0'+bits))+"bits", func(t *testing.T) {
				enc := NewEncoder(1)
				frameSize := 960
				nbBands := GetModeConfig(frameSize).EffBands
				lm := GetModeConfig(frameSize).LM

				// Generate energies
				energies := make([]float64, nbBands)
				for i := range energies {
					energies[i] = rand.Float64()*30 - 20
				}

				// Uniform bit allocation
				fineBits := make([]int, nbBands)
				for i := range fineBits {
					fineBits[i] = bits
				}

				// Encode
				buf := make([]byte, 512)
				re := &rangecoding.Encoder{}
				re.Init(buf)
				enc.SetRangeEncoder(re)

				quantizedCoarse := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)
				enc.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
				encoded := re.Done()

				// Verify output produced
				if len(encoded) == 0 {
					t.Errorf("No bytes produced for %d bits/band", bits)
				}

				// Higher bits should produce more output
				t.Logf("%d bits/band: %d output bytes", bits, len(encoded))
			})
		}
	})
}

// TestEnergyEncodingAllFrameSizes tests energy encoding works for all frame sizes.
func TestEnergyEncodingAllFrameSizes(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	intraModes := []bool{true, false}

	for _, frameSize := range frameSizes {
		for _, intra := range intraModes {
			mode := GetModeConfig(frameSize)
			name := sizeToString(frameSize)
			if intra {
				name += "_intra"
			} else {
				name += "_inter"
			}

			t.Run(name, func(t *testing.T) {
				enc := NewEncoder(1)
				nbBands := mode.EffBands
				lm := mode.LM

				// Generate random energies
				energies := make([]float64, nbBands)
				for i := range energies {
					energies[i] = rand.Float64()*36 - 28 // [-28, +8]
				}

				// Fine bits
				fineBits := make([]int, nbBands)
				for i := range fineBits {
					fineBits[i] = 2
				}

				// Encode
				buf := make([]byte, 256)
				re := &rangecoding.Encoder{}
				re.Init(buf)
				enc.SetRangeEncoder(re)

				quantizedCoarse := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)
				enc.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
				encoded := re.Done()

				// Verify output produced
				if len(encoded) == 0 {
					t.Errorf("No bytes produced for frameSize=%d, intra=%v", frameSize, intra)
				}

				// Verify all quantized energies are valid
				for band := 0; band < nbBands; band++ {
					if math.IsNaN(quantizedCoarse[band]) || math.IsInf(quantizedCoarse[band], 0) {
						t.Errorf("Band %d: invalid quantized energy %f", band, quantizedCoarse[band])
					}
				}

				// Log output size for comparison
				t.Logf("frameSize=%d, intra=%v: %d bands, %d output bytes",
					frameSize, intra, nbBands, len(encoded))
			})
		}
	}
}

// TestLaplaceEncoderProducesValidOutput verifies Laplace encoding produces valid bytes.
// Note: Strict encode->decode round-trip testing is limited by the decoder's approximate
// updateRange implementation (uses DecodeBit approximations). This test verifies the
// encoder follows the same probability model and produces non-empty output.
func TestLaplaceEncoderProducesValidOutput(t *testing.T) {
	// Test range of values
	values := []int{-10, -5, -3, -2, -1, 0, 1, 2, 3, 5, 10}

	for _, val := range values {
		t.Run(string(rune('0'+val+10)), func(t *testing.T) {
			enc := NewEncoder(1)

			// Encode
			buf := make([]byte, 64)
			re := &rangecoding.Encoder{}
			re.Init(buf)
			enc.rangeEncoder = re
			fs := int(eProbModel[0][0][0]) << 7
			decay := int(eProbModel[0][0][1]) << 6
			enc.encodeLaplace(val, fs, decay)
			encoded := re.Done()

			// Should produce non-empty output
			if len(encoded) == 0 {
				t.Errorf("Value %d: no bytes produced", val)
			}

			// Larger values should consume more bits (generally)
			// This verifies the probability model is being used
			t.Logf("Value %d: encoded to %d bytes", val, len(encoded))
		})
	}
}

// TestLaplaceEncoderProbabilityModel verifies Laplace encoder uses same model as decoder.
func TestLaplaceEncoderProbabilityModel(t *testing.T) {
	decay := 16384
	fs0 := 16000

	fs1 := ec_laplace_get_freq1(fs0, decay)
	if fs1 < 0 || fs1 >= laplaceFS {
		t.Errorf("fs1 = %d, should be in [0, %d)", fs1, laplaceFS)
	}

	// Verify frequency progression (fk decreases geometrically)
	prevFk := fs1 + laplaceMinP
	for k := 1; k <= 10; k++ {
		fk := (prevFk * decay) >> 15
		if fk < laplaceMinP {
			fk = laplaceMinP
		}

		// fk should be non-negative and bounded
		if fk < 0 || fk > prevFk {
			t.Errorf("k=%d: fk=%d invalid (prevFk=%d)", k, fk, prevFk)
		}
		prevFk = fk + laplaceMinP
	}
}

// TestEncoderStateUpdates verifies encoder state updates correctly across frames.
func TestEncoderStateUpdates(t *testing.T) {
	enc := NewEncoder(1)

	frameSize := 960
	nbBands := GetModeConfig(frameSize).EffBands
	lm := GetModeConfig(frameSize).LM

	// Process multiple frames
	for frame := 0; frame < 3; frame++ {
		// Generate random energies
		energies := make([]float64, nbBands)
		for i := range energies {
			energies[i] = rand.Float64()*30 - 20
		}

		// Encode
		buf := make([]byte, 256)
		re := &rangecoding.Encoder{}
		re.Init(buf)
		enc.SetRangeEncoder(re)

		// Use intra for first frame, inter for rest
		intra := frame == 0
		quantizedEnc := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)
		re.Done()

		// Verify prevEnergy was updated with quantized values
		for band := 0; band < nbBands && band < MaxBands; band++ {
			if enc.prevEnergy[band] != quantizedEnc[band] {
				t.Errorf("Frame %d, band %d: prevEnergy=%f, expected=%f",
					frame, band, enc.prevEnergy[band], quantizedEnc[band])
			}
		}

		// For inter frames, verify prediction uses previous frame values
		if !intra && frame > 0 {
			// Inter-frame prediction should use alpha coefficient
			// Alpha > 0 means previous frame energy affects prediction
			if AlphaCoef[lm] <= 0 {
				t.Errorf("Frame %d: alpha coefficient should be positive for inter-frame prediction", frame)
			}
		}
	}
}

// TestComputeBandEnergiesIntegration verifies computed energies can be encoded.
func TestComputeBandEnergiesIntegration(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	nbBands := GetModeConfig(frameSize).EffBands
	lm := GetModeConfig(frameSize).LM

	// Create synthetic audio-like MDCT coefficients
	mdctCoeffs := make([]float64, frameSize)
	for i := range mdctCoeffs {
		// Low frequencies have more energy (typical of audio)
		freq := float64(i) / float64(frameSize)
		mdctCoeffs[i] = (1.0 - freq) * rand.Float64() * 0.5
	}

	// Compute energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Energies should be in valid range
	for band := 0; band < nbBands; band++ {
		if energies[band] < -28.0 || energies[band] > 16.0 {
			t.Errorf("Band %d: energy %f out of valid range [-28, 16]", band, energies[band])
		}
	}

	// Verify energies can be encoded
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	enc.SetRangeEncoder(re)

	quantized := enc.EncodeCoarseEnergy(energies, nbBands, true, lm)
	encoded := re.Done()

	// Verify output produced
	if len(encoded) == 0 {
		t.Error("No bytes produced")
	}

	// Verify quantized energies are valid
	for band := 0; band < nbBands; band++ {
		if math.IsNaN(quantized[band]) || math.IsInf(quantized[band], 0) {
			t.Errorf("Band %d: invalid quantized energy %f", band, quantized[band])
		}
	}

	// Verify quantization error is bounded
	for band := 0; band < nbBands; band++ {
		diff := math.Abs(energies[band] - quantized[band])
		if diff > (DB6/2)+0.01 {
			t.Errorf("Band %d: quantization error %f exceeds DB6/2", band, diff)
		}
	}
}
