package celt

import "testing"

func TestGetModeConfig(t *testing.T) {
	tests := []struct {
		frameSize    int
		wantLM       int
		wantEffBands int
	}{
		{120, 0, 21},
		{240, 1, 21},
		{480, 2, 21},
		{960, 3, 21},
	}

	for _, tt := range tests {
		cfg := GetModeConfig(tt.frameSize)
		if cfg.LM != tt.wantLM {
			t.Errorf("GetModeConfig(%d).LM = %d, want %d", tt.frameSize, cfg.LM, tt.wantLM)
		}
		if cfg.EffBands != tt.wantEffBands {
			t.Errorf("GetModeConfig(%d).EffBands = %d, want %d", tt.frameSize, cfg.EffBands, tt.wantEffBands)
		}
	}
}

// TestModeConfigShortFrames verifies mode configuration for 2.5ms and 5ms frames.
// These short frames still use the full band set at 48kHz:
// - 2.5ms (120 samples): LM=0, EffBands=21, ShortBlocks=1
// - 5ms (240 samples): LM=1, EffBands=21, ShortBlocks=2
// Reference: RFC 8251, libopus celt/modes.c
func TestModeConfigShortFrames(t *testing.T) {
	// Test 2.5ms frame configuration
	cfg120 := GetModeConfig(120)
	if cfg120.EffBands != 21 || cfg120.LM != 0 {
		t.Errorf("120 samples: got EffBands=%d LM=%d, want 21, 0", cfg120.EffBands, cfg120.LM)
	}
	if cfg120.ShortBlocks != 1 {
		t.Errorf("120 samples: got ShortBlocks=%d, want 1", cfg120.ShortBlocks)
	}
	if cfg120.MDCTSize != 120 {
		t.Errorf("120 samples: got MDCTSize=%d, want 120", cfg120.MDCTSize)
	}

	// Test 5ms frame configuration
	cfg240 := GetModeConfig(240)
	if cfg240.EffBands != 21 || cfg240.LM != 1 {
		t.Errorf("240 samples: got EffBands=%d LM=%d, want 21, 1", cfg240.EffBands, cfg240.LM)
	}
	if cfg240.ShortBlocks != 2 {
		t.Errorf("240 samples: got ShortBlocks=%d, want 2", cfg240.ShortBlocks)
	}
	if cfg240.MDCTSize != 240 {
		t.Errorf("240 samples: got MDCTSize=%d, want 240", cfg240.MDCTSize)
	}
}

func TestBandwidth(t *testing.T) {
	if CELTFullband.EffectiveBands() != 21 {
		t.Errorf("CELTFullband.EffectiveBands() = %d, want 21", CELTFullband.EffectiveBands())
	}
	if CELTNarrowband.EffectiveBands() != 13 {
		t.Errorf("CELTNarrowband.EffectiveBands() = %d, want 13", CELTNarrowband.EffectiveBands())
	}
}

func TestNewDecoder(t *testing.T) {
	// Test mono decoder
	dec := NewDecoder(1)
	if dec.Channels() != 1 {
		t.Errorf("NewDecoder(1).Channels() = %d, want 1", dec.Channels())
	}
	if len(dec.OverlapBuffer()) != Overlap {
		t.Errorf("mono overlap buffer length = %d, want %d", len(dec.OverlapBuffer()), Overlap)
	}
	if len(dec.PrevEnergy()) != MaxBands {
		t.Errorf("mono prevEnergy length = %d, want %d", len(dec.PrevEnergy()), MaxBands)
	}

	// Test stereo decoder
	decStereo := NewDecoder(2)
	if decStereo.Channels() != 2 {
		t.Errorf("NewDecoder(2).Channels() = %d, want 2", decStereo.Channels())
	}
	if len(decStereo.OverlapBuffer()) != Overlap*2 {
		t.Errorf("stereo overlap buffer length = %d, want %d", len(decStereo.OverlapBuffer()), Overlap*2)
	}
	if len(decStereo.PrevEnergy()) != MaxBands*2 {
		t.Errorf("stereo prevEnergy length = %d, want %d", len(decStereo.PrevEnergy()), MaxBands*2)
	}
}

func TestDecoderReset(t *testing.T) {
	dec := NewDecoder(2)

	// Modify state
	dec.SetPostfilter(100, 0.5, 1)
	dec.SetRNG(12345)
	dec.OverlapBuffer()[0] = 1.0
	dec.PrevEnergy()[0] = 10.0

	// Reset
	dec.Reset()

	// Verify state is cleared
	if dec.PostfilterPeriod() != 0 {
		t.Errorf("after reset, PostfilterPeriod = %d, want 0", dec.PostfilterPeriod())
	}
	if dec.RNG() != 0 {
		t.Errorf("after reset, RNG = %d, want 0", dec.RNG())
	}
	if dec.OverlapBuffer()[0] != 0 {
		t.Errorf("after reset, OverlapBuffer[0] = %f, want 0", dec.OverlapBuffer()[0])
	}
}

func TestDecoderRNG(t *testing.T) {
	dec := NewDecoder(1)

	// Test LCG progression
	initial := dec.RNG()
	next1 := dec.NextRNG()
	next2 := dec.NextRNG()

	// Verify RNG changes
	if next1 == initial {
		t.Error("NextRNG() returned same value as initial")
	}
	if next2 == next1 {
		t.Error("NextRNG() returned same value twice")
	}

	// Verify LCG formula: rng = rng*1664525 + 1013904223
	dec.SetRNG(1)
	expected := uint32(1)*1664525 + 1013904223
	actual := dec.NextRNG()
	if actual != expected {
		t.Errorf("NextRNG() = %d, want %d", actual, expected)
	}
}

func TestTables(t *testing.T) {
	// Verify eBands table length
	if len(EBands) != 22 {
		t.Errorf("len(EBands) = %d, want 22", len(EBands))
	}

	// Verify first and last values
	if EBands[0] != 0 {
		t.Errorf("EBands[0] = %d, want 0", EBands[0])
	}
	if EBands[21] != 100 {
		t.Errorf("EBands[21] = %d, want 100", EBands[21])
	}

	// Verify band widths are monotonically increasing (up to a point)
	for i := 0; i < MaxBands; i++ {
		width := BandWidth(i)
		if width <= 0 {
			t.Errorf("BandWidth(%d) = %d, want > 0", i, width)
		}
	}

	// Verify AlphaCoef and BetaCoefInter lengths
	if len(AlphaCoef) != 4 {
		t.Errorf("len(AlphaCoef) = %d, want 4", len(AlphaCoef))
	}
	if len(BetaCoefInter) != 4 {
		t.Errorf("len(BetaCoefInter) = %d, want 4", len(BetaCoefInter))
	}

	// Verify LogN length
	if len(LogN) != 21 {
		t.Errorf("len(LogN) = %d, want 21", len(LogN))
	}

	// Verify SmallDiv length
	if len(SmallDiv) != 129 {
		t.Errorf("len(SmallDiv) = %d, want 129", len(SmallDiv))
	}
}
