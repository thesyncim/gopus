package silk

import (
	"math/rand"
	"testing"
)

// nlsfVQReference is libopus silk/NLSF_VQ.c silk_NLSF_VQ written with its
// running codebook pointers.
func nlsfVQReference(errQ24 []int32, inQ15 []int16, cbQ8 []uint8, cbWghtQ9 []int16, nVectors, order int) {
	cb := cbQ8
	w := cbWghtQ9
	for i := range nVectors {
		var sumErrQ24, predQ24 int32
		for m := order - 2; m >= 0; m -= 2 {
			diffQ15 := int32(inQ15[m+1]) - int32(cb[m+1])<<7
			diffwQ24 := silkSMULBB(diffQ15, int32(w[m+1]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-predQ24>>1))
			predQ24 = diffwQ24

			diffQ15 = int32(inQ15[m]) - int32(cb[m])<<7
			diffwQ24 = silkSMULBB(diffQ15, int32(w[m]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-predQ24>>1))
			predQ24 = diffwQ24
		}
		errQ24[i] = sumErrQ24
		cb = cb[order:]
		w = w[order:]
	}
}

func TestSILKNLSFVQMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9f5c))
	for iter := range 600 {
		cb := &silk_NLSF_CB_WB
		if iter%2 == 1 {
			cb = &silk_NLSF_CB_NB_MB
		}
		order := int(cb.order)
		nVectors := int(cb.nVectors)
		in := make([]int16, order)
		for j := range in {
			if iter%3 == 0 {
				in[j] = int16(rng.Intn(1 << 16))
			} else {
				in[j] = int16(rng.Intn(1 << 15))
			}
		}
		got := make([]int32, nVectors)
		want := make([]int32, nVectors)
		silkNLSFVQ(got, in, cb.cb1NLSFQ8, cb.cb1WghtQ9, nVectors, order)
		nlsfVQReference(want, in, cb.cb1NLSFQ8, cb.cb1WghtQ9, nVectors, order)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d: err[%d]=%d want %d", iter, i, got[i], want[i])
			}
		}
	}
}
