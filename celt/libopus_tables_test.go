// libopus_tables_test.go - Validates CELT tables against libopus 1.6.1
//
// Reference: tmp_check/opus-1.6.1/celt/ (static_modes_float.h, modes.c, quant_bands.c, rate.c, celt.h)
//
// Acceptance criteria: exact value match (bit-for-bit for integers, exact float match)

package celt

import (
	"math"
	"testing"
)

// TestEBands validates band boundaries against libopus eband5ms table
// Reference: libopus celt/modes.c eband5ms
func TestEBandsMatchLibopus(t *testing.T) {
	// From libopus celt/modes.c:
	// static const opus_int16 eband5ms[] = {
	// /*0  200 400 600 800  1k 1.2 1.4 1.6  2k 2.4 2.8 3.2  4k 4.8 5.6 6.8  8k 9.6 12k 15.6 */
	//   0,  1,  2,  3,  4,  5,  6,  7,  8, 10, 12, 14, 16, 20, 24, 28, 34, 40, 48, 60, 78, 100
	// };
	libopusEBands := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
		12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
		78, 100,
	}

	if len(EBands) != len(libopusEBands) {
		t.Fatalf("EBands length mismatch: got %d, want %d", len(EBands), len(libopusEBands))
	}

	for i, expected := range libopusEBands {
		if EBands[i] != expected {
			t.Errorf("EBands[%d] = %d, want %d (libopus eband5ms)", i, EBands[i], expected)
		}
	}
}

// TestLogNMatchLibopus validates log band widths against libopus logN400
// Reference: libopus celt/static_modes_float.h logN400
func TestLogNMatchLibopus(t *testing.T) {
	// From libopus celt/static_modes_float.h:
	// static const opus_int16 logN400[21] = {
	// 0, 0, 0, 0, 0, 0, 0, 0, 8, 8, 8, 8, 16, 16, 16, 21, 21, 24, 29, 34, 36, };
	libopusLogN := []int{
		0, 0, 0, 0, 0, 0, 0, 0, 8, 8, 8, 8, 16, 16, 16, 21, 21, 24, 29, 34, 36,
	}

	if len(LogN) != len(libopusLogN) {
		t.Fatalf("LogN length mismatch: got %d, want %d", len(LogN), len(libopusLogN))
	}

	for i, expected := range libopusLogN {
		if LogN[i] != expected {
			t.Errorf("LogN[%d] = %d, want %d (libopus logN400)", i, LogN[i], expected)
		}
	}
}

// TestBandAllocMatchLibopus validates band allocation table against libopus
// Reference: libopus celt/modes.c band_allocation
func TestBandAllocMatchLibopus(t *testing.T) {
	// From libopus celt/modes.c:
	// static const unsigned char band_allocation[] = {
	libopusBandAlloc := [11][21]int{
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{90, 80, 75, 69, 63, 56, 49, 40, 34, 29, 20, 18, 10, 0, 0, 0, 0, 0, 0, 0, 0},
		{110, 100, 90, 84, 78, 71, 65, 58, 51, 45, 39, 32, 26, 20, 12, 0, 0, 0, 0, 0, 0},
		{118, 110, 103, 93, 86, 80, 75, 70, 65, 59, 53, 47, 40, 31, 23, 15, 4, 0, 0, 0, 0},
		{126, 119, 112, 104, 95, 89, 83, 78, 72, 66, 60, 54, 47, 39, 32, 25, 17, 12, 1, 0, 0},
		{134, 127, 120, 114, 103, 97, 91, 85, 78, 72, 66, 60, 54, 47, 41, 35, 29, 23, 16, 10, 1},
		{144, 137, 130, 124, 113, 107, 101, 95, 88, 82, 76, 70, 64, 57, 51, 45, 39, 33, 26, 15, 1},
		{152, 145, 138, 132, 123, 117, 111, 105, 98, 92, 86, 80, 74, 67, 61, 55, 49, 43, 36, 20, 1},
		{162, 155, 148, 142, 133, 127, 121, 115, 108, 102, 96, 90, 84, 77, 71, 65, 59, 53, 46, 30, 1},
		{172, 165, 158, 152, 143, 137, 131, 125, 118, 112, 106, 100, 94, 87, 81, 75, 69, 63, 56, 45, 20},
		{200, 200, 200, 200, 200, 200, 200, 200, 198, 193, 188, 183, 178, 173, 168, 163, 158, 153, 148, 129, 104},
	}

	for q := 0; q < 11; q++ {
		for b := 0; b < 21; b++ {
			if BandAlloc[q][b] != libopusBandAlloc[q][b] {
				t.Errorf("BandAlloc[%d][%d] = %d, want %d (libopus band_allocation)", q, b, BandAlloc[q][b], libopusBandAlloc[q][b])
			}
		}
	}
}

