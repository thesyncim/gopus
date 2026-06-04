//go:build amd64 && !purego

package celt

import (
	"math"
	"testing"
)

func TestCeltInnerProdSSEAsmMatchesLibopusOrder(t *testing.T) {
	x := make([]celtNorm, 65)
	y := make([]celtNorm, len(x))
	state := uint32(11)
	for i := range x {
		state = 1664525*state + 1013904223
		xScale := float32(1e-2)
		if state&1 != 0 {
			xScale = 1e3
		}
		x[i] = celtNorm(((float32(state)/float32(math.MaxUint32))*2 - 1) * xScale)
		state = 1664525*state + 1013904223
		yScale := float32(1e-2)
		if state&1 != 0 {
			yScale = 1e3
		}
		y[i] = celtNorm(((float32(state)/float32(math.MaxUint32))*2 - 1) * yScale)
	}

	for n := 0; n <= len(x); n++ {
		got := celtInnerProdSSEStyleAsm(x[:n], y[:n])
		want := celtInnerProdSSEStyleGo(x[:n], y[:n])
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("n=%d: asm=%08x(%v), want libopus-order %08x(%v)",
				n, math.Float32bits(got), got, math.Float32bits(want), want)
		}
	}
}
