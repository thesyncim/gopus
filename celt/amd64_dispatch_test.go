//go:build amd64

package celt

import (
	"reflect"
	"testing"
)

func withAMD64FeaturesForTest(avx, avxfma, avx2fma bool, fn func()) {
	prevAVX := amd64HasAVX
	prevAVXFMA := amd64HasAVXFMA
	prevAVX2FMA := amd64HasAVX2FMA
	amd64HasAVX = avx
	amd64HasAVXFMA = avxfma
	amd64HasAVX2FMA = avx2fma
	defer func() {
		amd64HasAVX = prevAVX
		amd64HasAVXFMA = prevAVXFMA
		amd64HasAVX2FMA = prevAVX2FMA
	}()
	fn()
}

func TestAMD64DispatchFallbackMatchesGeneric(t *testing.T) {
	withAMD64FeaturesForTest(false, false, false, func() {
		absInput := []float64{-4.5, 0, 1.25, -2.75, 8}
		if got, want := absSum(absInput), absSumGeneric(absInput); got != want {
			t.Fatalf("absSum fallback mismatch: got %v want %v", got, want)
		}

		roundInput := []float64{-1.1, 3.141592653589793, 1 << 20, -1 << 18, 0.3333333333333333}
		roundWant := append([]float64(nil), roundInput...)
		roundFloat64ToFloat32Generic(roundWant)
		roundGot := append([]float64(nil), roundInput...)
		roundFloat64ToFloat32(roundGot)
		if !reflect.DeepEqual(roundGot, roundWant) {
			t.Fatalf("roundFloat64ToFloat32 fallback mismatch: got %v want %v", roundGot, roundWant)
		}

		xcorrInput := []float64{0.5, -1.25, 3.0, -0.75, 2.25, 1.5}
		xcorrY := []float64{1.0, -0.5, 2.5, 0.75, -1.0, 1.25, 0.5, -2.0, 3.0, -1.5}
		xcorrWant := make([]float64, 4)
		xcorrGot := make([]float64, 4)
		celtPitchXcorrGeneric(xcorrInput, xcorrY, xcorrWant, len(xcorrInput), len(xcorrWant))
		celtPitchXcorr(xcorrInput, xcorrY, xcorrGot, len(xcorrInput), len(xcorrGot))
		if !reflect.DeepEqual(xcorrGot, xcorrWant) {
			t.Fatalf("celtPitchXcorr fallback mismatch: got %v want %v", xcorrGot, xcorrWant)
		}

		prefX := []float64{1, -2, 0.5, 3.5, -4.25}
		prefY1 := []float64{0.25, 1.5, -2, 0.5, 1.25}
		prefY2 := []float64{-1, 2.25, 0.75, -0.5, 4}
		if got, want := prefilterInnerProd(prefX, prefY1, len(prefX)), prefilterInnerProdGeneric(prefX, prefY1, len(prefX)); got != want {
			t.Fatalf("prefilterInnerProd fallback mismatch: got %v want %v", got, want)
		}
		got1, got2 := prefilterDualInnerProd(prefX, prefY1, prefY2, len(prefX))
		want1, want2 := prefilterDualInnerProdGeneric(prefX, prefY1, prefY2, len(prefX))
		if got1 != want1 || got2 != want2 {
			t.Fatalf("prefilterDualInnerProd fallback mismatch: got (%v,%v) want (%v,%v)", got1, got2, want1, want2)
		}

		absX := []float32{1.5, 0.75, 2.25, 0.5, 1.125}
		y := []float32{2, 0, 4, 2, 0}
		if got, want := pvqSearchBestPos(absX, y, 1.25, 3.5, len(absX)), pvqSearchBestPosGeneric(absX, y, 1.25, 3.5, len(absX)); got != want {
			t.Fatalf("pvqSearchBestPos fallback mismatch: got %v want %v", got, want)
		}

		yGot := append([]float32(nil), y...)
		yWant := append([]float32(nil), y...)
		iyGot := make([]int, len(absX))
		iyWant := make([]int, len(absX))
		gotXY, gotYY := pvqSearchPulseLoop(absX, yGot, iyGot, 1.25, 3.5, len(absX), 4)
		wantXY, wantYY := pvqSearchPulseLoopGeneric(absX, yWant, iyWant, 1.25, 3.5, len(absX), 4)
		if gotXY != wantXY || gotYY != wantYY || !reflect.DeepEqual(yGot, yWant) || !reflect.DeepEqual(iyGot, iyWant) {
			t.Fatalf("pvqSearchPulseLoop fallback mismatch: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", gotXY, gotYY, yGot, iyGot, wantXY, wantYY, yWant, iyWant)
		}

		signInput := []float64{-1.5, 2.25, 0, -0.125, 4}
		signGotAbs := make([]float32, len(signInput))
		signWantAbs := make([]float32, len(signInput))
		signGotY := make([]float32, len(signInput))
		signWantY := make([]float32, len(signInput))
		signGotBits := make([]int, len(signInput))
		signWantBits := make([]int, len(signInput))
		signGotIY := make([]int, len(signInput))
		signWantIY := make([]int, len(signInput))
		pvqExtractAbsSign(signInput, signGotAbs, signGotY, signGotBits, signGotIY, len(signInput))
		pvqExtractAbsSignGeneric(signInput, signWantAbs, signWantY, signWantBits, signWantIY, len(signInput))
		if !reflect.DeepEqual(signGotAbs, signWantAbs) || !reflect.DeepEqual(signGotY, signWantY) || !reflect.DeepEqual(signGotBits, signWantBits) || !reflect.DeepEqual(signGotIY, signWantIY) {
			t.Fatalf("pvqExtractAbsSign fallback mismatch")
		}

		rotInput := []float64{1, -2, 3, -4, 5, -6, 7, -8}
		rotWant := append([]float64(nil), rotInput...)
		rotGot := append([]float64(nil), rotInput...)
		expRotation1Stride2Generic(rotWant, len(rotWant), 0.875, 0.4841229182759271)
		expRotation1Stride2(rotGot, len(rotGot), 0.875, 0.4841229182759271)
		if !reflect.DeepEqual(rotGot, rotWant) {
			t.Fatalf("expRotation1Stride2 fallback mismatch: got %v want %v", rotGot, rotWant)
		}

		transientTmp := []float64{1.5, -2.5, 0.25, 4, -1, 0.5, 3.5, -0.75}
		transientGot := make([]float32, len(transientTmp)/2)
		transientWant := make([]float32, len(transientTmp)/2)
		gotMean := transientEnergyPairs(transientTmp, transientGot, len(transientGot))
		wantMean := transientEnergyPairsGeneric(transientTmp, transientWant, len(transientWant))
		if gotMean != wantMean || !reflect.DeepEqual(transientGot, transientWant) {
			t.Fatalf("transientEnergyPairs fallback mismatch: got (%v,%v) want (%v,%v)", gotMean, transientGot, wantMean, transientWant)
		}

		lp := []float64{1, -0.5, 2.25, 3.5, -4.125, 0.75, 1.5, -2}
		var acGot, acWant [5]float64
		pitchAutocorr5(lp, len(lp), &acGot)
		pitchAutocorr5Generic(lp, len(lp), &acWant)
		if acGot != acWant {
			t.Fatalf("pitchAutocorr5 fallback mismatch: got %v want %v", acGot, acWant)
		}

		toneInput := []float32{1, -2, 3, 4, -5, 6, 7, -8, 9}
		gotR00, gotR01, gotR02 := toneLPCCorr(toneInput, 5, 2, 4)
		wantR00, wantR01, wantR02 := toneLPCCorrGeneric(toneInput, 5, 2, 4)
		if gotR00 != wantR00 || gotR01 != wantR01 || gotR02 != wantR02 {
			t.Fatalf("toneLPCCorr fallback mismatch: got (%v,%v,%v) want (%v,%v,%v)", gotR00, gotR01, gotR02, wantR00, wantR01, wantR02)
		}

		prefXcorrGot := make([]float64, 5)
		prefXcorrWant := make([]float64, 5)
		prefilterPitchXcorr(xcorrInput, xcorrY, prefXcorrGot, len(xcorrInput), len(prefXcorrGot))
		prefilterPitchXcorrGeneric(xcorrInput, xcorrY, prefXcorrWant, len(xcorrInput), len(prefXcorrWant))
		if !reflect.DeepEqual(prefXcorrGot, prefXcorrWant) {
			t.Fatalf("prefilterPitchXcorr fallback mismatch: got %v want %v", prefXcorrGot, prefXcorrWant)
		}
	})
}
