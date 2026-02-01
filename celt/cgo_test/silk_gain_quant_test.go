//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides tests for SILK gain quantization comparison.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/silk"
)

// TestLibopusGainConstants verifies the gain quantization constants
func TestLibopusGainConstants(t *testing.T) {
	offset := GainGetOffset()
	scaleQ16 := GainGetScaleQ16()
	invScaleQ16 := GainGetInvScaleQ16()
	nLevels := GainGetNLevels()
	minDelta := GainGetMinDelta()
	maxDelta := GainGetMaxDelta()

	t.Logf("OFFSET = %d", offset)
	t.Logf("SCALE_Q16 = %d", scaleQ16)
	t.Logf("INV_SCALE_Q16 = %d", invScaleQ16)
	t.Logf("N_LEVELS_QGAIN = %d", nLevels)
	t.Logf("MIN_DELTA_GAIN_QUANT = %d", minDelta)
	t.Logf("MAX_DELTA_GAIN_QUANT = %d", maxDelta)

	// Verify expected values
	if offset != 2090 {
		t.Errorf("OFFSET mismatch: got %d, want 2090", offset)
	}
	if nLevels != 64 {
		t.Errorf("N_LEVELS_QGAIN mismatch: got %d, want 64", nLevels)
	}
	if minDelta != -4 {
		t.Errorf("MIN_DELTA_GAIN_QUANT mismatch: got %d, want -4", minDelta)
	}
	if maxDelta != 36 {
		t.Errorf("MAX_DELTA_GAIN_QUANT mismatch: got %d, want 36", maxDelta)
	}
}

// TestSilkLin2LogAgainstLibopus compares our silkLin2Log with libopus
func TestSilkLin2LogAgainstLibopus(t *testing.T) {
	testCases := []int32{
		1, 10, 100, 1000, 10000, 65536, 100000, 1000000,
		81, 17830, // GainDequantTable[0] and [63]
		320, 1256, 4935, // Some mid-range values
	}

	for _, in := range testCases {
		libopusResult := GainSilkLin2Log(in)
		goResult := silk.SilkLin2LogExport(in)

		if libopusResult != goResult {
			t.Errorf("silkLin2Log(%d): libopus=%d, gopus=%d", in, libopusResult, goResult)
		}
	}
}

// TestSilkLog2LinAgainstLibopus compares our silkLog2Lin with libopus
func TestSilkLog2LinAgainstLibopus(t *testing.T) {
	testCases := []int32{
		0, 128, 256, 512, 1024, 2048, 3000, 3967,
		2090, 2218, 2346, // Values around typical gain ranges
	}

	for _, in := range testCases {
		libopusResult := GainSilkLog2Lin(in)
		goResult := silk.SilkLog2LinExport(in)

		if libopusResult != goResult {
			t.Errorf("silkLog2Lin(%d): libopus=%d, gopus=%d", in, libopusResult, goResult)
		}
	}
}

// TestGainQuantizationAgainstLibopus compares gain quantization
func TestGainQuantizationAgainstLibopus(t *testing.T) {
	// Test with all values from GainDequantTable
	for idx := 0; idx < 64; idx++ {
		gainQ16 := silk.GainDequantTable[idx]

		// Quantize with libopus
		libopusIdx := GainQuantizeSingle(gainQ16)

		// Quantize with gopus (fixed version)
		gopusIdx := silk.ComputeLogGainIndexQ16Export(gainQ16)

		if libopusIdx != gopusIdx {
			t.Errorf("Gain index %d (Q16=%d): libopus=%d, gopus=%d",
				idx, gainQ16, libopusIdx, gopusIdx)
		}

		// Verify round-trip is close (quantization has inherent error)
		if absGain(libopusIdx-idx) > 1 {
			t.Logf("Note: idx=%d maps to libopus idx=%d (expected within 1)", idx, libopusIdx)
		}
	}
}

// TestGainDequantizationAgainstLibopus compares gain dequantization
func TestGainDequantizationAgainstLibopus(t *testing.T) {
	for idx := 0; idx < 64; idx++ {
		// Dequantize with libopus
		libopusGainQ16 := GainDequantize(idx)

		// Dequantize with gopus (using GainDequantTable)
		gopusGainQ16 := silk.GainDequantTable[idx]

		if libopusGainQ16 != gopusGainQ16 {
			t.Errorf("Dequant index %d: libopus=%d, gopus=%d", idx, libopusGainQ16, gopusGainQ16)
		}
	}
}

// TestGainRoundTrip tests that quantize->dequantize produces consistent results
func TestGainRoundTrip(t *testing.T) {
	// Test various Q16 gain values
	testGains := []int32{
		81, 100, 200, 500, 1000, 2000, 5000, 10000, 17830, // Covering full range
	}

	for _, gainQ16 := range testGains {
		// Quantize with libopus
		idx := GainQuantizeSingle(gainQ16)

		// Dequantize
		dequantGainQ16 := GainDequantize(idx)

		// Verify dequantized gain is reasonably close to original
		ratio := float64(dequantGainQ16) / float64(gainQ16)
		if ratio < 0.5 || ratio > 2.0 {
			t.Errorf("Gain Q16=%d -> idx=%d -> Q16=%d (ratio=%.2f)",
				gainQ16, idx, dequantGainQ16, ratio)
		}
	}
}

