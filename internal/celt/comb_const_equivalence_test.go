package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestCombFilterConstMatchesSequentialReference runs the constant-gain comb
// filter in place, as the decoder postfilter does, against a sequential
// per-output reference with the same operation order: the SSE-prefix order
// where the build uses comb_filter_const_sse, the scalar order otherwise.
func TestCombFilterConstMatchesSequentialReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0b))
	for iter := 0; iter < 20000; iter++ {
		T := combFilterMinPeriod + rng.Intn(300)
		n := 1 + rng.Intn(480)
		H := T + 2
		x := make([]float32, H+n)
		for i := range x {
			x[i] = (rng.Float32()*2 - 1) * float32(math.Pow(10, float64(rng.Intn(6)-1)))
		}
		g10 := rng.Float32()
		g11 := rng.Float32() * 0.5
		g12 := rng.Float32() * 0.25
		sseCount := rng.Intn(n + 1)
		if rng.Intn(2) == 0 {
			sseCount = n &^ 3
		}
		want := append([]float32(nil), x...)
		for i := range n {
			p := H + i
			if combUsesSSE && i < sseCount {
				want[p] = combFilterConstSSEValue(want[p], g10, g11, g12, want[p-T], want[p-T+1], want[p-T-1], want[p-T+2], want[p-T-2])
			} else {
				want[p] = combFilterConstValue(want[p], g10, g11, g12, want[p-T], want[p-T+1], want[p-T-1], want[p-T+2], want[p-T-2])
			}
		}
		got := x
		x4, x3, x2, x1 := got[H-T-2], got[H-T-1], got[H-T], got[H-T+1]
		c4, c3, c2, c1 := combFilterConstFloat32(got[H:H+n], got[H-T+2:H-T+2+n], g10, g11, g12, x4, x3, x2, x1, sseCount)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d T=%d n=%d sse=%d: x[%d] = %v, want %v", iter, T, n, sseCount, i, got[i], want[i])
			}
		}
		e := H + n - T + 2
		if c4 != want[e-4] || c3 != want[e-3] || c2 != want[e-2] || c1 != want[e-1] {
			t.Fatalf("iter %d: carries (%v,%v,%v,%v) want (%v,%v,%v,%v)", iter, c4, c3, c2, c1, want[e-4], want[e-3], want[e-2], want[e-1])
		}
	}
}

func TestCombFilterKernelsZeroAllocs(t *testing.T) {
	const n = 120
	dst := make([]float32, n)
	d0 := make([]float32, n+4)
	d1 := make([]float32, n+4)
	wsq := make([]float32, n)
	for i := range d0 {
		d0[i] = float32((i*37)%191-95) / 64
		d1[i] = float32((i*53)%173-86) / 80
	}
	for i := range wsq {
		wsq[i] = float32((i*29)%101) / 100
		dst[i] = float32((i*17)%89-44) / 32
	}
	if allocs := testing.AllocsPerRun(100, func() {
		combFilterOverlap(dst, d0, d1, wsq, 0.125, -0.0625, 0.03125, 0.25, -0.125, 0.0625)
	}); allocs != 0 {
		t.Fatalf("comb overlap allocated: %g allocs/run", allocs)
	}

	squareSamples := make([]float32, n)
	squareHistory := make([]celtSig, combFilterHistory)
	for i := range squareHistory {
		squareHistory[i] = celtSig(float32((i*47)%197-98) / 64)
	}
	squareWindow := GetWindowBufferF32(Overlap)
	squareWindowSq := GetWindowSquareBufferF32(Overlap)
	squareRun := func() {
		combFilterWithSquarePlanarFloat32(squareSamples, squareHistory, combFilterHistory, 0,
			37, 40, n, 0.28125, 0.65625, 0, 0, squareWindow, nil, Overlap)
	}
	squareRun()
	if allocs := testing.AllocsPerRun(100, squareRun); allocs != 0 {
		t.Fatalf("comb square fallback allocated: %g allocs/run", allocs)
	}
	squarePrecomputedRun := func() {
		combFilterWithSquarePlanarFloat32(squareSamples, squareHistory, combFilterHistory, 0,
			37, 40, n, 0.28125, 0.65625, 0, 0, squareWindow, squareWindowSq, Overlap)
	}
	squarePrecomputedRun()
	if allocs := testing.AllocsPerRun(100, squarePrecomputedRun); allocs != 0 {
		t.Fatalf("comb precomputed-window seam allocated: %g allocs/run", allocs)
	}

	constBody := make([]float32, n)
	delay := make([]float32, n)
	for i := range delay {
		delay[i] = float32((i*43)%211-105) / 64
	}
	if allocs := testing.AllocsPerRun(100, func() {
		combFilterConstFloat32(constBody, delay, 0.25, 0.125, 0.0625, 0, 0, 0, 0, 0)
	}); allocs != 0 {
		t.Fatalf("comb constant body allocated: %g allocs/run", allocs)
	}
}

