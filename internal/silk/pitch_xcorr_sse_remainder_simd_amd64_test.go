//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"math"
	"math/rand"
	"testing"
)

// sseOrderInnerProductRef is scalar libopus celt_inner_prod_sse: four lane
// accumulators, the (a0+a2)+(a1+a3) reduction, then a serial tail.
func sseOrderInnerProductRef(x, y []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i+4 <= n; i += 4 {
		for l := range 4 {
			acc[l] += noFMA32(x[i+l], y[i+l])
		}
	}
	sum := (acc[0] + acc[2]) + (acc[1] + acc[3])
	for ; i < n; i++ {
		sum += noFMA32(x[i], y[i])
	}
	return sum
}

// TestCeltPitchXcorrAVX2RemainderUsesSSEOrder pins the lags past the last
// eight-lag AVX2 block to celt_inner_prod_sse, which celt_pitch_xcorr_avx2
// calls through the x86 dispatch table.
func TestCeltPitchXcorrAVX2RemainderUsesSSEOrder(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2+FMA pitch xcorr is not selected on this CPU")
	}
	rng := rand.New(rand.NewSource(9))
	for _, length := range []int{5, 40, 80, 81, 120} {
		for _, maxPitch := range []int{3, 11, 17, 22, 149} {
			x := make([]float32, length)
			y := make([]float32, length+maxPitch+8)
			for i := range x {
				x[i] = rng.Float32()*2 - 1
			}
			for i := range y {
				y[i] = rng.Float32()*2 - 1
			}
			out := make([]float32, maxPitch)
			celtPitchXcorrFloat(x, y, out, length, maxPitch)
			for i := maxPitch &^ 7; i < maxPitch; i++ {
				want := sseOrderInnerProductRef(x, y[i:], length)
				if math.Float32bits(out[i]) != math.Float32bits(want) {
					t.Fatalf("len=%d maxPitch=%d lag %d = %08x, want %08x", length, maxPitch, i,
						math.Float32bits(out[i]), math.Float32bits(want))
				}
			}
		}
	}
}
