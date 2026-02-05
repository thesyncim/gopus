//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

func TestSilkGainsQuantIntoMatchesLibopus(t *testing.T) {
	const nbSubfr = 4
	state := uint32(1)
	next := func() uint32 {
		state = state*1664525 + 1013904223
		return state
	}

	for i := 0; i < 1000; i++ {
		prev := int8(next() % nLevelsQGain)
		conditional := (next() & 1) == 0

		gains := make([]int32, nbSubfr)
		for k := 0; k < nbSubfr; k++ {
			// Cover typical and edge gain ranges in Q16.
			switch next() % 5 {
			case 0:
				gains[k] = 1 + int32(next()%256)
			case 1:
				gains[k] = int32(GainDequantTable[next()%nLevelsQGain])
			case 2:
				gains[k] = 1 << 16
			case 3:
				gains[k] = int32(20000 + next()%100000)
			default:
				gains[k] = int32(1 + next()%0x3FFFFFFF)
			}
			if gains[k] <= 0 {
				gains[k] = 1
			}
		}

		goInd := make([]int8, nbSubfr)
		goGains := append([]int32(nil), gains...)
		goPrev := silkGainsQuantInto(goInd, goGains, prev, conditional, nbSubfr)

		libInd, libGains, libPrev := cgowrap.GainQuantizeVector(gains, prev, conditional, nbSubfr)

		for k := 0; k < nbSubfr; k++ {
			if goInd[k] != libInd[k] {
				t.Fatalf("case %d subfr %d index mismatch: go=%d lib=%d prev=%d cond=%v gains=%v", i, k, goInd[k], libInd[k], prev, conditional, gains)
			}
			if goGains[k] != libGains[k] {
				t.Fatalf("case %d subfr %d gain mismatch: go=%d lib=%d prev=%d cond=%v gains=%v", i, k, goGains[k], libGains[k], prev, conditional, gains)
			}
		}
		if goPrev != libPrev {
			t.Fatalf("case %d prev mismatch: go=%d lib=%d prevIn=%d cond=%v gains=%v", i, goPrev, libPrev, prev, conditional, gains)
		}
	}
}
