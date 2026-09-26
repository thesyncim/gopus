//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestCombFilterWithInputSigUsesSSEConstOrder pins the constant-gain body of
// the prefilter/PLC comb filter to libopus comb_filter_const_sse, which the
// x86 SIMD build binds statically (OPUS_X86_PRESUME_SSE): each output is
// (x + g10*x[-T]) + (g11*(x[-T+1]+x[-T-1]) + g12*(x[-T+2]+x[-T-2])).
func TestCombFilterWithInputSigUsesSSEConstOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	const maxPeriod, n, overlap = 1024, 960, 120
	for _, tapset := range []int{0, 1, 2} {
		for _, period := range []int{15, 48, 200, 1020} {
			src := make([]celtSig, maxPeriod+n)
			for i := range src {
				src[i] = celtSig(rng.Float32()*2e4 - 1e4)
			}
			window := make([]float32, overlap)
			for i := range window {
				window[i] = rng.Float32()
			}
			dst := make([]celtSig, len(src))
			g := float32(0.5625)
			combFilterWithInputSig(dst, src, maxPeriod, period, period, n, g, g, tapset, tapset, window, overlap)

			g10 := combGain32(g, tapset, 0)
			g11 := combGain32(g, tapset, 1)
			g12 := combGain32(g, tapset, 2)
			for i := 0; i < n; i++ {
				x := func(k int) float32 { return float32(src[maxPeriod+i+k]) }
				want := combFilterConstSSEValue(x(0), g10, g11, g12, x(-period), x(-period+1), x(-period-1), x(-period+2), x(-period-2))
				if got := float32(dst[maxPeriod+i]); math.Float32bits(got) != math.Float32bits(want) {
					t.Fatalf("tapset=%d T=%d y[%d] = %08x, want %08x", tapset, period, i,
						math.Float32bits(got), math.Float32bits(want))
				}
			}
		}
	}
}
