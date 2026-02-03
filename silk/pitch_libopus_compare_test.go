//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestPitchAnalysisMatchesLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	fsKHz := 16
	numSubfr := 4
	frameLen := (peLTPMemLengthMS + numSubfr*peSubfrLengthMS) * fsKHz
	laPitch := laPitchMs * fsKHz
	needed := frameLen + laPitch

	signal := make([]float32, needed)
	for i := range signal {
		tm := float64(i) / float64(fsKHz*1000)
		signal[i] = float32(
			0.6*math.Sin(2*math.Pi*220*tm) +
				0.3*math.Sin(2*math.Pi*440*tm) +
				0.1*math.Sin(2*math.Pi*660*tm),
		)
	}
	enc.inputBuffer = signal
	enc.pitchState.prevLag = 0
	enc.pitchState.ltpCorr = 0

	_, residual32, _, _ := enc.computePitchResidual(numSubfr)
	if len(residual32) < frameLen {
		t.Fatalf("residual too short: %d < %d", len(residual32), frameLen)
	}

	complexity := enc.pitchEstimationComplexity
	if complexity < 0 {
		complexity = 0
	} else if complexity > 2 {
		complexity = 2
	}

	searchThres1 := 0.8 - 0.5*float64(complexity)/2.0
	searchThres2 := 0.4 - 0.25*float64(complexity)/2.0

	pitchLags := enc.detectPitch(residual32, numSubfr, searchThres1, searchThres2)
	if len(pitchLags) != numSubfr {
		t.Fatalf("unexpected pitch lag count: %d", len(pitchLags))
	}

	pitchParams := enc.preparePitchLags(append([]int(nil), pitchLags...), numSubfr)

	lib := libopusPitchAnalysis(residual32[:frameLen], fsKHz, numSubfr, complexity, searchThres1, searchThres2, 0, 0)
	if !lib.Voiced {
		t.Fatalf("libopus pitch analysis returned unvoiced")
	}

	for i := 0; i < numSubfr; i++ {
		if pitchLags[i] != lib.Pitch[i] {
			t.Fatalf("pitchLags[%d] mismatch: go=%d lib=%d", i, pitchLags[i], lib.Pitch[i])
		}
	}
	if pitchParams.lagIdx != int(lib.LagIndex) {
		t.Fatalf("lagIndex mismatch: go=%d lib=%d", pitchParams.lagIdx, lib.LagIndex)
	}
	if pitchParams.contourIdx != int(lib.ContourIndex) {
		t.Fatalf("contourIndex mismatch: go=%d lib=%d", pitchParams.contourIdx, lib.ContourIndex)
	}
	if diff := math.Abs(float64(enc.pitchState.ltpCorr) - float64(lib.LTPCorr)); diff > 5e-2 {
		t.Fatalf("ltpCorr mismatch: go=%g lib=%g diff=%g", enc.pitchState.ltpCorr, lib.LTPCorr, diff)
	}
}
