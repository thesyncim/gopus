//go:build arm64 && !purego

package silk

import (
	"math/rand"
	"testing"
)

func synthesizeLPCOrder16CoreRef(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	var v [16]int32
	for k := 0; k < 16; k++ {
		v[k] = sLPC[maxLPCOrder-1-k]
	}
	sIdx := maxLPCOrder
	for i := 0; i < subfrLength; i++ {
		lpcPredQ10 := int32(maxLPCOrder >> 1)
		for k := 0; k < 16; k++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, v[k], int32(A_Q12[k]))
		}
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(lpcPredQ10))
		sLPC[sIdx] = s
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
		sIdx++
		copy(v[1:], v[:15])
		v[0] = s
	}
}

// TestSynthesizeLPCOrder16CoreBitExact pins the NEON tap-tree kernel to the
// sequential scalar reference bit-for-bit, including states/coefficients that
// drive the saturating epilogue and int32-wrapping accumulations.
func TestSynthesizeLPCOrder16CoreBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	for trial := 0; trial < 200; trial++ {
		subfr := 1 + rng.Intn(120)
		coefs := make([]int16, 16)
		for i := range coefs {
			coefs[i] = int16(rng.Intn(1<<16) - 1<<15)
		}
		sLPCa := make([]int32, maxLPCOrder+subfr)
		pres := make([]int32, subfr)
		for i := range sLPCa {
			sLPCa[i] = rng.Int31() - rng.Int31()
			if trial%3 == 0 {
				// Large states exercise wraparound and saturation.
				sLPCa[i] = int32(rng.Uint32())
			}
		}
		for i := range pres {
			pres[i] = int32(rng.Uint32())
			if trial%2 == 0 {
				pres[i] = rng.Int31()>>8 - rng.Int31()>>9
			}
		}
		gain := int32(rng.Uint32())
		sLPCb := append([]int32(nil), sLPCa...)
		pxqA := make([]int16, subfr)
		pxqB := make([]int16, subfr)

		synthesizeLPCOrder16Core(sLPCa, coefs, pres, pxqA, gain, subfr)
		synthesizeLPCOrder16CoreRef(sLPCb, coefs, pres, pxqB, gain, subfr)

		for k := range sLPCa {
			if sLPCa[k] != sLPCb[k] {
				t.Fatalf("trial %d subfr %d: sLPC[%d] = %d, want %d", trial, subfr, k, sLPCa[k], sLPCb[k])
			}
		}
		for k := range pxqA {
			if pxqA[k] != pxqB[k] {
				t.Fatalf("trial %d subfr %d: pxq[%d] = %d, want %d", trial, subfr, k, pxqA[k], pxqB[k])
			}
		}
	}
}
