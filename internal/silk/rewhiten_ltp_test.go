package silk

import (
	"math/rand"
	"testing"
)

// TestRewhitenLTPMatchesGuardedScalar checks rewhitenLTP against the
// per-tap guarded rewhitenLTPScalar on random histories, including windows
// that run past either end of xq or sLTP.
func TestRewhitenLTPMatchesGuardedScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4e7a))
	for iter := range 3000 {
		order := minLPCOrder
		switch iter % 3 {
		case 0:
			order = maxLPCOrder
		case 1:
			order = 2 * (1 + rng.Intn(maxLPCOrder/2))
		}
		xq := make([]int16, 64+rng.Intn(900))
		for i := range xq {
			xq[i] = int16(rng.Intn(1 << 16))
		}
		aQ12 := make([]int16, order)
		for i := range aQ12 {
			aQ12[i] = int16(rng.Intn(1 << 16))
		}
		sLTPLen := 32 + rng.Intn(700)
		startIdx := rng.Intn(sLTPLen)
		offset := rng.Intn(len(xq)) - startIdx/2
		length := order + rng.Intn(400)

		got := make([]int16, sLTPLen)
		want := make([]int16, sLTPLen)
		for i := range got {
			got[i] = int16(rng.Intn(1 << 16))
			want[i] = got[i]
		}
		rewhitenLTP(got, xq, startIdx, offset, aQ12, length, order)
		for i := startIdx; i < startIdx+order && i < len(want); i++ {
			want[i] = 0
		}
		rewhitenLTPScalar(want, xq, startIdx, offset, aQ12, order, length, order)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d (order %d start %d offset %d length %d): sLTP[%d]=%d want %d",
					iter, order, startIdx, offset, length, i, got[i], want[i])
			}
		}
	}
}
