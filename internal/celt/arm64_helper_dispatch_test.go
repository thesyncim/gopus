package celt

import (
	"reflect"
	"testing"
)

func prefilterDualInnerProdRef(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]
	_ = y1[length-1]
	_ = y2[length-1]
	sum1 := float32(0)
	sum2 := float32(0)
	for i := 0; i < length; i++ {
		xi := float32(x[i])
		sum1 += xi * float32(y1[i])
		sum2 += xi * float32(y2[i])
	}
	return float64(sum1), float64(sum2)
}

func pitchAutocorr5Ref(lp []float64, length int, ac *[5]float64) {
	fastN := length - 4
	if fastN < 0 {
		fastN = 0
	}
	if pitchAutocorr5RefUsesArm64LibopusOrder && fastN > 0 {
		for lag := 0; lag < 4; lag++ {
			sum := float32(0)
			for i := 0; i < fastN; i++ {
				sum = fma32(float32(lp[i]), float32(lp[i+lag]), sum)
			}
			tail := float32(0)
			for i := lag + fastN; i < length; i++ {
				tail += float32(lp[i]) * float32(lp[i-lag])
			}
			ac[lag] = float64(sum + tail)
		}
		ac[4] = float64(pitchAutocorr5InnerProdNeonRef(lp, lp[4:], fastN))
		return
	}
	for lag := 0; lag <= 4; lag++ {
		sum := float32(0)
		for i := 0; i < fastN; i++ {
			sum += float32(lp[i]) * float32(lp[i+lag])
		}
		tail := float32(0)
		for i := lag + fastN; i < length; i++ {
			tail += float32(lp[i]) * float32(lp[i-lag])
		}
		ac[lag] = float64(sum + tail)
	}
}

func pitchAutocorr5InnerProdNeonRef(x, y []float64, n int) float32 {
	var sum [4]float32
	i := 0
	for ; i < n-7; i += 8 {
		for lane := 0; lane < 4; lane++ {
			sum[lane] = fma32(float32(x[i+lane]), float32(y[i+lane]), sum[lane])
		}
		for lane := 0; lane < 4; lane++ {
			sum[lane] = fma32(float32(x[i+4+lane]), float32(y[i+4+lane]), sum[lane])
		}
	}
	if n-i >= 4 {
		for lane := 0; lane < 4; lane++ {
			sum[lane] = fma32(float32(x[i+lane]), float32(y[i+lane]), sum[lane])
		}
		i += 4
	}
	xy0 := sum[0] + sum[2]
	xy1 := sum[1] + sum[3]
	xy := xy0 + xy1
	for ; i < n; i++ {
		xy = fma32(float32(x[i]), float32(y[i]), xy)
	}
	return xy
}

func TestArm64HotHelpersMatchReference(t *testing.T) {
	x := make([]float64, 480)
	y := make([]float64, 480+64)
	y2 := make([]float64, 480)
	for i := range x {
		x[i] = float64((i%17)-8) * 0.125
	}
	for i := range y {
		y[i] = float64((i%19)-9) * 0.09375
	}
	for i := range y2 {
		y2[i] = float64((i%23)-11) * 0.0625
	}

	xcorrGot := make([]float64, 64)
	xcorrWant := make([]float64, 64)
	celtPitchXcorr(x, y, xcorrGot, len(x), len(xcorrGot))
	refPitchXcorr(x, y, xcorrWant, len(x), len(xcorrWant))
	if !reflect.DeepEqual(xcorrGot, xcorrWant) {
		t.Fatalf("celtPitchXcorr mismatch")
	}

	got1, got2 := prefilterDualInnerProd(x, y[:len(x)], y2, len(x))
	want1, want2 := prefilterDualInnerProdRef(x, y[:len(x)], y2, len(x))
	if got1 != want1 || got2 != want2 {
		t.Fatalf("prefilterDualInnerProd mismatch: got (%v,%v) want (%v,%v)", got1, got2, want1, want2)
	}

	var acGot, acWant [5]float64
	pitchAutocorr5(y[:len(x)], len(x), &acGot)
	pitchAutocorr5Ref(y[:len(x)], len(x), &acWant)
	if acGot != acWant {
		t.Fatalf("pitchAutocorr5 mismatch: got %v want %v", acGot, acWant)
	}
}

func BenchmarkCeltPitchXcorrCurrent(b *testing.B) {
	length := 480
	maxPitch := 64
	x := make([]float64, length)
	y := make([]float64, maxPitch+length)
	xcorr := make([]float64, maxPitch)
	for i := range x {
		x[i] = float64(i) * 0.001
	}
	for i := range y {
		y[i] = float64(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtPitchXcorr(x, y, xcorr, length, maxPitch)
	}
}

func BenchmarkCeltPitchXcorrReference(b *testing.B) {
	length := 480
	maxPitch := 64
	x := make([]float64, length)
	y := make([]float64, maxPitch+length)
	xcorr := make([]float64, maxPitch)
	for i := range x {
		x[i] = float64(i) * 0.001
	}
	for i := range y {
		y[i] = float64(i) * 0.002
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		refPitchXcorr(x, y, xcorr, length, maxPitch)
	}
}

func BenchmarkPrefilterDualInnerProdCurrent(b *testing.B) {
	length := 480
	x := make([]float64, length)
	y1 := make([]float64, length)
	y2 := make([]float64, length)
	for i := range x {
		x[i] = float64(i) * 0.001
		y1[i] = float64(i) * 0.002
		y2[i] = float64(i) * 0.003
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = prefilterDualInnerProd(x, y1, y2, length)
	}
}

func BenchmarkPrefilterDualInnerProdReference(b *testing.B) {
	length := 480
	x := make([]float64, length)
	y1 := make([]float64, length)
	y2 := make([]float64, length)
	for i := range x {
		x[i] = float64(i) * 0.001
		y1[i] = float64(i) * 0.002
		y2[i] = float64(i) * 0.003
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = prefilterDualInnerProdRef(x, y1, y2, length)
	}
}

func BenchmarkPitchAutocorr5Current(b *testing.B) {
	length := (combFilterMaxPeriod + 480) >> 1
	lp := make([]float64, length)
	for i := range lp {
		lp[i] = float64((i%29)-14) * 0.03125
	}
	var ac [5]float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pitchAutocorr5(lp, length, &ac)
	}
}

func BenchmarkPitchAutocorr5Reference(b *testing.B) {
	length := (combFilterMaxPeriod + 480) >> 1
	lp := make([]float64, length)
	for i := range lp {
		lp[i] = float64((i%29)-14) * 0.03125
	}
	var ac [5]float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pitchAutocorr5Ref(lp, length, &ac)
	}
}
