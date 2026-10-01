//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math/rand"
	"slices"
	"testing"
)

func TestRewhitenLTPMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x3e17))
	for trial := range 3000 {
		order := [...]int{10, 16, 12, 8}[trial%4]
		xq := make([]int16, 2*maxFrameLengthNSQ)
		for i := range xq {
			switch rng.Intn(6) {
			case 0:
				xq[i] = [...]int16{32767, -32768}[rng.Intn(2)]
			default:
				xq[i] = int16(rng.Intn(65536) - 32768)
			}
		}
		aQ12 := make([]int16, maxLPCOrder)
		for i := range aQ12 {
			aQ12[i] = int16(rng.Intn(65536) - 32768)
		}
		length := rng.Intn(ltpMemLength + 1)
		startIdx := 1 + rng.Intn(ltpMemLength)
		offset := rng.Intn(maxFrameLengthNSQ) - order - 8
		want := make([]int16, ltpMemLength+maxFrameLengthNSQ)
		for i := range want {
			want[i] = int16(rng.Intn(65536) - 32768)
		}
		got := slices.Clone(want)
		for i := startIdx; i < startIdx+order && i < len(want); i++ {
			want[i] = 0
		}
		rewhitenLTPScalar(want, xq, startIdx, offset, aQ12, order, length, order)
		rewhitenLTP(got, xq, startIdx, offset, aQ12, length, order)
		if !slices.Equal(got, want) {
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("trial %d order %d start %d offset %d length %d: sLTP[%d] = %d, want %d",
						trial, order, startIdx, offset, length, i, got[i], want[i])
				}
			}
		}
	}
}

func TestRewhitenLTPZeroAlloc(t *testing.T) {
	xq := make([]int16, 2*maxFrameLengthNSQ)
	for i := range xq {
		xq[i] = int16(i*37 - 9000)
	}
	aQ12 := make([]int16, maxLPCOrder)
	for i := range aQ12 {
		aQ12[i] = int16(100 * (i - 8))
	}
	sLTP := make([]int16, ltpMemLength+maxFrameLengthNSQ)
	if allocs := testing.AllocsPerRun(100, func() {
		rewhitenLTP(sLTP, xq, 100, 80, aQ12, 220, maxLPCOrder)
	}); allocs != 0 {
		t.Fatalf("rewhitenLTP allocated %v times", allocs)
	}
}
