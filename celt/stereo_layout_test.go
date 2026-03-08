package celt

import (
	"reflect"
	"testing"
)

func TestStereoLayoutHelpersRoundTrip(t *testing.T) {
	interleaved := []float64{1, -1, 2, -2, 3, -3, 4, -4, 5, -5, 6, -6, 7, -7}
	left := make([]float64, len(interleaved)/2)
	right := make([]float64, len(interleaved)/2)
	roundtrip := make([]float64, len(interleaved))

	DeinterleaveStereoInto(interleaved, left, right)
	InterleaveStereoInto(left, right, roundtrip)

	if !reflect.DeepEqual(roundtrip, interleaved) {
		t.Fatalf("stereo roundtrip mismatch: got %v want %v", roundtrip, interleaved)
	}
}

func benchmarkStereoLayout(b *testing.B, interleave bool) {
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
		if interleave {
			InterleaveStereoInto(left, right, interleaved)
		} else {
			DeinterleaveStereoInto(interleaved, left, right)
		}
	}
}

func BenchmarkInterleaveStereoInto(b *testing.B) {
	benchmarkStereoLayout(b, true)
}

func BenchmarkDeinterleaveStereoInto(b *testing.B) {
	benchmarkStereoLayout(b, false)
}