// TestCacheIndex50MatchLibopus validates pulse cache index against libopus
// Reference: libopus celt/static_modes_float.h cache_index50
func TestCacheIndex50MatchLibopus(t *testing.T) {
	// From libopus celt/static_modes_float.h:
	// static const opus_int16 cache_index50[105] = {
	libopusCacheIndex := []int16{
		-1, -1, -1, -1, -1, -1, -1, -1, 0, 0, 0, 0, 41, 41, 41,
		82, 82, 123, 164, 200, 222, 0, 0, 0, 0, 0, 0, 0, 0, 41,
		41, 41, 41, 123, 123, 123, 164, 164, 240, 266, 283, 295, 41, 41, 41,
		41, 41, 41, 41, 41, 123, 123, 123, 123, 240, 240, 240, 266, 266, 305,
		318, 328, 336, 123, 123, 123, 123, 123, 123, 123, 123, 240, 240, 240, 240,
		305, 305, 305, 318, 318, 343, 351, 358, 364, 240, 240, 240, 240, 240, 240,
		240, 240, 305, 305, 305, 305, 343, 343, 343, 351, 351, 370, 376, 382, 387,
	}

	if len(cacheIndex50) != len(libopusCacheIndex) {
		t.Fatalf("cacheIndex50 length mismatch: got %d, want %d", len(cacheIndex50), len(libopusCacheIndex))
	}

	for i, expected := range libopusCacheIndex {
		if cacheIndex50[i] != expected {
			t.Errorf("cacheIndex50[%d] = %d, want %d (libopus cache_index50)", i, cacheIndex50[i], expected)
		}
	}
}

// TestCacheBits50MatchLibopus validates pulse cache bits against libopus
// Reference: libopus celt/static_modes_float.h cache_bits50
func TestCacheBits50MatchLibopus(t *testing.T) {
	// From libopus celt/static_modes_float.h:
	// static const unsigned char cache_bits50[392] = {
	libopusCacheBits := []uint8{
		40, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
		7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
		7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 40, 15, 23, 28,
		31, 34, 36, 38, 39, 41, 42, 43, 44, 45, 46, 47, 47, 49, 50,
		51, 52, 53, 54, 55, 55, 57, 58, 59, 60, 61, 62, 63, 63, 65,
		66, 67, 68, 69, 70, 71, 71, 40, 20, 33, 41, 48, 53, 57, 61,
		64, 66, 69, 71, 73, 75, 76, 78, 80, 82, 85, 87, 89, 91, 92,
		94, 96, 98, 101, 103, 105, 107, 108, 110, 112, 114, 117, 119, 121, 123,
		124, 126, 128, 40, 23, 39, 51, 60, 67, 73, 79, 83, 87, 91, 94,
		97, 100, 102, 105, 107, 111, 115, 118, 121, 124, 126, 129, 131, 135, 139,
		142, 145, 148, 150, 153, 155, 159, 163, 166, 169, 172, 174, 177, 179, 35,
		28, 49, 65, 78, 89, 99, 107, 114, 120, 126, 132, 136, 141, 145, 149,
		153, 159, 165, 171, 176, 180, 185, 189, 192, 199, 205, 211, 216, 220, 225,
		229, 232, 239, 245, 251, 21, 33, 58, 79, 97, 112, 125, 137, 148, 157,
		166, 174, 182, 189, 195, 201, 207, 217, 227, 235, 243, 251, 17, 35, 63,
		86, 106, 123, 139, 152, 165, 177, 187, 197, 206, 214, 222, 230, 237, 250,
		25, 31, 55, 75, 91, 105, 117, 128, 138, 146, 154, 161, 168, 174, 180,
		185, 190, 200, 208, 215, 222, 229, 235, 240, 245, 255, 16, 36, 65, 89,
		110, 128, 144, 159, 173, 185, 196, 207, 217, 226, 234, 242, 250, 11, 41,
		74, 103, 128, 151, 172, 191, 209, 225, 241, 255, 9, 43, 79, 110, 138,
		163, 186, 207, 227, 246, 12, 39, 71, 99, 123, 144, 164, 182, 198, 214,
		228, 241, 253, 9, 44, 81, 113, 142, 168, 192, 214, 235, 255, 7, 49,
		90, 127, 160, 191, 220, 247, 6, 51, 95, 134, 170, 203, 234, 7, 47,
		87, 123, 155, 184, 212, 237, 6, 52, 97, 137, 174, 208, 240, 5, 57,
		106, 151, 192, 231, 5, 59, 111, 158, 202, 243, 5, 55, 103, 147, 187,
		224, 5, 60, 113, 161, 206, 248, 4, 65, 122, 175, 224, 4, 67, 127,
		182, 234,
	}

	if len(cacheBits50) != len(libopusCacheBits) {
		t.Fatalf("cacheBits50 length mismatch: got %d, want %d", len(cacheBits50), len(libopusCacheBits))
	}

	for i, expected := range libopusCacheBits {
		if cacheBits50[i] != expected {
			t.Errorf("cacheBits50[%d] = %d, want %d (libopus cache_bits50)", i, cacheBits50[i], expected)
		}
	}
}

