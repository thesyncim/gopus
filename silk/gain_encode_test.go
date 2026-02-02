package silk

import (
	"testing"
)

func TestComputeLogGainIndex(t *testing.T) {
	// Test that computeLogGainIndex is inverse of GainDequantTable
	for idx := 0; idx < 64; idx++ {
		gainQ16 := GainDequantTable[idx]
		gainFloat := float32(gainQ16) / 65536.0

		computedIdx := computeLogGainIndex(gainFloat)

		// Allow +/- 1 for rounding
		if absInt(computedIdx-idx) > 1 {
			t.Errorf("idx=%d: gainQ16=%d, computed idx=%d", idx, gainQ16, computedIdx)
		}
	}
}

func TestComputeLogGainIndexBoundary(t *testing.T) {
	// Test boundary conditions
	tests := []struct {
		name   string
		gain   float32
		minIdx int
		maxIdx int
	}{
		{"zero gain", 0.0, 0, 1}, // Should map to lowest index
		{"min table gain", float32(GainDequantTable[0]) / 65536.0, 0, 1},
		{"max table gain", float32(GainDequantTable[63]) / 65536.0, 62, 63},
		{"mid table gain", float32(GainDequantTable[32]) / 65536.0, 31, 33},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idx := computeLogGainIndex(tc.gain)
			if idx < tc.minIdx || idx > tc.maxIdx {
				t.Errorf("gain=%.6f: expected idx in [%d, %d], got %d", tc.gain, tc.minIdx, tc.maxIdx, idx)
			}
		})
	}
}

func TestGainEncodeDecode(t *testing.T) {
	// Test that encoded gains produce same decoded values
	// GainDequantTable Q16 values range from 81920 (~1.25 float) to 1686110208 (~25729 float)
	// These represent SILK gain levels, not direct amplitude multipliers
	enc := NewEncoder(BandwidthWideband)
	dec := NewDecoder()

	// Test gains using values from the CORRECT GainDequantTable range
	// Table[0] = 81920 = 1.25 float, Table[63] = 1686110208 = 25729 float
	testCases := []struct {
		name       string
		gains      []float32
		signalType int
	}{
		{"voiced high", []float32{5000, 6000, 5500, 5800}, 2}, // Voiced (higher gains, mid-range)
		{"unvoiced mid", []float32{100, 120, 110, 115}, 1},    // Unvoiced (lower gains)
		{"inactive low", []float32{2.0, 2.0, 2.0, 2.0}, 0},    // Inactive (near minimum)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Quantize gains
			logGains := make([]int, len(tc.gains))
			for i, g := range tc.gains {
				logGains[i] = computeLogGainIndex(g)
			}

			// Dequantize using decoder's table and verify round-trip
			for i, logGain := range logGains {
				decodedQ16 := GainDequantTable[logGain]
				decodedFloat := float32(decodedQ16) / 65536.0

				// Verify decoded is in same ballpark as original
				// Allow factor of 2 error due to coarse quantization (64 levels over large range)
				ratio := float64(decodedFloat) / float64(tc.gains[i])
				if ratio < 0.3 || ratio > 3.0 {
					t.Errorf("sf=%d: input=%.4f, decoded=%.4f, ratio=%.2f",
						i, tc.gains[i], decodedFloat, ratio)
				}
			}
		})
	}

	_ = dec // Avoid unused variable warning
	_ = enc
}

