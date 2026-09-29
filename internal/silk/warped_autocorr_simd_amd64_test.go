//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"
)

func TestWarpedAutocorrelationWavefrontMatchesSamples(t *testing.T) {
	rng := rand.New(rand.NewSource(0x3a7c))
	for trial := range 3000 {
		order := 2 * (1 + rng.Intn(maxShapeLpcOrder/2))
		length := rng.Intn(260)
		in := make([]float32, length)
		scale := float32(math.Pow(10, float64(rng.Intn(10)-3)))
		for i := range in {
			in[i] = (rng.Float32()*2 - 1) * scale
			if rng.Intn(64) == 0 {
				in[i] = [...]float32{0, float32(math.Copysign(0, -1)), 1e-38, 3e38}[rng.Intn(4)]
			}
		}
		w := silkCReal(float32(rng.Float64()*0.9 - 0.2))
		var gotSt, gotCorr, wantSt, wantCorr warpedAutocorrState
		for i := 0; i <= order; i++ {
			v := silkCReal(rng.NormFloat64())
			gotSt[1+i], wantSt[1+i] = v, v
			c := silkCReal(rng.NormFloat64())
			gotCorr[1+i], wantCorr[1+i] = c, c
		}
		warpedAutocorrelationSections(&gotSt, &gotCorr, in, w, order)
		warpedAutocorrelationSamples(&wantSt, &wantCorr, in, w, order)
		for i := 0; i <= order; i++ {
			if math.Float64bits(gotSt[1+i]) != math.Float64bits(wantSt[1+i]) || math.Float64bits(gotCorr[1+i]) != math.Float64bits(wantCorr[1+i]) {
				t.Fatalf("trial %d order %d length %d: index %d state %v corr %v, want %v %v",
					trial, order, length, i, gotSt[1+i], gotCorr[1+i], wantSt[1+i], wantCorr[1+i])
			}
		}
	}
}

func TestWarpedAutocorrelationFLP32ZeroAlloc(t *testing.T) {
	in := make([]float32, 240)
	for i := range in {
		in[i] = float32(math.Sin(float64(i) * 0.3))
	}
	out := make([]float32, maxShapeLpcOrder+1)
	if allocs := testing.AllocsPerRun(100, func() {
		warpedAutocorrelationFLP32(out, nil, in, 0.02, len(in), maxShapeLpcOrder)
	}); allocs != 0 {
		t.Fatalf("warped autocorrelation allocated %v times", allocs)
	}
}
