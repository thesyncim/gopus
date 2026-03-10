//go:build arm64

package celt

import (
	"math"
	"testing"
)

func deinterleaveStereoIntoGeneric(interleaved, left, right []float64, n int) {
	i := 0
	for ; i+3 < n; i += 4 {
		b0 := i * 2
		left[i] = interleaved[b0]
		right[i] = interleaved[b0+1]
		left[i+1] = interleaved[b0+2]
		right[i+1] = interleaved[b0+3]
		left[i+2] = interleaved[b0+4]
		right[i+2] = interleaved[b0+5]
		left[i+3] = interleaved[b0+6]
		right[i+3] = interleaved[b0+7]
	}
	for ; i < n; i++ {
		b := i * 2
		left[i] = interleaved[b]
		right[i] = interleaved[b+1]
	}
}

func interleaveStereoIntoGeneric(left, right, interleaved []float64, n int) {
	i := 0
	for ; i+3 < n; i += 4 {
		b0 := i << 1
		interleaved[b0] = left[i]
		interleaved[b0+1] = right[i]
		interleaved[b0+2] = left[i+1]
		interleaved[b0+3] = right[i+1]
		interleaved[b0+4] = left[i+2]
		interleaved[b0+5] = right[i+2]
		interleaved[b0+6] = left[i+3]
		interleaved[b0+7] = right[i+3]
	}
	for ; i < n; i++ {
		b := i << 1
		interleaved[b] = left[i]
		interleaved[b+1] = right[i]
	}
}

func requireFloatBitsEqual(t *testing.T, name string, got, want []float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: got %d want %d", name, len(got), len(want))
	}
	for i := range got {
		if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
			t.Fatalf("%s[%d] mismatch: got 0x%x want 0x%x", name, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
		}
	}
}

func TestStereoLayoutArm64MatchesGenericExact(t *testing.T) {
	const n = 17
	interleaved := make([]float64, n*2)
	for i := range interleaved {
		switch i % 5 {
		case 0:
			interleaved[i] = math.Float64frombits(0x7ff8000000000000 + uint64(i))
		case 1:
			interleaved[i] = math.Float64frombits(0x8000000000000000 | uint64(i))
		case 2:
			interleaved[i] = math.Float64frombits(uint64(i) << 20)
		case 3:
			interleaved[i] = float64(i) * -0.125
		default:
			interleaved[i] = float64(i) * 0.375
		}
	}

	leftGot := make([]float64, n)
	rightGot := make([]float64, n)
	leftWant := make([]float64, n)
	rightWant := make([]float64, n)
	DeinterleaveStereoInto(interleaved, leftGot, rightGot)
	deinterleaveStereoIntoGeneric(interleaved, leftWant, rightWant, n)
	requireFloatBitsEqual(t, "left", leftGot, leftWant)
	requireFloatBitsEqual(t, "right", rightGot, rightWant)

	interleavedGot := make([]float64, n*2)
	interleavedWant := make([]float64, n*2)
	InterleaveStereoInto(leftGot, rightGot, interleavedGot)
	interleaveStereoIntoGeneric(leftWant, rightWant, interleavedWant, n)
	requireFloatBitsEqual(t, "interleaved", interleavedGot, interleavedWant)
}

func BenchmarkInterleaveStereoIntoGeneric(b *testing.B) {
	const n = 960
	left := make([]float64, n)
	right := make([]float64, n)
	interleaved := make([]float64, n*2)
	for i := 0; i < n; i++ {
		left[i] = float64(i%17) * 0.25
		right[i] = -float64(i%19) * 0.125
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interleaveStereoIntoGeneric(left, right, interleaved, n)
	}
}

func BenchmarkDeinterleaveStereoIntoGeneric(b *testing.B) {
	const n = 960
	left := make([]float64, n)
	right := make([]float64, n)
	interleaved := make([]float64, n*2)
	for i := 0; i < n; i++ {
		left[i] = float64(i%17) * 0.25
		right[i] = -float64(i%19) * 0.125
		interleaved[2*i] = left[i]
		interleaved[2*i+1] = right[i]
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deinterleaveStereoIntoGeneric(interleaved, left, right, n)
	}
}
