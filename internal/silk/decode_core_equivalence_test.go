package silk

import (
	"math/rand"
	"testing"
)

// decodeExcitationReference is the excitation loop of libopus
// silk/decode_core.c silk_decode_core written literally.
func decodeExcitationReference(exc []int32, pulses []int16, seed, offsetQ10 int32) {
	for i := range exc {
		seed = silkRand(seed)
		exc[i] = int32(pulses[i]) << 14
		if exc[i] > 0 {
			exc[i] -= quantLevelAdjustQ10 << 4
		} else if exc[i] < 0 {
			exc[i] += quantLevelAdjustQ10 << 4
		}
		exc[i] += offsetQ10 << 4
		if seed < 0 {
			exc[i] = -exc[i]
		}
		seed += int32(pulses[i])
	}
}

func TestDecodeExcitationMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xe4c))
	for iter := 0; iter < 5000; iter++ {
		n := 1 + rng.Intn(maxFrameLength)
		pulses := make([]int16, n)
		for i := range pulses {
			switch rng.Intn(4) {
			case 0:
				pulses[i] = int16(rng.Uint32())
			case 1:
				pulses[i] = 0
			default:
				pulses[i] = int16(rng.Intn(31) - 15)
			}
		}
		seed := int32(rng.Uint32())
		offsetQ10 := int32(silk_Quantization_Offsets_Q10[rng.Intn(2)][rng.Intn(2)])
		got := make([]int32, n)
		want := make([]int32, n)
		silkDecodeExcitation(got, pulses, seed, offsetQ10<<4)
		decodeExcitationReference(want, pulses, seed, offsetQ10)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d: exc[%d] = %d, want %d", iter, i, got[i], want[i])
			}
		}
	}
}

// ltpSynthesisReference is the voiced LTP loop of libopus
// silk/decode_core.c silk_decode_core written literally.
func ltpSynthesisReference(presQ14, exc, sLTPQ15 []int32, bufIdx, lag int, bQ14 []int16) {
	predLag := bufIdx - lag + ltpOrder/2
	for i := range presQ14 {
		ltpPredQ13 := int32(2)
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag+0], int32(bQ14[0]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-1], int32(bQ14[1]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-2], int32(bQ14[2]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-3], int32(bQ14[3]))
		ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTPQ15[predLag-4], int32(bQ14[4]))
		predLag++
		presQ14[i] = silkADD_LSHIFT32(exc[i], ltpPredQ13, 1)
		sLTPQ15[bufIdx] = silkLSHIFT(presQ14[i], 1)
		bufIdx++
	}
}

func TestLTPSynthesisMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x17b))
	for iter := 0; iter < 20000; iter++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		subfr := 5 * fsKHz
		ltpMem := 20 * fsKHz
		// Lags shorter than the subframe read outputs of the same call.
		lag := 2*fsKHz + rng.Intn(16*fsKHz+1)
		bufIdx := ltpMem + subfr*rng.Intn(4)
		wide := rng.Intn(3) == 0
		state := make([]int32, ltpMem+4*subfr)
		for i := range state {
			if wide {
				state[i] = int32(rng.Uint32())
			} else {
				state[i] = int32(rng.Intn(1<<22)) - 1<<21
			}
		}
		exc := make([]int32, subfr)
		for i := range exc {
			if wide {
				exc[i] = int32(rng.Uint32())
			} else {
				exc[i] = int32(rng.Intn(1<<20)) - 1<<19
			}
		}
		var b [ltpOrder]int16
		for i := range b {
			if wide {
				b[i] = int16(rng.Uint32())
			} else {
				b[i] = int16(rng.Intn(1<<14) - 1<<13)
			}
		}
		gotState := append([]int32(nil), state...)
		wantState := append([]int32(nil), state...)
		got := make([]int32, subfr)
		want := make([]int32, subfr)
		silkLTPSynthesis(got, exc, gotState, bufIdx, lag, &b)
		ltpSynthesisReference(want, exc, wantState, bufIdx, lag, b[:])
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d lag %d: presQ14[%d] = %d, want %d", iter, lag, i, got[i], want[i])
			}
		}
		for i := range wantState {
			if gotState[i] != wantState[i] {
				t.Fatalf("iter %d lag %d: sLTP_Q15[%d] = %d, want %d", iter, lag, i, gotState[i], wantState[i])
			}
		}
	}
}

func TestDecodeCoreKernelsDoNotAllocate(t *testing.T) {
	pulses := make([]int16, maxFrameLength)
	exc := make([]int32, maxFrameLength)
	state := make([]int32, 2*maxFrameLength)
	pres := make([]int32, maxSubFrameLength)
	var b [ltpOrder]int16
	if allocs := testing.AllocsPerRun(100, func() {
		silkDecodeExcitation(exc, pulses, 1, 3)
		silkLTPSynthesis(pres, exc, state, maxFrameLength, 100, &b)
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0", allocs)
	}
}

// clz32Reference counts leading zeros by halving, as the portable
// silk_CLZ32 fallback does.
func clz32Reference(x int32) int {
	if x == 0 {
		return 32
	}
	if x < 0 {
		return 0
	}
	n := 0
	ux := uint32(x)
	for ux&0x80000000 == 0 {
		n++
		ux <<= 1
	}
	return n
}

func TestCLZ32MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc12))
	check := func(x int32) {
		if got, want := silk_CLZ32(x), clz32Reference(x); got != want {
			t.Fatalf("silk_CLZ32(%d) = %d, want %d", x, got, want)
		}
	}
	for _, x := range []int32{0, 1, -1, 0x7fffffff, -0x80000000, 0x10000, 0xffff} {
		check(x)
	}
	for range 100000 {
		check(int32(rng.Uint32()) >> uint(rng.Intn(32)))
	}
}

func TestInterleaveStereoFloat32(t *testing.T) {
	left := []float32{1, 2, 3}
	right := []float32{-1, -2, -3, -4}
	dst := make([]float32, 6)
	interleaveStereoFloat32(dst, left, right)
	want := []float32{1, -1, 2, -2, 3, -3}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("dst[%d] = %v, want %v", i, dst[i], want[i])
		}
	}
}
