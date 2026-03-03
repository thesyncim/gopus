package celt

import (
	"math/rand"
	"testing"
)

var pitchDownsampleSink float64

func pitchDownsampleLegacy(x []float64, xLP []float64, length, channels, factor int) {
	if length <= 0 || factor <= 0 || len(xLP) < length {
		return
	}
	offset := factor / 2
	if offset < 1 {
		offset = 1
	}
	for i := 1; i < length; i++ {
		idx := factor * i
		v := float32(0.25)*float32(x[idx-offset]) +
			float32(0.25)*float32(x[idx+offset]) +
			float32(0.5)*float32(x[idx])
		xLP[i] = float64(v)
	}
	xLP[0] = float64(float32(0.25)*float32(x[offset]) + float32(0.5)*float32(x[0]))
	if channels == 2 {
		chStride := len(x) / 2
		x1 := x[chStride:]
		for i := 1; i < length; i++ {
			idx := factor * i
			v := float32(0.25)*float32(x1[idx-offset]) +
				float32(0.25)*float32(x1[idx+offset]) +
				float32(0.5)*float32(x1[idx])
			xLP[i] = float64(float32(xLP[i]) + v)
		}
		xLP[0] = float64(float32(xLP[0]) + float32(0.25)*float32(x1[offset]) + float32(0.5)*float32(x1[0]))
	}

	var ac [5]float64
	lp := xLP[:length]
	pitchAutocorr5(lp, length, &ac)

	ac[0] = float64(float32(ac[0]) * float32(1.0001))
	for i := 1; i <= 4; i++ {
		f := float32(0.008) * float32(i)
		ac[i] = float64(float32(ac[i]) - float32(ac[i])*f*f)
	}

	lpc := lpcFromAutocorr(ac)
	tmp := float32(1.0)
	for i := 0; i < 4; i++ {
		tmp *= float32(0.9)
		lpc[i] = float64(float32(lpc[i]) * tmp)
	}
	c1 := float32(0.8)
	lpc2 := [5]float64{
		float64(float32(lpc[0]) + float32(0.8)),
		float64(float32(lpc[1]) + c1*float32(lpc[0])),
		float64(float32(lpc[2]) + c1*float32(lpc[1])),
		float64(float32(lpc[3]) + c1*float32(lpc[2])),
		float64(c1 * float32(lpc[3])),
	}
	celtFIR5(xLP, lpc2)
}

func benchmarkPitchDownsample(b *testing.B, channels int, fn func([]float64, []float64, int, int, int)) {
	rng := rand.New(rand.NewSource(43))
	length := (combFilterMaxPeriod + 480) >> 1
	xLen := 2 * length
	if channels == 2 {
		xLen *= 2
	}
	x := make([]float64, xLen)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	xLP := make([]float64, length)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(x, xLP, length, channels, 2)
	}
	pitchDownsampleSink = xLP[length-1]
}

func BenchmarkPitchDownsampleCurrentMono(b *testing.B) {
	benchmarkPitchDownsample(b, 1, pitchDownsample)
}

func BenchmarkPitchDownsampleLegacyMono(b *testing.B) {
	benchmarkPitchDownsample(b, 1, pitchDownsampleLegacy)
}

func BenchmarkPitchDownsampleCurrentStereo(b *testing.B) {
	benchmarkPitchDownsample(b, 2, pitchDownsample)
}

func BenchmarkPitchDownsampleLegacyStereo(b *testing.B) {
	benchmarkPitchDownsample(b, 2, pitchDownsampleLegacy)
}