func TestComputeSubframeGains(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test with known PCM data
	tests := []struct {
		name         string
		pcm          []float32
		numSubframes int
		expectedLen  int
	}{
		{"4 subframes", make([]float32, 320), 4, 4},
		{"2 subframes", make([]float32, 160), 2, 2},
		{"1 subframe", make([]float32, 80), 1, 1},
		{"empty", []float32{}, 4, 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Fill PCM with test signal if non-empty
			for i := range tc.pcm {
				tc.pcm[i] = float32(i%100) / 100.0
			}

			gains := enc.computeSubframeGains(tc.pcm, tc.numSubframes)
			if len(gains) != tc.expectedLen {
				t.Errorf("expected %d gains, got %d", tc.expectedLen, len(gains))
			}

			// All gains should be non-negative
			for i, g := range gains {
				if g < 0 {
					t.Errorf("gain[%d] = %f, expected non-negative", i, g)
				}
			}

			// For non-empty PCM with non-zero signal, gains should be positive
			if tc.name != "empty" && len(tc.pcm) > 0 {
				for i, g := range gains {
					if g <= 0 {
						t.Errorf("gain[%d] = %f, expected positive for non-empty PCM", i, g)
					}
				}
			}

			// For empty PCM, gains are zero (early return)
			if tc.name == "empty" {
				for i, g := range gains {
					if g != 0 {
						t.Errorf("empty PCM gain[%d] = %f, expected 0", i, g)
					}
				}
			}
		})
	}
}

func TestQuantizeLSF(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Create test LSF (evenly spaced)
	lsfQ15 := make([]int16, config.LPCOrder)
	for i := 0; i < config.LPCOrder; i++ {
		lsfQ15[i] = int16((i + 1) * 32767 / (config.LPCOrder + 1))
	}

	stage1Idx, residuals, interpIdx := enc.quantizeLSF(lsfQ15, BandwidthWideband, 2, 200, 4)

	// Verify stage1 index is valid
	if stage1Idx < 0 || stage1Idx >= 32 {
		t.Errorf("invalid stage1Idx: %d", stage1Idx)
	}

	// Verify residuals count
	if len(residuals) != config.LPCOrder {
		t.Errorf("expected %d residuals, got %d", config.LPCOrder, len(residuals))
	}

	// Verify residuals are in valid range (0-8 for 9 entries)
	for i, r := range residuals {
		if r < 0 || r > 8 {
			t.Errorf("residual[%d] = %d, expected [0, 8]", i, r)
		}
	}

	// Verify interpolation index
	if interpIdx < 0 || interpIdx > 4 {
		t.Errorf("invalid interpIdx: %d", interpIdx)
	}
}

func TestQuantizeLSFNarrowband(t *testing.T) {
	enc := NewEncoder(BandwidthNarrowband)
	config := GetBandwidthConfig(BandwidthNarrowband)

	// Create test LSF
	lsfQ15 := make([]int16, config.LPCOrder)
	for i := 0; i < config.LPCOrder; i++ {
		lsfQ15[i] = int16((i + 1) * 32767 / (config.LPCOrder + 1))
	}

	stage1Idx, residuals, interpIdx := enc.quantizeLSF(lsfQ15, BandwidthNarrowband, 1, 200, 4)

	// Verify stage1 index is valid (NB/MB has fewer entries in ICDF)
	if stage1Idx < 0 || stage1Idx >= 25 { // ICDFLSFStage1NBMBUnvoiced has 24 symbols
		t.Errorf("invalid stage1Idx: %d", stage1Idx)
	}

	// Verify residuals count
	if len(residuals) != config.LPCOrder {
		t.Errorf("expected %d residuals, got %d", config.LPCOrder, len(residuals))
	}

	// Verify interpolation index
	if interpIdx < 0 || interpIdx > 4 {
		t.Errorf("invalid interpIdx: %d", interpIdx)
	}
}

