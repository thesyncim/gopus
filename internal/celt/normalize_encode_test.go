package celt

import "testing"

func TestNormalizeBandsMonoF32MatchesSeparatePasses(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 480
	nbBands := 18
	mdct := make([]float32, frameSize)
	for i := range mdct {
		mdct[i] = float32((i%29)-14) / 17.0
	}

	norm := make([]celtNorm, frameSize)
	bandE := make([]celtEner, nbBands)
	NormalizeBandsToArrayIntoF32(mdct, nbBands, frameSize, norm, bandE)

	normGot, bandEGot := enc.normalizeBandsMonoF32(mdct, nbBands, frameSize)
	for i := range frameSize {
		if normGot[i] != norm[i] {
			t.Fatalf("norm[%d]=%v, want %v", i, normGot[i], norm[i])
		}
	}
	for i := range nbBands {
		if bandEGot[i] != bandE[i] {
			t.Fatalf("bandE[%d]=%v, want %v", i, bandEGot[i], bandE[i])
		}
	}
}

func TestNormalizeBandsStereoF32MatchesSeparatePasses(t *testing.T) {
	enc := NewEncoder(2)
	frameSize := 480
	nbBands := 18
	left := make([]float32, frameSize)
	right := make([]float32, frameSize)
	for i := range frameSize {
		left[i] = float32((i%31)-15) / 19.0
		right[i] = float32((i%27)-13) / 23.0
	}

	normL := make([]celtNorm, frameSize)
	normR := make([]celtNorm, frameSize)
	bandEL := make([]celtEner, nbBands)
	bandER := make([]celtEner, nbBands)
	NormalizeBandsToArrayIntoF32(left, nbBands, frameSize, normL, bandEL)
	NormalizeBandsToArrayIntoF32(right, nbBands, frameSize, normR, bandER)

	normLGot, normRGot, bandEGot := enc.normalizeBandsStereoF32(left, right, nbBands, frameSize)
	for i := range frameSize {
		if normLGot[i] != normL[i] {
			t.Fatalf("normL[%d]=%v, want %v", i, normLGot[i], normL[i])
		}
		if normRGot[i] != normR[i] {
			t.Fatalf("normR[%d]=%v, want %v", i, normRGot[i], normR[i])
		}
	}
	for i := range nbBands {
		if bandEGot[i] != bandEL[i] {
			t.Fatalf("bandEL[%d]=%v, want %v", i, bandEGot[i], bandEL[i])
		}
		if bandEGot[nbBands+i] != bandER[i] {
			t.Fatalf("bandER[%d]=%v, want %v", i, bandEGot[nbBands+i], bandER[i])
		}
	}
}
