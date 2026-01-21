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
