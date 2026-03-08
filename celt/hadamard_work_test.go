package celt

import (
	"reflect"
	"testing"
)

func TestHadamardWorkIntoMatchesLegacy(t *testing.T) {
	cases := []struct {
		name     string
		n0       int
		stride   int
		hadamard bool
	}{
		{name: "plain_stride2", n0: 11, stride: 2},
		{name: "plain_stride4", n0: 9, stride: 4},
		{name: "plain_stride8", n0: 7, stride: 8},
		{name: "hadamard_stride2", n0: 13, stride: 2, hadamard: true},
		{name: "hadamard_stride4", n0: 10, stride: 4, hadamard: true},
		{name: "hadamard_stride8", n0: 6, stride: 8, hadamard: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.n0 * tc.stride
			src := make([]float64, n)
			for i := range src {
				src[i] = float64((i%17)-8) * 0.375
			}

			wantDeinterleave := append([]float64(nil), src...)
			deinterleaveHadamard(wantDeinterleave, tc.n0, tc.stride, tc.hadamard)

			gotDeinterleave := make([]float64, n)
			deinterleaveHadamardInto(gotDeinterleave, src, tc.n0, tc.stride, tc.hadamard)
			if !reflect.DeepEqual(gotDeinterleave, wantDeinterleave) {
				t.Fatalf("deinterleave mismatch: got %v want %v", gotDeinterleave, wantDeinterleave)
			}

			wantInterleave := append([]float64(nil), wantDeinterleave...)
			interleaveHadamard(wantInterleave, tc.n0, tc.stride, tc.hadamard)

			gotInterleave := make([]float64, n)
			interleaveHadamardInto(gotInterleave, gotDeinterleave, tc.n0, tc.stride, tc.hadamard)
			if !reflect.DeepEqual(gotInterleave, wantInterleave) {
				t.Fatalf("interleave mismatch: got %v want %v", gotInterleave, wantInterleave)
			}
			if !reflect.DeepEqual(gotInterleave, src) {
				t.Fatalf("roundtrip mismatch: got %v want %v", gotInterleave, src)
			}
		})
	}
}

func benchmarkHadamardWorkRoundTrip(b *testing.B, direct bool) {
	const (
		n0       = 22
		stride   = 6
		hadamard = false
	)
	n := n0 * stride
	src := make([]float64, n)
	for i := range src {
		src[i] = float64((i%23)-11) * 0.25
	}
	work := make([]float64, n)
	dst := make([]float64, n)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if direct {
			deinterleaveHadamardInto(work, src, n0, stride, hadamard)
			interleaveHadamardInto(dst, work, n0, stride, hadamard)
		} else {
			deinterleaveHadamardIntoLegacyDefault(work, src, n0, stride)
			interleaveHadamardIntoLegacyDefault(dst, work, n0, stride)
		}
	}
}

func deinterleaveHadamardIntoLegacyDefault(dst, src []float64, n0, stride int) {
	for i := 0; i < stride; i++ {
		row := i * n0
		for j := 0; j < n0; j++ {
			dst[row+j] = src[j*stride+i]
		}
	}
}

func interleaveHadamardIntoLegacyDefault(dst, src []float64, n0, stride int) {
	for i := 0; i < stride; i++ {
		row := i * n0
		for j := 0; j < n0; j++ {
			dst[j*stride+i] = src[row+j]
		}
	}
}

func BenchmarkHadamardWorkRoundTripCurrent(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true)
}

func BenchmarkHadamardWorkRoundTripLegacy(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false)
}
