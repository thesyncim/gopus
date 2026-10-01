package celt

import (
	"math"
	"math/rand"
	"testing"
)

// deemphasisChannelReference is the per-channel libopus deemphasis() loop
// written literally: a scratch buffer followed by a separate decimation pass.
func deemphasisChannelReference(y []float32, yStride int, x []float32, xStride, n, downsample int, coef, m float32, accum bool) float32 {
	if downsample > 1 {
		scratch := make([]float32, n)
		for j := range n {
			tmp := x[j*xStride] + deemphasisVerySmall + m
			m = mul32(coef, tmp)
			scratch[j] = tmp
		}
		for j := range n / downsample {
			if accum {
				y[j*yStride] = fma32(sig2res, scratch[j*downsample], y[j*yStride])
			} else {
				y[j*yStride] = sig2res * scratch[j*downsample]
			}
		}
		return m
	}
	for j := range n {
		if accum {
			tmp := x[j*xStride] + m + deemphasisVerySmall
			m = mul32(coef, tmp)
			y[j*yStride] = fma32(sig2res, tmp, y[j*yStride])
		} else {
			tmp := x[j*xStride] + deemphasisVerySmall + m
			m = mul32(coef, tmp)
			y[j*yStride] = sig2res * tmp
		}
	}
	return m
}

func TestDeemphasisMatchesPerChannelReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5eed))
	for iter := 0; iter < 3000; iter++ {
		channels := 1 + rng.Intn(2)
		downsample := []int{1, 1, 2, 3, 4, 6}[rng.Intn(6)]
		n := 1 + rng.Intn(1000)
		if rng.Intn(3) == 0 {
			n = []int{120, 240, 480, 960}[rng.Intn(4)]
		}
		n = max(n, downsample)
		accum := rng.Intn(2) == 0
		interleaved := rng.Intn(2) == 0
		scale := float32(math.Pow(10, float64(rng.Intn(8)-2)))
		planes := make([][]float32, channels)
		src := make([]float32, n*channels)
		for c := range planes {
			planes[c] = make([]float32, n)
			for i := range n {
				v := (rng.Float32()*2 - 1) * scale
				if rng.Intn(50) == 0 {
					v = 0
				}
				planes[c][i] = v
				src[i*channels+c] = v
			}
		}
		mem := make([]float32, channels)
		for c := range mem {
			mem[c] = (rng.Float32()*2 - 1) * scale
		}
		coef := float32(PreemphCoef)
		nd := n / downsample
		seed := make([]float32, nd*channels)
		for i := range seed {
			seed[i] = rng.Float32()*2 - 1
		}

		want := append([]float32(nil), seed...)
		wantMem := append([]float32(nil), mem...)
		for c := range channels {
			wantMem[c] = deemphasisChannelReference(want[c:], channels, planes[c], 1, n, downsample, coef, wantMem[c], accum)
		}

		d := NewDecoder(channels)
		copy(d.preemphState, mem)
		got := append([]float32(nil), seed...)
		if interleaved {
			x1 := src
			if channels == 2 {
				x1 = src[1:]
			}
			d.deemphasis(got, src, x1, channels, n, downsample, accum)
		} else {
			d.deemphasis(got, planes[0], planes[channels-1], 1, n, downsample, accum)
		}
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("iter %d ch=%d n=%d ds=%d accum=%v interleaved=%v: pcm[%d]=%v want %v", iter, channels, n, downsample, accum, interleaved, i, got[i], want[i])
			}
		}
		for c := range channels {
			if math.Float32bits(d.preemphState[c]) != math.Float32bits(wantMem[c]) {
				t.Fatalf("iter %d: mem[%d]=%v want %v", iter, c, d.preemphState[c], wantMem[c])
			}
		}
	}
}
