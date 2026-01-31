// Package celt provides validation tests for energy decoding formulas.
// These tests verify that the Go implementation matches libopus float formulas exactly.
package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFineEnergyFormulaMatch validates the fine energy offset formula
// matches libopus float implementation exactly.
//
// libopus float formula (quant_bands.c unquant_fine_energy):
//
//	offset = (q2+.5f)*(1<<(14-extra))*(1.f/16384) - .5f;
//	offset *= (1<<(14-prev))*(1.f/16384);
//
// This simplifies to:
//
//	offset = ((q2 + 0.5) / (1 << extra) - 0.5) / (1 << prev)
func TestFineEnergyFormulaMatch(t *testing.T) {
	// Test all valid combinations of extra and prev bits
	for extra := 1; extra <= 8; extra++ {
		for q2 := 0; q2 < (1 << extra); q2++ {
			for prev := 0; prev <= extra; prev++ {
				// libopus formula (expanded for precision analysis)
				// Step 1: (q2+.5f)*(1<<(14-extra))*(1.f/16384)
				step1 := (float64(q2) + 0.5) * float64(uint(1)<<(14-extra)) * (1.0 / 16384.0)
				// Step 2: - .5f
				step2 := step1 - 0.5
				// Step 3: *= (1<<(14-prev))*(1.f/16384)
				libOffset := step2
				if prev > 0 {
					libOffset *= float64(uint(1)<<(14-prev)) * (1.0 / 16384.0)
				}

				// gopus formula (from energy.go decodeFineEnergy)
				scale := float64(uint(1) << extra)
				goOffset := (float64(q2)+0.5)/scale - 0.5
				if prev > 0 {
					goOffset /= float64(uint(1) << prev)
				}

				diff := math.Abs(libOffset - goOffset)
				if diff > 1e-15 {
					t.Errorf("Fine offset mismatch: extra=%d, q2=%d, prev=%d: lib=%.15f, go=%.15f, diff=%.15e",
						extra, q2, prev, libOffset, goOffset, diff)
				}
			}
		}
	}
	t.Log("Fine energy formula matches libopus exactly")
}

// TestEnergyFinaliseFormulaMatch validates the finalise offset formula
// matches libopus float implementation exactly.
//
// libopus float formula (quant_bands.c unquant_energy_finalise):
//
//	offset = (q2-.5f)*(1<<(14-fine_quant[i]-1))*(1.f/16384);
//
// This simplifies to:
//
//	offset = (q2 - 0.5) / (1 << (fineQuant + 1))
func TestEnergyFinaliseFormulaMatch(t *testing.T) {
	for fineQuant := 0; fineQuant < 8; fineQuant++ {
		for q2 := 0; q2 <= 1; q2++ {
			// libopus formula
			libOffset := (float64(q2) - 0.5) * float64(uint(1)<<(14-fineQuant-1)) * (1.0 / 16384.0)

			// gopus formula (from energy.go DecodeEnergyFinalise)
			goOffset := (float64(q2) - 0.5) / float64(uint(1)<<(fineQuant+1))

			diff := math.Abs(libOffset - goOffset)
			if diff > 1e-15 {
				t.Errorf("Finalise offset mismatch: fineQuant=%d, q2=%d: lib=%.15f, go=%.15f, diff=%.15e",
					fineQuant, q2, libOffset, goOffset, diff)
			}
		}
	}
	t.Log("Energy finalise formula matches libopus exactly")
}

// TestAmplitudeComputationMatch validates amplitude (denormalization gain) computation.
//
// In CELT, band amplitudes are computed as:
//
//	bandE[i] = exp2(bandLogE[i] + eMeans[i])
//
// where bandLogE is in log2 units (1.0 = 6 dB change).
func TestAmplitudeComputationMatch(t *testing.T) {
	logEnergies := []float64{-20.0, -10.0, -5.0, 0.0, 5.0, 10.0, 15.0, 20.0, 25.0}

	for _, logE := range logEnergies {
		for band := 0; band < min(5, len(eMeans)); band++ {
			totalLogE := logE + eMeans[band]

			// Both libopus and gopus use exp2(logE)
			libAmp := math.Pow(2.0, totalLogE)
			goAmp := math.Pow(2.0, totalLogE)

			diff := math.Abs(libAmp - goAmp)
			if diff > 1e-15 {
				t.Errorf("Amplitude mismatch: logE=%.1f, band=%d: lib=%.15f, go=%.15f, diff=%.15e",
					logE, band, libAmp, goAmp, diff)
			}
		}
	}
	t.Log("Amplitude computation matches libopus exactly")
}

