package celt

import (
	"math"
	"math/rand"
	"testing"
)

// sameSumBits reports whether two abs-sums agree bit for bit, treating any
// two NaNs as equal: the callers only compare these sums.
func sameSumBits(a, b float32) bool {
	if math.IsNaN(float64(a)) || math.IsNaN(float64(b)) {
		return math.IsNaN(float64(a)) && math.IsNaN(float64(b))
	}
	return math.Float32bits(a) == math.Float32bits(b)
}

// TestAbsSumPairQuadFiveMatchSerial checks the build-selected multi-slice
// abs-sums against absSumSerial on each slice.
func TestAbsSumPairQuadFiveMatchSerial(t *testing.T) {
	rng := rand.New(rand.NewSource(0xab55))
	for trial := range 1000 {
		n := rng.Intn(300)
		s := make([][]float32, 5)
		for i := range s {
			s[i] = encodeKernelSlice(rng, n+rng.Intn(3))
			if trial%37 == 0 && n > 0 {
				s[i][rng.Intn(n)] = float32(math.Inf(1))
			}
			if trial%41 == 0 && n > 0 {
				s[i][rng.Intn(n)] = float32(math.NaN())
			}
		}
		want := make([]float32, 5)
		for i := range s {
			want[i] = absSumSerial(s[i][:n])
		}
		p0, p1 := absSumPair(s[0][:n], s[1])
		q0, q1, q2, q3 := absSumQuad(s[0][:n], s[1], s[2], s[3])
		f0, f1, f2, f3, f4 := absSumFive(s[0][:n], s[1], s[2], s[3], s[4])
		for i, got := range []float32{p0, p1, q0, q1, q2, q3, f0, f1, f2, f3, f4} {
			w := want[[]int{0, 1, 0, 1, 2, 3, 0, 1, 2, 3, 4}[i]]
			if !sameSumBits(got, w) {
				t.Fatalf("trial %d n=%d result %d: got %08x want %08x", trial, n, i, math.Float32bits(got), math.Float32bits(w))
			}
		}
	}
}
