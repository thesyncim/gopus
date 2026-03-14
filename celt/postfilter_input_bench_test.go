package celt

import (
	"math"
	"math/rand"
	"testing"
)

func combFilterWithInputF32Legacy(dst, src []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		copy(dst[start:start+n], src[start:start+n])
		return
	}

	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if window == nil {
		overlap = 0
	}
	if overlap > n {
		overlap = n
	}
	if window != nil && overlap > len(window) {
		overlap = len(window)
	}

	if tapset0 < 0 || tapset0 >= len(combFilterGains) {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= len(combFilterGains) {
		tapset1 = 0
	}

	g00 := float32(g0 * combFilterGains[tapset0][0])
	g01 := float32(g0 * combFilterGains[tapset0][1])
	g02 := float32(g0 * combFilterGains[tapset0][2])
	g10 := float32(g1 * combFilterGains[tapset1][0])
	g11 := float32(g1 * combFilterGains[tapset1][1])
	g12 := float32(g1 * combFilterGains[tapset1][2])

	x1 := float32(src[start-t1+1])
	x2 := float32(src[start-t1])
	x3 := float32(src[start-t1-1])
	x4 := float32(src[start-t1-2])

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	i := 0
	for ; i < overlap; i++ {
		w := float32(window[i])
		f := noFMA32Mul(w, w)
		oneMinus := float32(1.0) - f
		idx := start + i
		x0 := float32(src[idx-t1+2])
		var sum float32
		if tmpCombFilterSeqAccumEnabled {
			sum = float32(src[idx])
			sum += (oneMinus * g00) * float32(src[idx-t0])
			sum += (oneMinus * g01) * (float32(src[idx-t0-1]) + float32(src[idx-t0+1]))
			sum += (oneMinus * g02) * (float32(src[idx-t0-2]) + float32(src[idx-t0+2]))
			sum += (f * g10) * x2
			sum += (f * g11) * (x1 + x3)
			sum += (f * g12) * (x0 + x4)
		} else if tmpCombFilterFMAOverlapEnabled {
			sum = float32(src[idx])
			sum = fma32(oneMinus*g00, float32(src[idx-t0]), sum)
			sum = fma32(oneMinus*g01, float32(src[idx-t0+1])+float32(src[idx-t0-1]), sum)
			sum = fma32(oneMinus*g02, float32(src[idx-t0+2])+float32(src[idx-t0-2]), sum)
			sum = fma32(f*g10, x2, sum)
			sum = fma32(f*g11, x1+x3, sum)
			sum = fma32(f*g12, x0+x4, sum)
		} else {
			sum = float32(src[idx]) +
				(oneMinus*g00)*float32(src[idx-t0]) +
				(oneMinus*g01)*(float32(src[idx-t0-1])+float32(src[idx-t0+1])) +
				(oneMinus*g02)*(float32(src[idx-t0-2])+float32(src[idx-t0+2])) +
				(f*g10)*x2 +
				(f*g11)*(x1+x3) +
				(f*g12)*(x0+x4)
		}
		dst[idx] = float64(sum)

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		if i < n {
			copy(dst[start+i:start+n], src[start+i:start+n])
		}
		return
	}

	x4 = float32(src[start+i-t1-2])
	x3 = float32(src[start+i-t1-1])
	x2 = float32(src[start+i-t1])
	x1 = float32(src[start+i-t1+1])
	for ; i < n; i++ {
		idx := start + i
		x0 := float32(src[idx-t1+2])
		var sum float32
		if tmpCombFilterSeqAccumEnabled {
			sum = float32(src[idx])
			sum += g10 * x2
			sum += g11 * (x3 + x1)
			sum += g12 * (x4 + x0)
		} else {
			sum = float32(src[idx]) +
				g10*x2 +
				g11*(x3+x1) +
				g12*(x4+x0)
		}
		dst[idx] = float64(sum)

		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

func TestCombFilterWithInputF32MatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(91))
	window := GetWindowBuffer(Overlap)
	for trial := 0; trial < 300; trial++ {
		n := rng.Intn(960) + 1
		start := combFilterHistory
		bufLen := start + n + combFilterMaxPeriod + 4
		src := make([]float64, bufLen)
		for i := range src {
			src[i] = rng.Float64()*2 - 1
		}
		got := append([]float64(nil), src...)
		want := append([]float64(nil), src...)

		t0 := combFilterMinPeriod + rng.Intn(combFilterMaxPeriod-combFilterMinPeriod)
		t1 := combFilterMinPeriod + rng.Intn(combFilterMaxPeriod-combFilterMinPeriod)
		tapset0 := rng.Intn(len(combFilterGains))
		tapset1 := rng.Intn(len(combFilterGains))
		g0 := rng.Float64()*1.5 - 0.75
		g1 := rng.Float64()*1.5 - 0.75
		overlap := rng.Intn(min(n, Overlap) + 1)
		var win []float64
		if rng.Intn(2) == 0 {
			win = window
		}

		combFilterWithInputF32(got, src, start, t0, t1, n, g0, g1, tapset0, tapset1, win, overlap)
		combFilterWithInputF32Legacy(want, src, start, t0, t1, n, g0, g1, tapset0, tapset1, win, overlap)

		for i := 0; i < len(got); i++ {
			if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
				t.Fatalf("trial %d index %d mismatch: got=%#x want=%#x", trial, i, math.Float64bits(got[i]), math.Float64bits(want[i]))
			}
		}
	}
}

func benchmarkCombFilterWithInputF32(b *testing.B, legacy bool) {
	start := combFilterHistory
	n := 960
	bufLen := start + n + combFilterMaxPeriod + 4
	src := make([]float64, bufLen)
	dst := make([]float64, bufLen)
	for i := range src {
		src[i] = math.Sin(float64(i)*0.013) + 0.25*math.Cos(float64(i)*0.041)
	}
	window := GetWindowBuffer(Overlap)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(dst, src)
		if legacy {
			combFilterWithInputF32Legacy(dst, src, start, 100, 96, n, 0.4375, 0.3125, 1, 2, window, Overlap)
		} else {
			combFilterWithInputF32(dst, src, start, 100, 96, n, 0.4375, 0.3125, 1, 2, window, Overlap)
		}
	}
}

func BenchmarkCombFilterWithInputF32Current(b *testing.B) {
	benchmarkCombFilterWithInputF32(b, false)
}

func BenchmarkCombFilterWithInputF32Legacy(b *testing.B) {
	benchmarkCombFilterWithInputF32(b, true)
}