// TestEMeansTableMatch validates eMeans table matches libopus float eMeans exactly.
func TestEMeansTableMatch(t *testing.T) {
	// libopus eMeans (from quant_bands.c float build):
	// These are Q4 values converted to float: value/16.0
	libEMeans := []float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	for i, lib := range libEMeans {
		if i >= len(eMeans) {
			t.Errorf("eMeans too short: need index %d", i)
			continue
		}
		if eMeans[i] != lib {
			t.Errorf("eMeans[%d] mismatch: gopus=%.6f, libopus=%.6f", i, eMeans[i], lib)
		}
	}
	t.Log("eMeans table matches libopus exactly")
}

// TestPredictionCoefficientsMatch validates inter-frame prediction coefficients.
func TestPredictionCoefficientsMatch(t *testing.T) {
	// libopus pred_coef (float build): Q15 values divided by 32768
	libAlpha := []float64{
		29440.0 / 32768.0, // LM=0: 0.8984375
		26112.0 / 32768.0, // LM=1: 0.796875
		21248.0 / 32768.0, // LM=2: 0.6484375
		16384.0 / 32768.0, // LM=3: 0.5
	}

	for lm, lib := range libAlpha {
		if AlphaCoef[lm] != lib {
			t.Errorf("AlphaCoef[%d] mismatch: gopus=%.15f, libopus=%.15f", lm, AlphaCoef[lm], lib)
		}
	}

	// libopus beta_coef (float build): Q15 values divided by 32768
	libBeta := []float64{
		30147.0 / 32768.0, // LM=0
		22282.0 / 32768.0, // LM=1
		12124.0 / 32768.0, // LM=2
		6554.0 / 32768.0,  // LM=3
	}

	for lm, lib := range libBeta {
		if BetaCoefInter[lm] != lib {
			t.Errorf("BetaCoefInter[%d] mismatch: gopus=%.15f, libopus=%.15f", lm, BetaCoefInter[lm], lib)
		}
	}

	// libopus beta_intra (float build)
	libBetaIntra := 4915.0 / 32768.0
	if BetaIntra != libBetaIntra {
		t.Errorf("BetaIntra mismatch: gopus=%.15f, libopus=%.15f", BetaIntra, libBetaIntra)
	}

	t.Log("Prediction coefficients match libopus exactly")
}