// TestCacheCaps50MatchLibopus validates pulse cache caps against libopus
// Reference: libopus celt/static_modes_float.h cache_caps50
func TestCacheCaps50MatchLibopus(t *testing.T) {
	// From libopus celt/static_modes_float.h:
	// static const unsigned char cache_caps50[168] = {
	libopusCacheCaps := []uint8{
		224, 224, 224, 224, 224, 224, 224, 224, 160, 160, 160, 160, 185, 185, 185,
		178, 178, 168, 134, 61, 37, 224, 224, 224, 224, 224, 224, 224, 224, 240,
		240, 240, 240, 207, 207, 207, 198, 198, 183, 144, 66, 40, 160, 160, 160,
		160, 160, 160, 160, 160, 185, 185, 185, 185, 193, 193, 193, 183, 183, 172,
		138, 64, 38, 240, 240, 240, 240, 240, 240, 240, 240, 207, 207, 207, 207,
		204, 204, 204, 193, 193, 180, 143, 66, 40, 185, 185, 185, 185, 185, 185,
		185, 185, 193, 193, 193, 193, 193, 193, 193, 183, 183, 172, 138, 65, 39,
		207, 207, 207, 207, 207, 207, 207, 207, 204, 204, 204, 204, 201, 201, 201,
		188, 188, 176, 141, 66, 40, 193, 193, 193, 193, 193, 193, 193, 193, 193,
		193, 193, 193, 194, 194, 194, 184, 184, 173, 139, 65, 39, 204, 204, 204,
		204, 204, 204, 204, 204, 201, 201, 201, 201, 198, 198, 198, 187, 187, 175,
		140, 66, 40,
	}

	if len(cacheCaps) != len(libopusCacheCaps) {
		t.Fatalf("cacheCaps length mismatch: got %d, want %d", len(cacheCaps), len(libopusCacheCaps))
	}

	for i, expected := range libopusCacheCaps {
		if cacheCaps[i] != expected {
			t.Errorf("cacheCaps[%d] = %d, want %d (libopus cache_caps50)", i, cacheCaps[i], expected)
		}
	}
}

// TestEMeansMatchLibopus validates energy means table against libopus
// Reference: libopus celt/quant_bands.c eMeans (float version)
func TestEMeansMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c (float version):
	// const opus_val16 eMeans[25] = {
	//   6.437500f, 6.250000f, 5.750000f, 5.312500f, 5.062500f,
	//   4.812500f, 4.500000f, 4.375000f, 4.875000f, 4.687500f,
	//   4.562500f, 4.437500f, 4.875000f, 4.625000f, 4.312500f,
	//   4.500000f, 4.375000f, 4.625000f, 4.750000f, 4.437500f,
	//   3.750000f, 3.750000f, 3.750000f, 3.750000f, 3.750000f
	// };
	libopusEMeans := []float64{
		6.437500, 6.250000, 5.750000, 5.312500, 5.062500,
		4.812500, 4.500000, 4.375000, 4.875000, 4.687500,
		4.562500, 4.437500, 4.875000, 4.625000, 4.312500,
		4.500000, 4.375000, 4.625000, 4.750000, 4.437500,
		3.750000, 3.750000, 3.750000, 3.750000, 3.750000,
	}

	if len(eMeans) != len(libopusEMeans) {
		t.Fatalf("eMeans length mismatch: got %d, want %d", len(eMeans), len(libopusEMeans))
	}

	for i, expected := range libopusEMeans {
		if eMeans[i] != expected {
			t.Errorf("eMeans[%d] = %v, want %v (libopus eMeans)", i, eMeans[i], expected)
		}
	}
}

