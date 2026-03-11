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
		{name: "plain_stride12", n0: 5, stride: 12},
		{name: "plain_stride16", n0: 4, stride: 16},
		{name: "hadamard_stride2", n0: 13, stride: 2, hadamard: true},
		{name: "hadamard_stride4", n0: 10, stride: 4, hadamard: true},
		{name: "hadamard_stride8", n0: 6, stride: 8, hadamard: true},
		{name: "hadamard_stride16", n0: 4, stride: 16, hadamard: true},
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

func benchmarkHadamardWorkRoundTrip(b *testing.B, direct bool, n0, stride int, hadamard bool) {
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
			deinterleaveHadamardIntoLegacy(work, src, n0, stride, hadamard)
			interleaveHadamardIntoLegacy(dst, work, n0, stride, hadamard)
		}
	}
}

func deinterleaveHadamardIntoLegacy(work, src []float64, n0, stride int, hadamard bool) {
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			row := ordery[i] * n0
			for j := 0; j < n0; j++ {
				work[row+j] = src[j*stride+i]
			}
		}
		return
	}
	for i := 0; i < stride; i++ {
		row := i * n0
		for j := 0; j < n0; j++ {
			work[row+j] = src[j*stride+i]
		}
	}
}

func interleaveHadamardIntoLegacy(dst, src []float64, n0, stride int, hadamard bool) {
	if hadamard {
		ordery := orderyForStride(stride)
		for i := 0; i < stride; i++ {
			row := ordery[i] * n0
			for j := 0; j < n0; j++ {
				dst[j*stride+i] = src[row+j]
			}
		}
		return
	}
	for i := 0; i < stride; i++ {
		row := i * n0
		for j := 0; j < n0; j++ {
			dst[j*stride+i] = src[row+j]
		}
	}
}

func BenchmarkHadamardWorkRoundTripCurrent(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 22, 6, false)
}

func BenchmarkHadamardWorkRoundTripLegacy(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 22, 6, false)
}

func BenchmarkHadamardWorkRoundTripCurrentStride2(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 64, 2, false)
}

func BenchmarkHadamardWorkRoundTripLegacyStride2(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 64, 2, false)
}

func BenchmarkHadamardWorkRoundTripCurrentStride12(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 12, 12, false)
}

func BenchmarkHadamardWorkRoundTripLegacyStride12(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 12, 12, false)
}

func BenchmarkHadamardWorkRoundTripCurrentStride16(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 8, 16, false)
}

func BenchmarkHadamardWorkRoundTripLegacyStride16(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 8, 16, false)
}

func BenchmarkHadamardWorkRoundTripCurrentHadamardStride8(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 15, 8, true)
}

func BenchmarkHadamardWorkRoundTripLegacyHadamardStride8(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 15, 8, true)
}

func BenchmarkHadamardWorkRoundTripCurrentHadamardStride16(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, true, 8, 16, true)
}

func BenchmarkHadamardWorkRoundTripLegacyHadamardStride16(b *testing.B) {
	benchmarkHadamardWorkRoundTrip(b, false, 8, 16, true)
}