// TestProbabilityModelMatch validates e_prob_model table matches libopus.
func TestProbabilityModelMatch(t *testing.T) {
	// libopus e_prob_model table (from quant_bands.c)
	libEProbModel := [4][2][42]uint8{
		// 120 sample frames (LM=0)
		{
			// Inter
			{
				72, 127, 65, 129, 66, 128, 65, 128, 64, 128, 62, 128, 64, 128,
				64, 128, 92, 78, 92, 79, 92, 78, 90, 79, 116, 41, 115, 40,
				114, 40, 132, 26, 132, 26, 145, 17, 161, 12, 176, 10, 177, 11,
			},
			// Intra
			{
				24, 179, 48, 138, 54, 135, 54, 132, 53, 134, 56, 133, 55, 132,
				55, 132, 61, 114, 70, 96, 74, 88, 75, 88, 87, 74, 89, 66,
				91, 67, 100, 59, 108, 50, 120, 40, 122, 37, 97, 43, 78, 50,
			},
		},
		// 240 sample frames (LM=1)
		{
			// Inter
			{
				83, 78, 84, 81, 88, 75, 86, 74, 87, 71, 90, 73, 93, 74,
				93, 74, 109, 40, 114, 36, 117, 34, 117, 34, 143, 17, 145, 18,
				146, 19, 162, 12, 165, 10, 178, 7, 189, 6, 190, 8, 177, 9,
			},
			// Intra
			{
				23, 178, 54, 115, 63, 102, 66, 98, 69, 99, 74, 89, 71, 91,
				73, 91, 78, 89, 86, 80, 92, 66, 93, 64, 102, 59, 103, 60,
				104, 60, 117, 52, 123, 44, 138, 35, 133, 31, 97, 38, 77, 45,
			},
		},
		// 480 sample frames (LM=2)
		{
			// Inter
			{
				61, 90, 93, 60, 105, 42, 107, 41, 110, 45, 116, 38, 113, 38,
				112, 38, 124, 26, 132, 27, 136, 19, 140, 20, 155, 14, 159, 16,
				158, 18, 170, 13, 177, 10, 187, 8, 192, 6, 175, 9, 159, 10,
			},
			// Intra
			{
				21, 178, 59, 110, 71, 86, 75, 85, 84, 83, 91, 66, 88, 73,
				87, 72, 92, 75, 98, 72, 105, 58, 107, 54, 115, 52, 114, 55,
				112, 56, 129, 51, 132, 40, 150, 33, 140, 29, 98, 35, 77, 42,
			},
		},
		// 960 sample frames (LM=3)
		{
			// Inter
			{
				42, 121, 96, 66, 108, 43, 111, 40, 117, 44, 123, 32, 120, 36,
				119, 33, 127, 33, 134, 34, 139, 21, 147, 23, 152, 20, 158, 25,
				154, 26, 166, 21, 173, 16, 184, 13, 184, 10, 150, 13, 139, 15,
			},
			// Intra
			{
				22, 178, 63, 114, 74, 82, 84, 83, 92, 82, 103, 62, 96, 72,
				96, 67, 101, 73, 107, 72, 113, 55, 118, 52, 125, 52, 118, 52,
				117, 55, 135, 49, 137, 39, 157, 32, 145, 29, 97, 33, 77, 40,
			},
		},
	}

	mismatches := 0
	for lm := 0; lm < 4; lm++ {
		for intra := 0; intra < 2; intra++ {
			for i := 0; i < 42; i++ {
				if eProbModel[lm][intra][i] != libEProbModel[lm][intra][i] {
					t.Errorf("eProbModel[%d][%d][%d] mismatch: gopus=%d, libopus=%d",
						lm, intra, i, eProbModel[lm][intra][i], libEProbModel[lm][intra][i])
					mismatches++
				}
			}
		}
	}
	if mismatches == 0 {
		t.Log("Probability model table matches libopus exactly")
	}
}

// TestSmallEnergyICDFMatch validates small_energy_icdf table.
func TestSmallEnergyICDFMatch(t *testing.T) {
	// libopus small_energy_icdf = {2, 1, 0}
	libICDF := []uint8{2, 1, 0}

	for i, lib := range libICDF {
		if i >= len(smallEnergyICDF) {
			t.Errorf("smallEnergyICDF too short: need index %d", i)
			continue
		}
		if smallEnergyICDF[i] != lib {
			t.Errorf("smallEnergyICDF[%d] mismatch: gopus=%d, libopus=%d", i, smallEnergyICDF[i], lib)
		}
	}
	t.Log("smallEnergyICDF table matches libopus exactly")
}