// TestPredCoefMatchLibopus validates prediction coefficients against libopus
// Reference: libopus celt/quant_bands.c pred_coef (AlphaCoef in Go)
func TestPredCoefMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c:
	// static const opus_val16 pred_coef[4] = {29440/32768., 26112/32768., 21248/32768., 16384/32768.};
	libopusPredCoef := []float64{
		29440.0 / 32768.0,
		26112.0 / 32768.0,
		21248.0 / 32768.0,
		16384.0 / 32768.0,
	}

	for lm := 0; lm < 4; lm++ {
		if AlphaCoef[lm] != libopusPredCoef[lm] {
			t.Errorf("AlphaCoef[%d] = %v, want %v (libopus pred_coef)", lm, AlphaCoef[lm], libopusPredCoef[lm])
		}
	}
}

// TestBetaCoefMatchLibopus validates beta coefficients against libopus
// Reference: libopus celt/quant_bands.c beta_coef
func TestBetaCoefMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c:
	// static const opus_val16 beta_coef[4] = {30147/32768., 22282/32768., 12124/32768., 6554/32768.};
	libopusBetaCoef := []float64{
		30147.0 / 32768.0,
		22282.0 / 32768.0,
		12124.0 / 32768.0,
		6554.0 / 32768.0,
	}

	for lm := 0; lm < 4; lm++ {
		if BetaCoefInter[lm] != libopusBetaCoef[lm] {
			t.Errorf("BetaCoefInter[%d] = %v, want %v (libopus beta_coef)", lm, BetaCoefInter[lm], libopusBetaCoef[lm])
		}
	}
}

// TestBetaIntraMatchLibopus validates beta intra coefficient against libopus
// Reference: libopus celt/quant_bands.c beta_intra
func TestBetaIntraMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c:
	// static const opus_val16 beta_intra = 4915/32768.;
	libopusBetaIntra := 4915.0 / 32768.0

	if BetaIntra != libopusBetaIntra {
		t.Errorf("BetaIntra = %v, want %v (libopus beta_intra)", BetaIntra, libopusBetaIntra)
	}
}

// TestEProbModelMatchLibopus validates energy probability model against libopus
// Reference: libopus celt/quant_bands.c e_prob_model
func TestEProbModelMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c:
	// static const unsigned char e_prob_model[4][2][42] = {
	libopusEProbModel := [4][2][42]uint8{
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

	for lm := 0; lm < 4; lm++ {
		for intra := 0; intra < 2; intra++ {
			for i := 0; i < 42; i++ {
				if eProbModel[lm][intra][i] != libopusEProbModel[lm][intra][i] {
					t.Errorf("eProbModel[%d][%d][%d] = %d, want %d (libopus e_prob_model)",
						lm, intra, i, eProbModel[lm][intra][i], libopusEProbModel[lm][intra][i])
				}
			}
		}
	}
}

// TestSmallEnergyICDFMatchLibopus validates small energy ICDF against libopus
// Reference: libopus celt/quant_bands.c small_energy_icdf
func TestSmallEnergyICDFMatchLibopus(t *testing.T) {
	// From libopus celt/quant_bands.c:
	// static const unsigned char small_energy_icdf[3]={2,1,0};
	libopusSmallEnergyICDF := []uint8{2, 1, 0}

	if len(smallEnergyICDF) != len(libopusSmallEnergyICDF) {
		t.Fatalf("smallEnergyICDF length mismatch: got %d, want %d", len(smallEnergyICDF), len(libopusSmallEnergyICDF))
	}

	for i, expected := range libopusSmallEnergyICDF {
		if smallEnergyICDF[i] != expected {
			t.Errorf("smallEnergyICDF[%d] = %d, want %d (libopus small_energy_icdf)", i, smallEnergyICDF[i], expected)
		}
	}
}

