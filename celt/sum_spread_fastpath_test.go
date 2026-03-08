package celt

import "testing"

func spreadCountThresholdsLegacy(x []float64, n int, nf float64) (t0, t1, t2 int) {
	for j := 0; j < n; j++ {
		v := x[j]
		x2N := v * v * nf
		if x2N < 0.25 {
			t0++
		}
		if x2N < 0.0625 {
			t1++
		}
		if x2N < 0.015625 {
			t2++
		}
	}
	return
}

func makeSumSpreadFastpathInput(n int) []float64 {
	out := make([]float64, n)
	x := uint32(0x12345678)
	for i := range out {
		x = 1664525*x + 1013904223
		v := float64(int32(x>>8)%4096) / 257.0
		if i%7 == 0 {
			v *= -1
		}
		out[i] = v
	}
	return out
}

func TestSpreadCountThresholdsMatchesLegacy(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 32, 63, 64} {
		x := makeSumSpreadFastpathInput(n)
		got0, got1, got2 := spreadCountThresholds(x, n, 0.375)
		want0, want1, want2 := spreadCountThresholdsLegacy(x, n, 0.375)
		if got0 != want0 || got1 != want1 || got2 != want2 {
			t.Fatalf("n=%d mismatch: got=(%d,%d,%d) want=(%d,%d,%d)", n, got0, got1, got2, want0, want1, want2)
		}
	}
}