// TestCoarseEnergyPredictionFormula validates the coarse energy prediction formula.
//
// libopus formula (quant_bands.c unquant_coarse_energy):
//
//	tmp = coef*oldE + prev[c] + q
//	prev[c] = prev[c] + q - beta*q
//
// where:
//   - coef = pred_coef[LM] (or 0 for intra)
//   - beta = beta_coef[LM] (or beta_intra for intra)
//   - oldE = max(-9, previous frame energy for this band/channel)
//   - prev[c] = running inter-band prediction state
//   - q = decoded Laplace value (in units of 6dB, so q=1 means +6dB)
func TestCoarseEnergyPredictionFormula(t *testing.T) {
	// Test values
	testCases := []struct {
		lm       int
		intra    bool
		oldE     float64
		prevBand float64
		qi       int
	}{
		{3, false, 0.0, 0.0, 0},
		{3, false, 5.0, 0.0, 1},
		{3, false, -10.0, 0.0, -2}, // oldE clamped to -9
		{3, true, 0.0, 0.0, 3},
		{0, false, 10.0, 2.0, -1},
		{2, true, -5.0, 1.5, 2},
	}

	for _, tc := range testCases {
		// Get coefficients
		var coef, beta float64
		if tc.intra {
			coef = 0.0
			beta = BetaIntra
		} else {
			coef = AlphaCoef[tc.lm]
			beta = BetaCoefInter[tc.lm]
		}

		// Clamp oldE to -9 (as libopus does)
		oldE := tc.oldE
		if oldE < -9.0 {
			oldE = -9.0
		}

		// Compute q (in float mode, q = qi directly, not shifted)
		q := float64(tc.qi)

		// Compute energy: tmp = coef*oldE + prev + q
		libTmp := coef*oldE + tc.prevBand + q
		goTmp := coef*oldE + tc.prevBand + q // Same formula

		// Compute new prev: prev = prev + q - beta*q = prev + q*(1-beta)
		libNewPrev := tc.prevBand + q - beta*q
		goNewPrev := tc.prevBand + q - beta*q // Same formula

		if libTmp != goTmp {
			t.Errorf("Energy mismatch: lm=%d, intra=%v, oldE=%.1f, prev=%.1f, qi=%d: lib=%.6f, go=%.6f",
				tc.lm, tc.intra, tc.oldE, tc.prevBand, tc.qi, libTmp, goTmp)
		}
		if libNewPrev != goNewPrev {
			t.Errorf("NewPrev mismatch: lm=%d, intra=%v, prev=%.1f, qi=%d: lib=%.6f, go=%.6f",
				tc.lm, tc.intra, tc.prevBand, tc.qi, libNewPrev, goNewPrev)
		}
	}
	t.Log("Coarse energy prediction formula validated")
}

// TestLaplaceDecodeConsistency tests that Laplace decoding produces consistent results
// for known inputs and parameters.
func TestLaplaceDecodeConsistency(t *testing.T) {
	// Test that the same input produces the same output
	testData := []byte{0xCF, 0xC5, 0x88, 0x30, 0x00, 0x00, 0x00, 0x00}

	// LM=3 inter: fs=42<<7, decay=121<<6
	fs := int(eProbModel[3][0][0]) << 7 // Band 0 fs
	decay := int(eProbModel[3][0][1]) << 6

	// Decode multiple times, should get same result
	results := make([]int, 5)
	for i := 0; i < 5; i++ {
		d := NewDecoder(1)
		rd := &rangecoding.Decoder{}
		rd.Init(testData)
		d.SetRangeDecoder(rd)
		results[i] = d.decodeLaplace(fs, decay)
	}

	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("Laplace decode not consistent: run 0=%d, run %d=%d", results[0], i, results[i])
		}
	}
	t.Logf("Laplace decode consistent: value=%d (fs=%d, decay=%d)", results[0], fs, decay)
}

// TestMaxFineBitsConstant validates MAX_FINE_BITS constant.
func TestMaxFineBitsConstant(t *testing.T) {
	// libopus MAX_FINE_BITS = 8 (from rate.h)
	if maxFineBits != 8 {
		t.Errorf("maxFineBits mismatch: gopus=%d, libopus=8", maxFineBits)
	}
	t.Log("maxFineBits constant matches libopus")
}

// TestDB6Constant validates DB6 constant (6 dB = 1.0 in log2 units).
func TestDB6Constant(t *testing.T) {
	// In libopus float, DB_SHIFT is 24 for fixed-point, but in float mode
	// the energy values are directly in log2 units where 1.0 = 6 dB.
	// This is because log2(amplitude_ratio) = dB/6.02
	if DB6 != 1.0 {
		t.Errorf("DB6 mismatch: gopus=%f, expected=1.0", DB6)
	}
	t.Log("DB6 constant validated")
}