// TestLog2FracTableMatchLibopus validates log2 fraction table against libopus
// Reference: libopus celt/rate.c LOG2_FRAC_TABLE
func TestLog2FracTableMatchLibopus(t *testing.T) {
	// From libopus celt/rate.c:
	// static const unsigned char LOG2_FRAC_TABLE[24]={
	//    0,
	//    8,13,
	//   16,19,21,23,
	//   24,26,27,28,29,30,31,32,
	//   32,33,34,34,35,36,36,37,37
	// };
	libopusLog2FracTable := []uint8{
		0, 8, 13, 16, 19, 21, 23, 24, 26, 27, 28, 29,
		30, 31, 32, 32, 33, 34, 34, 35, 36, 36, 37, 37,
	}

	if len(log2FracTable) != len(libopusLog2FracTable) {
		t.Fatalf("log2FracTable length mismatch: got %d, want %d", len(log2FracTable), len(libopusLog2FracTable))
	}

	for i, expected := range libopusLog2FracTable {
		if log2FracTable[i] != expected {
			t.Errorf("log2FracTable[%d] = %d, want %d (libopus LOG2_FRAC_TABLE)", i, log2FracTable[i], expected)
		}
	}
}

// TestTrimICDFMatchLibopus validates trim ICDF against libopus
// Reference: libopus celt/celt.h trim_icdf
func TestTrimICDFMatchLibopus(t *testing.T) {
	// From libopus celt/celt.h:
	// static const unsigned char trim_icdf[11] = {126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0};
	libopusTrimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}

	if len(trimICDF) != len(libopusTrimICDF) {
		t.Fatalf("trimICDF length mismatch: got %d, want %d", len(trimICDF), len(libopusTrimICDF))
	}

	for i, expected := range libopusTrimICDF {
		if trimICDF[i] != expected {
			t.Errorf("trimICDF[%d] = %d, want %d (libopus trim_icdf)", i, trimICDF[i], expected)
		}
	}
}

// TestSpreadICDFMatchLibopus validates spread ICDF against libopus
// Reference: libopus celt/celt.h spread_icdf
func TestSpreadICDFMatchLibopus(t *testing.T) {
	// From libopus celt/celt.h:
	// static const unsigned char spread_icdf[4] = {25, 23, 2, 0};
	libopusSpreadICDF := []uint8{25, 23, 2, 0}

	if len(spreadICDF) != len(libopusSpreadICDF) {
		t.Fatalf("spreadICDF length mismatch: got %d, want %d", len(spreadICDF), len(libopusSpreadICDF))
	}

	for i, expected := range libopusSpreadICDF {
		if spreadICDF[i] != expected {
			t.Errorf("spreadICDF[%d] = %d, want %d (libopus spread_icdf)", i, spreadICDF[i], expected)
		}
	}
}

// TestTapsetICDFMatchLibopus validates tapset ICDF against libopus
// Reference: libopus celt/celt.h tapset_icdf
func TestTapsetICDFMatchLibopus(t *testing.T) {
	// From libopus celt/celt.h:
	// static const unsigned char tapset_icdf[3]={2,1,0};
	libopusTapsetICDF := []uint8{2, 1, 0}

	if len(tapsetICDF) != len(libopusTapsetICDF) {
		t.Fatalf("tapsetICDF length mismatch: got %d, want %d", len(tapsetICDF), len(libopusTapsetICDF))
	}

	for i, expected := range libopusTapsetICDF {
		if tapsetICDF[i] != expected {
			t.Errorf("tapsetICDF[%d] = %d, want %d (libopus tapset_icdf)", i, tapsetICDF[i], expected)
		}
	}
}

