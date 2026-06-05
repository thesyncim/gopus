package celt

import (
	"reflect"
	"testing"
)

func TestStereoLayoutHelpersRoundTrip(t *testing.T) {
	interleaved := []celtNorm{1, -1, 2, -2, 3, -3, 4, -4, 5, -5, 6, -6, 7, -7}
	left := make([]celtNorm, len(interleaved)/2)
	right := make([]celtNorm, len(interleaved)/2)
	roundtrip := make([]celtNorm, len(interleaved))

	DeinterleaveStereoInto(interleaved, left, right)
	InterleaveStereoInto(left, right, roundtrip)

	if !reflect.DeepEqual(roundtrip, interleaved) {
		t.Fatalf("stereo roundtrip mismatch: got %v want %v", roundtrip, interleaved)
	}
}

func benchmarkStereoLayout(b *testing.B, interleave bool) {
	const n = 960
	left := make([]celtNorm, n)
	right := make([]celtNorm, n)
	interleaved := make([]celtNorm, n*2)
	for i := range n {
		left[i] = celtNorm(float32(i%17) * 0.25)
		right[i] = celtNorm(-float32(i%19) * 0.125)
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