// TestRawGainIndexComputation tests the core computation without clamping
func TestRawGainIndexComputation(t *testing.T) {
	// Test some specific values to understand the formula
	testCases := []struct {
		gainQ16    int32
		expectedLo int // Expected index range low
		expectedHi int // Expected index range high
	}{
		{81, 0, 1},      // Minimum table value
		{17830, 62, 63}, // Maximum table value
		{320, 15, 17},   // Mid-low
		{4935, 47, 49},  // Mid-high
	}

	for _, tc := range testCases {
		rawIdx := int(GainComputeRawIndex(tc.gainQ16))
		t.Logf("gainQ16=%d -> rawIdx=%d (expected [%d, %d])", tc.gainQ16, rawIdx, tc.expectedLo, tc.expectedHi)

		if rawIdx < tc.expectedLo || rawIdx > tc.expectedHi {
			t.Errorf("gainQ16=%d: rawIdx=%d not in [%d, %d]", tc.gainQ16, rawIdx, tc.expectedLo, tc.expectedHi)
		}
	}
}

// TestSMULWBAgainstLibopus verifies SMULWB implementation
func TestSMULWBAgainstLibopus(t *testing.T) {
	testCases := []struct {
		a, b int32
	}{
		{2251, 1000},
		{2251, -1000},
		{65536, 32767},
		{100000, 16384},
		{2251, 2090}, // SCALE_Q16 * OFFSET
	}

	for _, tc := range testCases {
		libopusResult := GainSilkSMULWB(tc.a, tc.b)
		goResult := silk.SilkSMULWBExport(tc.a, tc.b)

		if libopusResult != goResult {
			t.Errorf("SMULWB(%d, %d): libopus=%d, gopus=%d", tc.a, tc.b, libopusResult, goResult)
		}
	}
}

// TestFloatToQ16GainQuantization tests that float gains are correctly converted
func TestFloatToQ16GainQuantization(t *testing.T) {
	// The typical float gain range from computeSubframeGains is [0.001, 0.5]
	// which maps to Q16 range [65, 32768]
	testFloatGains := []float32{
		0.001, 0.01, 0.05, 0.1, 0.15, 0.2, 0.27, 0.5,
	}

	for _, floatGain := range testFloatGains {
		// Convert to Q16
		gainQ16 := int32(floatGain * 65536.0)

		// Quantize with libopus
		libopusIdx := GainQuantizeSingle(gainQ16)

		// Quantize with gopus
		gopusIdx := silk.ComputeLogGainIndexQ16Export(gainQ16)

		t.Logf("floatGain=%.4f -> Q16=%d -> libopusIdx=%d, gopusIdx=%d",
			floatGain, gainQ16, libopusIdx, gopusIdx)

		if libopusIdx != gopusIdx {
			t.Errorf("Float gain %.4f (Q16=%d): libopus=%d, gopus=%d",
				floatGain, gainQ16, libopusIdx, gopusIdx)
		}
	}
}

// TestFloatGainQuantization tests the float wrapper function
func TestFloatGainQuantization(t *testing.T) {
	testFloatGains := []float32{
		0.001, 0.01, 0.05, 0.1, 0.15, 0.2, 0.27, 0.5,
	}

	for _, floatGain := range testFloatGains {
		// Convert to Q16 for libopus comparison
		gainQ16 := int32(floatGain * 65536.0)

		// Quantize with libopus (using Q16)
		libopusIdx := GainQuantizeSingle(gainQ16)

		// Quantize with gopus float wrapper
		gopusIdx := silk.ComputeLogGainIndexExport(floatGain)

		t.Logf("floatGain=%.4f -> libopusIdx=%d, gopusIdx=%d",
			floatGain, libopusIdx, gopusIdx)

		if libopusIdx != gopusIdx {
			t.Errorf("Float gain %.4f: libopus=%d, gopus=%d",
				floatGain, libopusIdx, gopusIdx)
		}
	}
}

func absGain(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// TestEdgeCases tests boundary conditions
func TestEdgeCases(t *testing.T) {
	edgeCases := []int32{
		1, 2, // Very small values
		81 - 1, 81, 81 + 1, // Around minimum table value
		17830 - 1, 17830, 17830 + 1, // Around maximum table value
		65536, // 1.0 in float
	}

	for _, gainQ16 := range edgeCases {
		if gainQ16 <= 0 {
			continue // Skip non-positive for log
		}

		libopusIdx := GainQuantizeSingle(gainQ16)
		gopusIdx := silk.ComputeLogGainIndexQ16Export(gainQ16)

		if libopusIdx != gopusIdx {
			t.Errorf("Edge case Q16=%d: libopus=%d, gopus=%d", gainQ16, libopusIdx, gopusIdx)
		}
	}
}

// BenchmarkGainQuantization benchmarks the gain quantization
func BenchmarkGainQuantization(b *testing.B) {
	gains := make([]int32, 64)
	for i := range gains {
		gains[i] = silk.GainDequantTable[i]
	}

	b.Run("libopus", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, g := range gains {
				_ = GainQuantizeSingle(g)
			}
		}
	})

	b.Run("gopus", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, g := range gains {
				_ = silk.ComputeLogGainIndexQ16Export(g)
			}
		}
	})
}
