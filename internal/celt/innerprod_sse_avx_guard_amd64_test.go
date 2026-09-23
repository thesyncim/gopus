//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"testing"
)

func TestCeltInnerProdSSEStyleAVXDisabledUsesScalarPath(t *testing.T) {
	x := []celtNorm{1.25, -2.5, 3.75, -4.125, 5.5, -6.25, 7.75}
	y := []celtNorm{-0.5, 1.125, -1.75, 2.25, -2.75, 3.5, -4.25}
	got := celtInnerProdSSEStyleDispatch(x, y, false)
	want := celtInnerProdSSEStyleGo(x, y)
	if math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("AVX-disabled result=%08x (%v), scalar result=%08x (%v)", math.Float32bits(got), got, math.Float32bits(want), want)
	}
}