// combFilterReference is libopus celt/celt.c comb_filter run in place on one
// contiguous buffer (y == x), with the constant part in the operation order
// the build's comb_filter_const uses.
func combFilterReference(x []float32, at, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, windowSq []float32, overlap int) {
	if g0 == 0 && g1 == 0 {
		return
	}
	t0 = max(t0, combFilterMinPeriod)
	t1 = max(t1, combFilterMinPeriod)
	g00, g01, g02 := combGain32(g0, tapset0, 0), combGain32(g0, tapset0, 1), combGain32(g0, tapset0, 2)
	g10, g11, g12 := combGain32(g1, tapset1, 0), combGain32(g1, tapset1, 1), combGain32(g1, tapset1, 2)
	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}
	overlap = min(overlap, n)
	i := 0
	for ; i < overlap; i++ {
		p := at + i
		f := windowSq[i]
		oneMinus := float32(1.0) - f
		x[p] = x[p] +
			(oneMinus*g00)*x[p-t0] +
			(oneMinus*g01)*(x[p-t0+1]+x[p-t0-1]) +
			(oneMinus*g02)*(x[p-t0+2]+x[p-t0-2]) +
			(f*g10)*x[p-t1] +
			(f*g11)*(x[p-t1+1]+x[p-t1-1]) +
			(f*g12)*(x[p-t1+2]+x[p-t1-2])
	}
	if g1 == 0 {
		return
	}
	sseEnd := i + ((n - i) &^ 3)
	for ; i < n; i++ {
		p := at + i
		if combUsesSSE && i < sseEnd {
			x[p] = combFilterConstSSEValue(x[p], g10, g11, g12, x[p-t1], x[p-t1+1], x[p-t1-1], x[p-t1+2], x[p-t1-2])
		} else {
			x[p] = combFilterConstValue(x[p], g10, g11, g12, x[p-t1], x[p-t1+1], x[p-t1-1], x[p-t1+2], x[p-t1-2])
		}
	}
}

func TestCombFilterWithSquarePlanarMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0c))
	window := GetWindowBufferF32(Overlap)
	windowSq := make([]float32, len(window))
	for i, w := range window {
		windowSq[i] = noFMA32Mul(w, w)
	}
	history := combFilterHistory
	for iter := 0; iter < 20000; iter++ {
		frameSize := []int{120, 240, 480, 960}[rng.Intn(4)]
		frameOffset := 0
		n := frameSize
		if rng.Intn(3) == 0 {
			frameOffset = 120
			n = frameSize - 120
			if n <= 0 {
				continue
			}
		}
		hist := make([]celtSig, history)
		samples := make([]float32, frameSize)
		for i := range hist {
			hist[i] = (rng.Float32()*2 - 1) * 1000
		}
		for i := range samples {
			samples[i] = (rng.Float32()*2 - 1) * 1000
		}
		pick := func() int { return rng.Intn(combFilterMaxPeriod + 1) }
		t0, t1 := pick(), pick()
		if rng.Intn(3) == 0 {
			t0 = combFilterMinPeriod + rng.Intn(140)
			t1 = combFilterMinPeriod + rng.Intn(140)
		}
		g0 := float32(rng.Intn(8)) * 0.09375
		g1 := float32(rng.Intn(8)) * 0.09375
		tap0, tap1 := rng.Intn(3), rng.Intn(3)
		if rng.Intn(4) == 0 {
			t1, g1, tap1 = t0, g0, tap0
		}

		full := make([]float32, history+frameSize)
		copy(full, hist)
		copy(full[history:], samples)
		combFilterReference(full, history+frameOffset, t0, t1, n, g0, g1, tap0, tap1, windowSq, Overlap)

		combFilterWithSquarePlanarFloat32(samples, hist, history, frameOffset, t0, t1, n, g0, g1, tap0, tap1, window, windowSq, Overlap)
		for i := range samples {
			if math.Float32bits(samples[i]) != math.Float32bits(full[history+i]) {
				t.Fatalf("iter %d T0=%d T1=%d off=%d n=%d: samples[%d] = %v, want %v", iter, t0, t1, frameOffset, n, i, samples[i], full[history+i])
			}
		}
	}
}