// TestTFSelectTableMatchLibopus validates TF select table against libopus
// Reference: libopus celt/celt.c tf_select_table
func TestTFSelectTableMatchLibopus(t *testing.T) {
	// From libopus celt/celt.c:
	// const signed char tf_select_table[4][8] = {
	//     /*isTransient=0     isTransient=1 */
	//       {0, -1, 0, -1,    0,-1, 0,-1}, /* 2.5 ms */
	//       {0, -1, 0, -2,    1, 0, 1,-1}, /* 5 ms */
	//       {0, -2, 0, -3,    2, 0, 1,-1}, /* 10 ms */
	//       {0, -2, 0, -3,    3, 0, 1,-1}, /* 20 ms */
	// };
	libopusTFSelectTable := [4][8]int8{
		{0, -1, 0, -1, 0, -1, 0, -1},
		{0, -1, 0, -2, 1, 0, 1, -1},
		{0, -2, 0, -3, 2, 0, 1, -1},
		{0, -2, 0, -3, 3, 0, 1, -1},
	}

	for lm := 0; lm < 4; lm++ {
		for i := 0; i < 8; i++ {
			if tfSelectTable[lm][i] != libopusTFSelectTable[lm][i] {
				t.Errorf("tfSelectTable[%d][%d] = %d, want %d (libopus tf_select_table)",
					lm, i, tfSelectTable[lm][i], libopusTFSelectTable[lm][i])
			}
		}
	}
}

// TestVorbisWindowMatchLibopus validates window values against libopus
// Reference: libopus celt/modes.c window generation formula
// Window: sin(0.5*pi * sin(0.5*pi*(i+0.5)/overlap)^2)
func TestVorbisWindowMatchLibopus(t *testing.T) {
	overlap := 120 // Standard CELT overlap

	// Verify formula matches libopus:
	// for (i=0;i<mode->overlap;i++)
	//    window[i] = Q15ONE*sin(.5*M_PI* sin(.5*M_PI*(i+.5)/mode->overlap) * sin(.5*M_PI*(i+.5)/mode->overlap));

	for i := 0; i < overlap; i++ {
		x := float64(i) + 0.5
		sinArg := 0.5 * math.Pi * x / float64(overlap)
		s := math.Sin(sinArg)
		expected := math.Sin(0.5 * math.Pi * s * s)
		got := VorbisWindow(i, overlap)

		if math.Abs(got-expected) > 1e-15 {
			t.Errorf("VorbisWindow(%d, %d) = %v, want %v", i, overlap, got, expected)
		}
	}

	// Verify power-complementary property: w[i]^2 + w[overlap-1-i]^2 = 1
	for i := 0; i < overlap/2; i++ {
		w1 := VorbisWindow(i, overlap)
		w2 := VorbisWindow(overlap-1-i, overlap)
		sum := w1*w1 + w2*w2
		if math.Abs(sum-1.0) > 1e-14 {
			t.Errorf("Window power complementary check failed at i=%d: w[%d]^2 + w[%d]^2 = %v, want 1.0",
				i, i, overlap-1-i, sum)
		}
	}
}

// TestModeConfigMatchLibopus validates mode configurations against libopus
// Reference: libopus uses these values for LM, short blocks, etc.
func TestModeConfigMatchLibopus(t *testing.T) {
	testCases := []struct {
		frameSize   int
		expectedLM  int
		shortBlocks int
	}{
		{120, 0, 1}, // 2.5ms
		{240, 1, 2}, // 5ms
		{480, 2, 4}, // 10ms
		{960, 3, 8}, // 20ms
	}

	for _, tc := range testCases {
		cfg := GetModeConfig(tc.frameSize)
		if cfg.LM != tc.expectedLM {
			t.Errorf("GetModeConfig(%d).LM = %d, want %d", tc.frameSize, cfg.LM, tc.expectedLM)
		}
		if cfg.ShortBlocks != tc.shortBlocks {
			t.Errorf("GetModeConfig(%d).ShortBlocks = %d, want %d", tc.frameSize, cfg.ShortBlocks, tc.shortBlocks)
		}
		if cfg.FrameSize != tc.frameSize {
			t.Errorf("GetModeConfig(%d).FrameSize = %d, want %d", tc.frameSize, cfg.FrameSize, tc.frameSize)
		}
	}
}

// TestMaxBandsMatchLibopus validates MaxBands constant
// Reference: libopus defines 21 bands for 48kHz standard mode
func TestMaxBandsMatchLibopus(t *testing.T) {
	// sizeof(eband5ms)/sizeof(eband5ms[0])-1 = 22 - 1 = 21
	expectedMaxBands := 21
	if MaxBands != expectedMaxBands {
		t.Errorf("MaxBands = %d, want %d (libopus sizeof(eband5ms)/sizeof(eband5ms[0])-1)", MaxBands, expectedMaxBands)
	}
}

