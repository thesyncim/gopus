package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/util"
)

func removeDoublingLegacyYYLookup(x []float64, maxPeriod, minPeriod, N int, T0 *int, prevPeriod int, prevGain float64) float64 {
	minPeriod0 := minPeriod
	maxPeriod >>= 1
	minPeriod >>= 1
	*T0 >>= 1
	prevPeriod >>= 1
	N >>= 1
	if maxPeriod <= 0 || N <= 0 {
		return 0
	}

	xBase := x
	if *T0 >= maxPeriod {
		*T0 = maxPeriod - 1
	}
	T0val := *T0
	x0 := xBase[maxPeriod:]
	xx64, xy64 := prefilterDualInnerProd(x0, x0, xBase[maxPeriod-T0val:maxPeriod-T0val+N], N)
	xx := float32(xx64)
	xy := float32(xy64)

	yyLookup := make([]float64, maxPeriod+1)
	yy := xx
	yyLookup[0] = float64(yy)
	for i := 1; i <= maxPeriod; i++ {
		v1 := float32(xBase[maxPeriod-i])
		v2 := float32(xBase[maxPeriod+N-i])
		yy += v1*v1 - v2*v2
		if yy < 0 {
			yy = 0
		}
		yyLookup[i] = float64(yy)
	}

	yy = float32(yyLookup[T0val])
	bestXY := xy
	bestYY := yy
	g := computePitchGain(xy, xx, yy)
	g0 := g
	T := T0val

	for k := 2; k <= 15; k++ {
		T1 := (2*T0val + k) / (2 * k)
		if T1 < minPeriod {
			break
		}
		var T1b int
		if k == 2 {
			if T1+T0val > maxPeriod {
				T1b = T0val
			} else {
				T1b = T0val + T1
			}
		} else {
			T1b = (2*secondCheck[k]*T0val + k) / (2 * k)
		}
		xy1, xy2 := prefilterDualInnerProd(x0, xBase[maxPeriod-T1:maxPeriod-T1+N], xBase[maxPeriod-T1b:maxPeriod-T1b+N], N)
		xy = float32(0.5) * (float32(xy1) + float32(xy2))
		yy = float32(0.5) * (float32(yyLookup[T1]) + float32(yyLookup[T1b]))
		g1 := computePitchGain(xy, xx, yy)
		cont := float32(0)
		if util.Abs(T1-prevPeriod) <= 1 {
			cont = float32(prevGain)
		} else if util.Abs(T1-prevPeriod) <= 2 && 5*k*k < T0val {
			cont = float32(0.5) * float32(prevGain)
		}
		thresh := maxFloat32(float32(0.3), float32(0.7)*g0-cont)
		if T1 < 3*minPeriod {
			thresh = maxFloat32(float32(0.4), float32(0.85)*g0-cont)
		} else if T1 < 2*minPeriod {
			thresh = maxFloat32(float32(0.5), float32(0.9)*g0-cont)
		}
		if g1 > thresh {
			bestXY = xy
			bestYY = yy
			T = T1
			g = g1
		}
	}

	if bestXY < 0 {
		bestXY = 0
	}
	pg := g
	if bestYY > bestXY {
		pg = bestXY / (bestYY + float32(1))
		if pg > g {
			pg = g
		}
	}

	var xcorr [3]float32
	for k := 0; k < 3; k++ {
		lag := T + k - 1
		xcorr[k] = float32(celtInnerProd(x0, xBase[maxPeriod-lag:maxPeriod-lag+N], N))
	}
	offset := 0
	if (xcorr[2] - xcorr[0]) > float32(0.7)*(xcorr[1]-xcorr[0]) {
		offset = 1
	} else if (xcorr[0] - xcorr[2]) > float32(0.7)*(xcorr[1]-xcorr[2]) {
		offset = -1
	}
	*T0 = 2*T + offset
	if *T0 < minPeriod0 {
		*T0 = minPeriod0
	}
	return float64(pg)
}

func TestRemoveDoublingMatchesLegacyYYLookup(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 500; iter++ {
		maxPeriod := combFilterMinPeriod + 8 + rng.Intn(320)
		if maxPeriod%2 != 0 {
			maxPeriod++
		}
		minPeriod := combFilterMinPeriod + rng.Intn(max(1, maxPeriod/6))
		if minPeriod >= maxPeriod {
			minPeriod = maxPeriod - 1
		}
		if minPeriod < combFilterMinPeriod {
			minPeriod = combFilterMinPeriod
		}
		N := 120 * (1 << rng.Intn(4))
		if N%2 != 0 {
			N++
		}

		x := make([]float64, maxPeriod+N)
		for i := range x {
			sine := math.Sin(float64(i+iter) * 0.031)
			cosine := math.Cos(float64(i*3+iter*5) * 0.017)
			step := float64((i*11+iter*7)%19-9) / 18.0
			x[i] = 0.6*sine + 0.3*cosine + step
		}

		t0Base := minPeriod + rng.Intn(maxPeriod-minPeriod)
		prevPeriod := minPeriod + rng.Intn(maxPeriod-minPeriod)
		prevGain := rng.Float64() * 0.95

		t0Current := t0Base
		t0Legacy := t0Base
		var scratch encoderScratch

		got := removeDoubling(x, maxPeriod, minPeriod, N, &t0Current, prevPeriod, prevGain, &scratch)
		want := removeDoublingLegacyYYLookup(x, maxPeriod, minPeriod, N, &t0Legacy, prevPeriod, prevGain)

		if t0Current != t0Legacy {
			t.Fatalf("iter %d lag mismatch: got=%d want=%d", iter, t0Current, t0Legacy)
		}
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("iter %d gain mismatch: got=%0.9g want=%0.9g", iter, got, want)
		}
	}
}

func BenchmarkRemoveDoublingCurrent(b *testing.B) {
	const (
		maxPeriod = 1024
		minPeriod = combFilterMinPeriod
		N         = 960
	)

	x := make([]float64, maxPeriod+N)
	for i := range x {
		x[i] = 0.5*math.Sin(float64(i)*0.021) + 0.25*math.Cos(float64(i)*0.037)
	}

	prevPeriod := 188
	prevGain := 0.42
	var scratch encoderScratch

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t0 := 193
		_ = removeDoubling(x, maxPeriod, minPeriod, N, &t0, prevPeriod, prevGain, &scratch)
	}
}

func BenchmarkRemoveDoublingLegacy(b *testing.B) {
	const (
		maxPeriod = 1024
		minPeriod = combFilterMinPeriod
		N         = 960
	)

	x := make([]float64, maxPeriod+N)
	for i := range x {
		x[i] = 0.5*math.Sin(float64(i)*0.021) + 0.25*math.Cos(float64(i)*0.037)
	}

	prevPeriod := 188
	prevGain := 0.42

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t0 := 193
		_ = removeDoublingLegacyYYLookup(x, maxPeriod, minPeriod, N, &t0, prevPeriod, prevGain)
	}
}
