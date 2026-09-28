package silk

import (
	"math/rand"
	"testing"
)

// lpcInversePredGainReference is silk_LPC_inverse_pred_gain_c written out
// with the generic silk_* helpers, one coefficient update at a time.
func lpcInversePredGainReference(aQ12 []int16) int32 {
	order := len(aQ12)
	var aQA [maxLPCOrder]int32
	var dcResp int32
	for k, v := range aQ12 {
		dcResp += int32(v)
		aQA[k] = silkLSHIFT(int32(v), lpcInvPredGainQA-12)
	}
	if dcResp >= 4096 {
		return 0
	}
	invGainQ30 := int32(1 << 30)
	for k := order - 1; k > 0; k-- {
		if aQA[k] > lpcInvPredGainALimitQ24 || aQA[k] < -lpcInvPredGainALimitQ24 {
			return 0
		}
		rcQ31 := -silkLSHIFT(aQA[k], 31-lpcInvPredGainQA)
		rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
		invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
		if invGainQ30 < maxPredictionPowerGainInvQ30 {
			return 0
		}
		mult2Q := int(32 - silkCLZ32(silkAbs32(rcMult1Q30)))
		rcMult2 := silkInverse32VarQ(rcMult1Q30, mult2Q+30)
		for n := 0; n < (k+1)>>1; n++ {
			tmp1 := aQA[n]
			tmp2 := aQA[k-n-1]
			tmp64 := silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp1, int32(silkRSHIFT_ROUND64(silkSMULL(tmp2, rcQ31), 31))), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[n] = int32(tmp64)
			tmp64 = silkRSHIFT_ROUND64(silkSMULL(silkSubSat32(tmp2, int32(silkRSHIFT_ROUND64(silkSMULL(tmp1, rcQ31), 31))), rcMult2), mult2Q)
			if tmp64 > int64(silkInt32Max) || tmp64 < int64(silkInt32Min) {
				return 0
			}
			aQA[k-n-1] = int32(tmp64)
		}
	}
	if aQA[0] > lpcInvPredGainALimitQ24 || aQA[0] < -lpcInvPredGainALimitQ24 {
		return 0
	}
	rcQ31 := -silkLSHIFT(aQA[0], 31-lpcInvPredGainQA)
	rcMult1Q30 := int32(1<<30) - silkSMMUL(rcQ31, rcQ31)
	invGainQ30 = silkLSHIFT(silkSMMUL(invGainQ30, rcMult1Q30), 2)
	if invGainQ30 < maxPredictionPowerGainInvQ30 {
		return 0
	}
	return invGainQ30
}

// TestLPCInversePredGainMatchesReference checks the paired step-down loop
// against the one-at-a-time reference on stable, marginal and unstable
// filters.
func TestLPCInversePredGainMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1c9))
	nonzero := 0
	for iter := range 200000 {
		order := []int{10, 16}[rng.Intn(2)]
		a := make([]int16, order)
		scale := []int{64, 512, 2048, 8192, 32768}[rng.Intn(5)]
		for i := range a {
			a[i] = int16(rng.Intn(2*scale) - scale)
		}
		if rng.Intn(4) == 0 {
			// Bandwidth-expanded all-pole filters are the typical stable input.
			for i := range a {
				a[i] = int16(int32(a[i]) * int32(order-i) / int32(4*order))
			}
		}
		got := silkLPCInversePredGain(a, order)
		want := lpcInversePredGainReference(a)
		if got != want {
			t.Fatalf("iter %d: silkLPCInversePredGain(%v) = %d, want %d", iter, a, got, want)
		}
		if got != 0 {
			nonzero++
		}
	}
	if nonzero < 1000 {
		t.Fatalf("only %d stable filters exercised", nonzero)
	}
}
