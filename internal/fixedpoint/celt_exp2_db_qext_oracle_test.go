//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTExp2DBFixedQEXTMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	values := []int32{0, 1, 127, 1<<20 - 1, 1 << 23, 1<<24 - 1, -1, -1 << 23,
		-8 << 24, -16 << 24, -17 << 24, 14 << 24, 15 << 24, 28 << 24}
	rng := rand.New(rand.NewSource(0x51455854))
	for i := 0; i < 512; i++ {
		values = append(values, int32(rng.Uint32()%uint32(40<<24)-(20<<24)))
	}
	wantFrac, wantWhole, err := libopustest.ProbeCELTExp2DBFixedQEXT(values)
	if err != nil {
		t.Fatal(err)
	}
	for i, x := range values {
		fracInput := x & ((1 << dbShift) - 1)
		if got := celtExp2DbFrac(fracInput); got != wantFrac[i] {
			t.Fatalf("frac[%d] x=%d got=%d want=%d", i, fracInput, got, wantFrac[i])
		}
		if got := celtExp2Db(x); got != wantWhole[i] {
			t.Fatalf("whole[%d] x=%d got=%d want=%d", i, x, got, wantWhole[i])
		}
	}
}
