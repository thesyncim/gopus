//go:build arm64 && goexperiment.simd && !nosimd

package celt

import (
	"fmt"
	"math"
	"runtime"
	"testing"
)

var deemphasisFusedBoundsSink [3]float32

func TestDeemphasisStereoPlanar2StepFusedPreservesBits(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 479, 480, 481} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			left, right := makeDeemphasisBoundsInputs(n)
			want, got := make([]float32, n*2), make([]float32, n*2)
			wantL, wantR := deemphasisStereoPlanar2StepFusedReference(want, left, right, n, 1.0/32768.0, -71.875, 311.5)
			gotL, gotR := deemphasisStereoPlanar2StepFused(got, left, right, n, 1.0/32768.0, -71.875, 311.5)
			assertDeemphasisFusedBits(t, got, want, gotL, gotR, wantL, wantR)
		})
	}
}

func TestDeemphasisStereoPlanar2StepFusedPreservesExceptionalBits(t *testing.T) {
	values := []float32{
		0,
		math.Float32frombits(0x80000000),
		float32(math.Inf(1)),
		float32(math.Inf(-1)),
		math.Float32frombits(0x7fc01234),
		math.Float32frombits(0xffc05678),
	}
	for lane, value := range values {
		left, right := makeDeemphasisBoundsInputs(9)
		left[lane] = value
		right[(lane+4)%len(right)] = values[len(values)-1-lane]
		want, got := make([]float32, 18), make([]float32, 18)
		wantL, wantR := deemphasisStereoPlanar2StepFusedReference(want, left, right, len(left), 0.75, -0.125, 0.25)
		gotL, gotR := deemphasisStereoPlanar2StepFused(got, left, right, len(left), 0.75, -0.125, 0.25)
		assertDeemphasisFusedBits(t, got, want, gotL, gotR, wantL, wantR)
	}
}

func TestDeemphasisStereoPlanar2StepFusedAllocations(t *testing.T) {
	const n = 480
	left, right := makeDeemphasisBoundsInputs(n)
	dst := make([]float32, n*2)
	deemphasisStereoPlanar2StepFused(dst, left, right, n, 1.0/32768.0, 0, 0)
	allocs := testing.AllocsPerRun(100, func() {
		deemphasisFusedBoundsSink[0], deemphasisFusedBoundsSink[1] = deemphasisStereoPlanar2StepFused(dst, left, right, n, 1.0/32768.0, 0, 0)
		deemphasisFusedBoundsSink[2] = dst[len(dst)-1]
	})
	runtime.KeepAlive(dst)
	if allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0", allocs)
	}
}

func BenchmarkPortDeemphasisStereoPlanar2StepFused(b *testing.B) {
	benchmarkDeemphasisStereoPlanar2StepFused(b, deemphasisStereoPlanar2StepFused)
}

func BenchmarkPortDeemphasisStereoPlanar2StepFusedBaseline(b *testing.B) {
	benchmarkDeemphasisStereoPlanar2StepFused(b, deemphasisStereoPlanar2StepFusedReference)
}

func benchmarkDeemphasisStereoPlanar2StepFused(b *testing.B, fn func([]float32, []float32, []float32, int, float32, float32, float32) (float32, float32)) {
	const n = 480
	left, right := makeDeemphasisBoundsInputs(n)
	dst := make([]float32, n*2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deemphasisFusedBoundsSink[0], deemphasisFusedBoundsSink[1] = fn(dst, left, right, n, 1.0/32768.0, -71.875, 311.5)
	}
	deemphasisFusedBoundsSink[2] = dst[len(dst)-1]
	runtime.KeepAlive(dst)
}

func assertDeemphasisFusedBits(t *testing.T, got, want []float32, gotL, gotR, wantL, wantR float32) {
	t.Helper()
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("pcm[%d]=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	if math.Float32bits(gotL) != math.Float32bits(wantL) || math.Float32bits(gotR) != math.Float32bits(wantR) {
		t.Fatalf("state=(%08x,%08x), want (%08x,%08x)", math.Float32bits(gotL), math.Float32bits(gotR), math.Float32bits(wantL), math.Float32bits(wantR))
	}
}

func makeDeemphasisBoundsInputs(n int) ([]float32, []float32) {
	left, right := make([]float32, n), make([]float32, n)
	for i := range left {
		left[i] = float32(math.Sin(float64(i+3)*0.113)*1900 + math.Cos(float64(i+9)*0.047)*720)
		right[i] = float32(math.Cos(float64(i+6)*0.151)*1600 - math.Sin(float64(i+2)*0.083)*810)
	}
	return left, right
}

func deemphasisStereoPlanar2StepFusedReference(dst, left, right []float32, n int, scale, stateL, stateR float32) (float32, float32) {
	const verySmall float32 = 1e-30
	const coef float32 = float32(PreemphCoef)
	outScale := scale / coef
	c2 := coef * coef
	i := 0
	for ; i+1 < n; i += 2 {
		lc0 := coef * (left[i] + verySmall)
		lc1 := coef * (left[i+1] + verySmall)
		lp := coef*lc0 + lc1
		ls0 := coef*stateL + lc0
		stateL = c2*stateL + lp

		rc0 := coef * (right[i] + verySmall)
		rc1 := coef * (right[i+1] + verySmall)
		rp := coef*rc0 + rc1
		rs0 := coef*stateR + rc0
		stateR = c2*stateR + rp

		dst[2*i] = ls0 * outScale
		dst[2*i+1] = rs0 * outScale
		dst[2*i+2] = stateL * outScale
		dst[2*i+3] = stateR * outScale
	}
	for ; i < n; i++ {
		stateL = coef*stateL + coef*(left[i]+verySmall)
		stateR = coef*stateR + coef*(right[i]+verySmall)
		dst[2*i] = stateL * outScale
		dst[2*i+1] = stateR * outScale
	}
	return stateL, stateR
}
