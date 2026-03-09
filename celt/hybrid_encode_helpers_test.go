package celt

import "testing"

func TestUpdateHybridPrefilterHistoryUpdatesStateCadence(t *testing.T) {
	enc := NewEncoder(1)
	enc.tapsetDecision = 2
	enc.prefilterPeriod = 0
	enc.prefilterGain = 0.3
	enc.prefilterTapset = 1

	frameSize := 480
	preemph := make([]float64, frameSize)
	for i := range preemph {
		preemph[i] = float64(i+1) / 1000.0
	}

	enc.UpdateHybridPrefilterHistory(preemph, frameSize)

	if enc.prefilterPeriod != combFilterMinPeriod {
		t.Fatalf("prefilterPeriod=%d, want %d", enc.prefilterPeriod, combFilterMinPeriod)
	}
	if enc.prefilterGain != 0 {
		t.Fatalf("prefilterGain=%f, want 0", enc.prefilterGain)
	}
	if enc.prefilterTapset != enc.tapsetDecision {
		t.Fatalf("prefilterTapset=%d, want %d", enc.prefilterTapset, enc.tapsetDecision)
	}
}

func TestNormalizeBandsToArrayMonoWithBandEMatchesSeparatePasses(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 18
	mdct := make([]float64, frameSize)
	for i := range mdct {
		mdct[i] = float64((i%29)-14) / 17.0
	}

	norm := make([]float64, frameSize)
	bandE := make([]float64, nbBands)
	NormalizeBandsToArrayInto(mdct, nbBands, frameSize, norm, bandE)

	normGot, bandEGot := enc.NormalizeBandsToArrayMonoWithBandE(mdct, nbBands, frameSize)
	for i := 0; i < frameSize; i++ {
		if normGot[i] != norm[i] {
			t.Fatalf("norm[%d]=%v, want %v", i, normGot[i], norm[i])
		}
	}
	for i := 0; i < nbBands; i++ {
		if bandEGot[i] != bandE[i] {
			t.Fatalf("bandE[%d]=%v, want %v", i, bandEGot[i], bandE[i])
		}
	}
}

func TestNormalizeBandsToArrayStereoWithBandEMatchesSeparatePasses(t *testing.T) {
	enc := NewEncoder(2)
	frameSize := 480
	nbBands := 18
	left := make([]float64, frameSize)
	right := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		left[i] = float64((i%31)-15) / 19.0
		right[i] = float64((i%27)-13) / 23.0
	}

	normL := make([]float64, frameSize)
	normR := make([]float64, frameSize)
	bandEL := make([]float64, nbBands)
	bandER := make([]float64, nbBands)
	NormalizeBandsToArrayInto(left, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayInto(right, nbBands, frameSize, normR, bandER)

	normLGot, normRGot, bandEGot := enc.NormalizeBandsToArrayStereoWithBandE(left, right, nbBands, frameSize)
	for i := 0; i < frameSize; i++ {
		if normLGot[i] != normL[i] {
			t.Fatalf("normL[%d]=%v, want %v", i, normLGot[i], normL[i])
		}
		if normRGot[i] != normR[i] {
			t.Fatalf("normR[%d]=%v, want %v", i, normRGot[i], normR[i])
		}
	}
	for i := 0; i < nbBands; i++ {
		if bandEGot[i] != bandEL[i] {
			t.Fatalf("bandEL[%d]=%v, want %v", i, bandEGot[i], bandEL[i])
		}
		if bandEGot[nbBands+i] != bandER[i] {
			t.Fatalf("bandER[%d]=%v, want %v", i, bandEGot[nbBands+i], bandER[i])
		}
	}
}