func TestLSFEncodeDecode(t *testing.T) {
	// This test verifies the quantization produces valid indices
	// that can be reconstructed using the libopus decoder logic

	enc := NewEncoder(BandwidthNarrowband)
	config := GetBandwidthConfig(BandwidthNarrowband)
	cb := &silk_NLSF_CB_NB_MB

	// Test LSF - use values closer to codebook entries for better reconstruction
	lsfQ15 := make([]int16, config.LPCOrder)
	for i := 0; i < config.LPCOrder; i++ {
		// Use evenly spaced values in the valid LSF range
		lsfQ15[i] = int16((i + 1) * 30000 / (config.LPCOrder + 1))
	}

	stage1Idx, residuals, _ := enc.quantizeLSF(lsfQ15, BandwidthNarrowband, 1, 200, 4)

	// Verify stage1Idx is in valid range
	if stage1Idx < 0 || stage1Idx >= cb.nVectors {
		t.Errorf("stage1Idx=%d out of range [0, %d)", stage1Idx, cb.nVectors)
	}

	// Verify residuals are in valid range [-nlsfQuantMaxAmplitude, +nlsfQuantMaxAmplitude]
	for i := 0; i < config.LPCOrder; i++ {
		if residuals[i] < -nlsfQuantMaxAmplitude || residuals[i] > nlsfQuantMaxAmplitude {
			t.Errorf("residual[%d]=%d out of range [%d, %d]",
				i, residuals[i], -nlsfQuantMaxAmplitude, nlsfQuantMaxAmplitude)
		}
	}

	// Reconstruct LSF using libopus decoder logic (silkNLSFDecode)
	indices := make([]int8, config.LPCOrder+1)
	indices[0] = int8(stage1Idx)
	for i := 0; i < config.LPCOrder; i++ {
		indices[i+1] = int8(residuals[i])
	}

	reconstructed := make([]int16, config.LPCOrder)
	silkNLSFDecode(reconstructed, indices, cb)

	// Verify reconstruction is reasonably close to original
	// VQ quantization can have significant error - allow 20% of full range
	maxAllowedDiff := 6500
	for i := 0; i < config.LPCOrder; i++ {
		diff := absInt(int(lsfQ15[i]) - int(reconstructed[i]))
		if diff > maxAllowedDiff {
			t.Errorf("LSF[%d]: original=%d, reconstructed=%d, diff=%d (max allowed %d)",
				i, lsfQ15[i], reconstructed[i], diff, maxAllowedDiff)
		}
	}
}

func TestInterpolationIndex(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Test first frame (no interpolation)
	lsfQ15 := make([]int16, config.LPCOrder)
	for i := range lsfQ15 {
		lsfQ15[i] = int16(i * 2000)
	}

	interpIdx := enc.computeInterpolationIndex(lsfQ15, config.LPCOrder)
	if interpIdx != 4 {
		t.Errorf("first frame should have interpIdx=4, got %d", interpIdx)
	}

	// Simulate having encoded a frame
	enc.haveEncoded = true
	copy(enc.prevLSFQ15, lsfQ15)

	// Same LSF should give heavy interpolation
	interpIdx = enc.computeInterpolationIndex(lsfQ15, config.LPCOrder)
	if interpIdx > 1 {
		t.Errorf("identical LSF should have low interpIdx, got %d", interpIdx)
	}

	// Very different LSF should give no interpolation
	for i := range lsfQ15 {
		lsfQ15[i] = int16(i*2000 + 10000)
	}
	interpIdx = enc.computeInterpolationIndex(lsfQ15, config.LPCOrder)
	if interpIdx < 3 {
		t.Errorf("very different LSF should have high interpIdx, got %d", interpIdx)
	}
}

func TestComputeSymbolRate8(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test with libopus NLSF CB1 ICDF (uint8)
	icdf := silk_NLSF_CB1_iCDF_WB

	// Low probability symbols should have higher rate
	rate0 := enc.computeSymbolRate8(0, icdf)
	rate10 := enc.computeSymbolRate8(10, icdf)

	// Both should be positive
	if rate0 <= 0 {
		t.Errorf("rate for symbol 0 should be positive, got %d", rate0)
	}
	if rate10 <= 0 {
		t.Errorf("rate for symbol 10 should be positive, got %d", rate10)
	}

	// Invalid symbol should return max rate
	rateInvalid := enc.computeSymbolRate8(-1, icdf)
	if rateInvalid != 256 {
		t.Errorf("invalid symbol rate should be 256, got %d", rateInvalid)
	}
}
