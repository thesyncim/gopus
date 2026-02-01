package celt

import (
	"math"
	"testing"
)

func TestAlphaCoef(t *testing.T) {
	// Values from libopus celt/quant_bands.c
	expected := []float64{
		29440.0 / 32768.0, // LM=0: 0.8984375
		26112.0 / 32768.0, // LM=1: 0.796875
		21248.0 / 32768.0, // LM=2: 0.6484375
		16384.0 / 32768.0, // LM=3: 0.5
	}

	for lm := 0; lm < 4; lm++ {
		if math.Abs(AlphaCoef[lm]-expected[lm]) > 1e-10 {
			t.Errorf("AlphaCoef[%d] = %v, want %v", lm, AlphaCoef[lm], expected[lm])
		}
	}
}

func TestBetaCoefInter(t *testing.T) {
	// Values from libopus celt/quant_bands.c for INTER mode
	expected := []float64{
		30147.0 / 32768.0, // LM=0: 0.9200744...
		22282.0 / 32768.0, // LM=1: 0.6800537...
		12124.0 / 32768.0, // LM=2: 0.3700561...
		6554.0 / 32768.0,  // LM=3: 0.2000122...
	}

	for lm := 0; lm < 4; lm++ {
		if math.Abs(BetaCoefInter[lm]-expected[lm]) > 1e-10 {
			t.Errorf("BetaCoefInter[%d] = %v, want %v", lm, BetaCoefInter[lm], expected[lm])
		}
	}

	// Verify values are distinct (not all 0.85 like old bug)
	for i := 0; i < 3; i++ {
		if BetaCoefInter[i] == BetaCoefInter[i+1] {
			t.Errorf("BetaCoefInter[%d] == BetaCoefInter[%d], values should be distinct", i, i+1)
		}
	}
}

func TestBetaIntra(t *testing.T) {
	expected := 4915.0 / 32768.0 // 0.15
	if math.Abs(BetaIntra-expected) > 1e-10 {
		t.Errorf("BetaIntra = %v, want %v", BetaIntra, expected)
	}
}
