package celt

import "testing"

func TestGetModeConfig(t *testing.T) {
	tests := []struct {
		frameSize   int
		wantLM      int
		wantEffBands int
	}{
		{120, 0, 13},
		{240, 1, 17},
		{480, 2, 19},
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
	if dec.RNG() != 22222 {
		t.Errorf("after reset, RNG = %d, want 22222", dec.RNG())
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

	// Verify AlphaCoef and BetaCoef lengths
	if len(AlphaCoef) != 4 {
		t.Errorf("len(AlphaCoef) = %d, want 4", len(AlphaCoef))
	}
	if len(BetaCoef) != 4 {
		t.Errorf("len(BetaCoef) = %d, want 4", len(BetaCoef))
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
