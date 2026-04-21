package celt

import "testing"

func TestApplyHybridPrefilterMatchesDisabledRunPrefilter(t *testing.T) {
	enc := NewEncoder(1)
	want := NewEncoder(1)
	frameSize := 480

	for _, cur := range []*Encoder{enc, want} {
		cur.tapsetDecision = 2
		cur.prefilterPeriod = 140
		cur.prefilterGain = 0.46875
		cur.prefilterTapset = 1
		cur.analysisValid = true
		cur.analysisMaxPitchRatio = 0.77
		for i := range cur.prefilterMem {
			cur.prefilterMem[i] = float64(i-120) / 512.0
		}
		for i := range cur.overlapBuffer {
			cur.overlapBuffer[i] = float64(60-i) / 256.0
		}
	}

	gotPreemph := make([]float64, frameSize)
	wantPreemph := make([]float64, frameSize)
	for i := range gotPreemph {
		v := float64((i%37)-18) / 64.0
		gotPreemph[i] = v
		wantPreemph[i] = v
	}

	enc.ApplyHybridPrefilter(gotPreemph, frameSize, 0.91, 14, 0.22, 0.18)

	prevPeriod := want.prefilterPeriod
	prevGain := want.prefilterGain
	pfResult := want.runPrefilter(wantPreemph, frameSize, want.TapsetDecision(), false, 0.91, 14, 0.22, 0.18, want.analysisMaxPitchRatio)
	roundFloat64ToFloat32(wantPreemph)
	want.lastPitchChange = false
	if prevPeriod > 0 && (pfResult.gain > 0.4 || prevGain > 0.4) {
		upper := int(1.26 * float64(prevPeriod))
		lower := int(0.79 * float64(prevPeriod))
		want.lastPitchChange = pfResult.pitch > upper || pfResult.pitch < lower
	}

	for i := range gotPreemph {
		if gotPreemph[i] != wantPreemph[i] {
			t.Fatalf("preemph[%d]=%v, want %v", i, gotPreemph[i], wantPreemph[i])
		}
	}
	for i := range enc.prefilterMem {
		if enc.prefilterMem[i] != want.prefilterMem[i] {
			t.Fatalf("prefilterMem[%d]=%v, want %v", i, enc.prefilterMem[i], want.prefilterMem[i])
		}
	}
	for i := range enc.overlapBuffer {
		if enc.overlapBuffer[i] != want.overlapBuffer[i] {
			t.Fatalf("overlapBuffer[%d]=%v, want %v", i, enc.overlapBuffer[i], want.overlapBuffer[i])
		}
	}
	if enc.prefilterPeriod != want.prefilterPeriod {
		t.Fatalf("prefilterPeriod=%d, want %d", enc.prefilterPeriod, want.prefilterPeriod)
	}
	if enc.prefilterGain != want.prefilterGain {
		t.Fatalf("prefilterGain=%v, want %v", enc.prefilterGain, want.prefilterGain)
	}
	if enc.prefilterTapset != want.prefilterTapset {
		t.Fatalf("prefilterTapset=%d, want %d", enc.prefilterTapset, want.prefilterTapset)
	}
	if enc.lastPitchChange != want.lastPitchChange {
		t.Fatalf("lastPitchChange=%v, want %v", enc.lastPitchChange, want.lastPitchChange)
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
