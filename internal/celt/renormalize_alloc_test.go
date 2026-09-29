package celt

import "testing"

func TestRenormalizeVectorZeroAllocs(t *testing.T) {
	x := make([]celtNorm, 177)
	for i := range x {
		x[i] = celtNorm(float32(i%13-6) / 13)
	}

	renormalizeVector(x, 1)
	if got := testing.AllocsPerRun(100, func() {
		renormalizeVector(x, 1)
	}); got != 0 {
		t.Fatalf("renormalizeVector allocations/run=%v, want 0", got)
	}
}
