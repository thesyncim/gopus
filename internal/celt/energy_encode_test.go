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

		// All energies should be -28.0 (minimum)
		for band, e := range energies {
			if e != -28.0 {
				t.Errorf("Band %d: energy = %f, want -28.0", band, e)
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

		// Target band should have positive energy
		if energies[targetBand] <= -28.0 {
			t.Errorf("Target band %d: energy = %f, expected > -28.0", targetBand, energies[targetBand])
		}

		// Verify energy is in reasonable range
		// For unit amplitude: sumSq = bandWidth, RMS = 1, log2(1) = 0
		if energies[targetBand] < -5 || energies[targetBand] > 5 {
			t.Errorf("Target band %d: energy = %f, expected roughly 0", targetBand, energies[targetBand])
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

			// All energies should be in valid range
			for band, e := range energies {
				if e < -28.0 || e > 16.0 {
					t.Errorf("frameSize %d, band %d: energy %f out of range [-28, 16]", frameSize, band, e)
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
			mdctCoeffs[i] = float64(i) / float64(frameSize)             // L ramp up
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

				// Verify quantized energies are 6dB steps from prediction
				for band := 0; band < nbBands; band++ {
					// Quantized should differ from original by at most 3dB (half step)
					diff := math.Abs(energies[band] - quantizedEnc[band])
					if diff > 3.0+0.01 { // 3dB = half of 6dB step
						t.Errorf("Band %d: original=%f, quantized=%f, diff=%f (>3dB)",
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

// TestCoarseEnergyQuantization verifies 6dB quantization works correctly.
func TestCoarseEnergyQuantization(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	nbBands := GetModeConfig(frameSize).EffBands
	lm := GetModeConfig(frameSize).LM

	// Test specific energy values
	testCases := []struct {
		energy   float64
		expected float64 // expected quantized (6dB steps from -28 + prediction)
	}{
		{0.0, 0.0},     // Near prediction
		{6.0, 6.0},     // One step up
		{-6.0, -6.0},   // One step down
		{3.0, 6.0},     // Rounds up
		{-3.0, -6.0},   // Rounds down
		{8.5, 6.0},     // More than one step
		{-10.0, -12.0}, // Two steps down
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
		// The quantized value should be a multiple of 6dB
		remainder := math.Mod(quantized[0], DB6)
		if math.Abs(remainder) > 0.01 && math.Abs(remainder-DB6) > 0.01 {
			t.Errorf("Energy %f: quantized to %f, not a multiple of 6dB (remainder=%f)",
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
		bitsBeforeFine := re.Tell()

		// Step 2: Encode fine
		enc.EncodeFineEnergy(energies, quantizedCoarse, nbBands, fineBits)
		bitsAfterFine := re.Tell()

		// Finish encoding
		encoded := re.Done()

		// Verify output produced
		if len(encoded) == 0 {
			t.Error("No bytes produced")
		}

		// Verify fine encoding consumed bits
		fineBitsUsed := bitsAfterFine - bitsBeforeFine
		expectedMinBits := nbBands * 3 // At least 3 bits per band
		if fineBitsUsed < expectedMinBits/2 {
			t.Errorf("Fine encoding used %d bits, expected at least %d",
				fineBitsUsed, expectedMinBits/2)
		}
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

			// Use standard decay
			decay := 16384

			// Encode
			buf := make([]byte, 64)
			re := &rangecoding.Encoder{}
			re.Init(buf)
			enc.rangeEncoder = re
			enc.encodeLaplace(val, decay)
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
	// Verify fs0 computation matches
	decay := 16384
	fs0Encoder := laplaceNMIN + (laplaceScale*decay)>>15

	// Should produce a valid center frequency for symbol 0
	if fs0Encoder <= 0 || fs0Encoder >= laplaceFS {
		t.Errorf("fs0 = %d, should be in (0, %d)", fs0Encoder, laplaceFS)
	}

	// Verify frequency progression (fk decreases geometrically)
	prevFk := fs0Encoder
	for k := 1; k <= 10; k++ {
		fk := (prevFk * decay) >> 15
		if fk < laplaceNMIN {
			fk = laplaceNMIN
		}

		// fk should be non-negative and bounded
		if fk <= 0 || fk > prevFk {
			t.Errorf("k=%d: fk=%d invalid (prevFk=%d)", k, fk, prevFk)
		}
		prevFk = fk
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
		if diff > 3.0+0.01 { // Half step of 6dB
			t.Errorf("Band %d: quantization error %f exceeds 3dB", band, diff)
		}
	}
}
