package silk

import (
	"math/rand"
	"testing"
)

// downFIRInterpolReference is libopus silk/resampler_private_down_FIR.c
// silk_resampler_private_down_FIR_INTERPOL written as its silk_SMULWB and
// silk_SMLAWB chains in C tap order.
func downFIRInterpolReference(out []int16, buf []int32, firCoefs []int16, firOrder, firFracs int, maxIndexQ16, indexIncrementQ16 int32) int {
	n := 0
	for indexQ16 := int32(0); indexQ16 < maxIndexQ16; indexQ16 += indexIncrementQ16 {
		p := buf[indexQ16>>16:]
		var resQ6 int32
		switch firOrder {
		case resamplerDownOrderFIR0:
			ind := int(silkSMULWB(indexQ16&0xFFFF, int32(firFracs)))
			c := firCoefs[resamplerDownOrderFIR0/2*ind:]
			for k := range resamplerDownOrderFIR0 / 2 {
				resQ6 = silkSMLAWB(resQ6, p[k], int32(c[k]))
			}
			c = firCoefs[resamplerDownOrderFIR0/2*(firFracs-1-ind):]
			for k := range resamplerDownOrderFIR0 / 2 {
				resQ6 = silkSMLAWB(resQ6, p[resamplerDownOrderFIR0-1-k], int32(c[k]))
			}
		default:
			for k := range firOrder / 2 {
				resQ6 = silkSMLAWB(resQ6, p[k]+p[firOrder-1-k], int32(firCoefs[k]))
			}
		}
		out[n] = silkSAT16(silkRSHIFT_ROUND(resQ6, 6))
		n++
	}
	return n
}

func TestDownsamplingFIRInterpolateMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5d0f))
	rates := [][2]int{
		{24000, 16000}, {16000, 12000}, {12000, 8000}, // FIR0, 2/3 and 3/4
		{16000, 8000}, {24000, 12000}, {48000, 24000}, // FIR1, 1/2
		{48000, 16000}, {24000, 8000}, {48000, 12000}, {48000, 8000}, // FIR2
	}
	for _, rt := range rates {
		r := NewDownsamplingResampler(rt[0], rt[1])
		for iter := range 50 {
			nIn := 1 + rng.Intn(480)
			buf := make([]int32, nIn+r.firOrder)
			for i := range buf {
				switch iter % 3 {
				case 0:
					buf[i] = rng.Int31n(1<<24) - 1<<23
				case 1:
					buf[i] = rng.Int31() - 1<<30
				default:
					buf[i] = int32(rng.Uint32())
				}
			}
			maxIndexQ16 := int32(nIn) << 16
			got := make([]int16, nIn)
			want := make([]int16, nIn)
			nGot := r.firInterpolate(got, buf, maxIndexQ16, r.invRatioQ16, 0)
			nWant := downFIRInterpolReference(want, buf, r.firCoefs, r.firOrder, r.firFracs, maxIndexQ16, r.invRatioQ16)
			if nGot != nWant {
				t.Fatalf("%v iter %d: got %d outputs, want %d", rt, iter, nGot, nWant)
			}
			for i := range nWant {
				if got[i] != want[i] {
					t.Fatalf("%v iter %d: out[%d]=%d want %d", rt, iter, i, got[i], want[i])
				}
			}
		}
	}
}