// TestOverlapMatchLibopus validates Overlap constant
// Reference: libopus uses 120 samples at 48kHz (2.5ms)
func TestOverlapMatchLibopus(t *testing.T) {
	expectedOverlap := 120
	if Overlap != expectedOverlap {
		t.Errorf("Overlap = %d, want %d (libopus overlap for 48kHz)", Overlap, expectedOverlap)
	}
}

// TestSmallDivTableCorrectness validates SmallDiv lookup table
// NOTE: SmallDiv is NOT from libopus - it's a Go implementation utility table
// for fast integer division. We just verify it's reasonable for its purpose.
// SmallDiv[i] should approximate (1 << 16) / (i + 1) with rounding up.
func TestSmallDivTableCorrectness(t *testing.T) {
	// SmallDiv is used for fast division: (x * SmallDiv[d]) >> 16 ~= x / (d+1)
	// The table uses ceiling division: (2^16 + d) / (d + 1)
	// This is NOT a libopus table, so we just verify it's self-consistent

	// Verify length matches documented size (129 entries for d=0..128)
	if len(SmallDiv) != 129 {
		t.Errorf("SmallDiv length = %d, want 129", len(SmallDiv))
	}

	// Verify monotonically decreasing (since 1/(d+1) decreases as d increases)
	for i := 1; i < len(SmallDiv); i++ {
		if SmallDiv[i] > SmallDiv[i-1] {
			t.Errorf("SmallDiv not monotonically decreasing at i=%d: SmallDiv[%d]=%d > SmallDiv[%d]=%d",
				i, i, SmallDiv[i], i-1, SmallDiv[i-1])
		}
	}

	// Verify reasonable bounds
	if SmallDiv[0] != 65535 {
		t.Errorf("SmallDiv[0] = %d, want 65535 (representing 1/1)", SmallDiv[0])
	}

	// Verify last entry is reasonable (512 = 65536/128 = 512)
	expectedLast := uint16(65536 / 129) // floor division
	if SmallDiv[128] < expectedLast || SmallDiv[128] > expectedLast+1 {
		t.Errorf("SmallDiv[128] = %d, want ~%d (representing 1/129)", SmallDiv[128], expectedLast)
	}
}

// Summary test that runs all validations and reports overall status
func TestAllTablesMatchLibopus(t *testing.T) {
	tests := []struct {
		name string
		test func(*testing.T)
	}{
		{"EBands", TestEBandsMatchLibopus},
		{"LogN", TestLogNMatchLibopus},
		{"BandAlloc", TestBandAllocMatchLibopus},
		{"CacheIndex50", TestCacheIndex50MatchLibopus},
		{"CacheBits50", TestCacheBits50MatchLibopus},
		{"CacheCaps50", TestCacheCaps50MatchLibopus},
		{"EMeans", TestEMeansMatchLibopus},
		{"PredCoef", TestPredCoefMatchLibopus},
		{"BetaCoef", TestBetaCoefMatchLibopus},
		{"BetaIntra", TestBetaIntraMatchLibopus},
		{"EProbModel", TestEProbModelMatchLibopus},
		{"SmallEnergyICDF", TestSmallEnergyICDFMatchLibopus},
		{"Log2FracTable", TestLog2FracTableMatchLibopus},
		{"TrimICDF", TestTrimICDFMatchLibopus},
		{"SpreadICDF", TestSpreadICDFMatchLibopus},
		{"TapsetICDF", TestTapsetICDFMatchLibopus},
		{"TFSelectTable", TestTFSelectTableMatchLibopus},
		{"VorbisWindow", TestVorbisWindowMatchLibopus},
		{"ModeConfig", TestModeConfigMatchLibopus},
		{"MaxBands", TestMaxBandsMatchLibopus},
		{"Overlap", TestOverlapMatchLibopus},
		{"SmallDiv (not libopus)", TestSmallDivTableCorrectness},
	}

	passed := 0
	failed := 0

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
			if t.Failed() {
				failed++
			} else {
				passed++
			}
		})
	}

	t.Logf("Tables validation summary: %d passed, %d failed", passed, failed)
}
