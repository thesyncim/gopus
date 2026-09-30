//go:build amd64.v3 && !gopus_fixed_point && (!goexperiment.simd || nosimd || purego)

package celt

import (
	"testing"
)

var thetaRDODistortionSink float32

func TestThetaRDODistortionZeroAlloc(t *testing.T) {
	const n = 24
	xSave := make([]celtNorm, n)
	xBand := make([]celtNorm, n)
	ySave := make([]celtNorm, n)
	yBand := make([]celtNorm, n)
	for i := range n {
		xSave[i] = celtNorm(float32(i+1) / 32)
		xBand[i] = celtNorm(float32(i+2) / 33)
		ySave[i] = celtNorm(float32(i+3) / 34)
		yBand[i] = celtNorm(float32(i+4) / 35)
	}
	measure := func() {
		thetaRDODistortionSink = thetaRDODistortion(0.75, 0.5, xSave, xBand, ySave, yBand)
	}
	measure()
	if allocations := testing.AllocsPerRun(100, measure); allocations != 0 {
		t.Fatalf("thetaRDODistortion allocates %v per warmed call", allocations)
	}
}
