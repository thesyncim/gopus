package silk

import (
	"math"
	"math/rand"
	"testing"
)

// schurFLPReference is libopus silk/float/schur_FLP.c silk_schur_FLP with
// its C[k][2] double work array.
func schurFLPReference(refl, autoCorr []float32, order int) float32 {
	var c [maxShapeLpcOrder + 1][2]silkCReal
	for k := 0; k <= order; k++ {
		c[k][0] = silkCReal(autoCorr[k])
		c[k][1] = silkCReal(autoCorr[k])
	}
	for k := range order {
		den := c[0][1]
		if den < silkCReal(float32(1e-9)) {
			den = silkCReal(float32(1e-9))
		}
		rc := -c[k+1][0] / den
		refl[k] = float32(rc)
		for n := range order - k {
			ctmp1 := c[n+k+1][0]
			ctmp2 := c[n][1]
			c[n+k+1][0] = ctmp1 + ctmp2*rc
			c[n][1] = ctmp2 + ctmp1*rc
		}
	}
	return float32(c[0][1])
}

func TestSchurF32MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5c4a))
	autoCorr := make([]float32, maxShapeLpcOrder+1)
	for iter := range 3000 {
		autoCorr[0] = rng.Float32()*1e6 + 1
		for k := 1; k < len(autoCorr); k++ {
			if iter%3 == 0 {
				autoCorr[k] = (rng.Float32() - 0.5) * autoCorr[0]
			} else {
				autoCorr[k] = autoCorr[k-1] * (0.2 + 0.79*rng.Float32())
			}
		}
		order := 1 + rng.Intn(maxShapeLpcOrder)
		got := make([]float32, order)
		want := make([]float32, order)
		gotNrg := schurF32(got, autoCorr, order)
		wantNrg := schurFLPReference(want, autoCorr, order)
		if math.Float32bits(gotNrg) != math.Float32bits(wantNrg) {
			t.Fatalf("iter %d order %d: energy %v want %v", iter, order, gotNrg, wantNrg)
		}
		for k := range want {
			if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
				t.Fatalf("iter %d order %d: refl[%d]=%v want %v", iter, order, k, got[k], want[k])
			}
		}
	}
}

// vqWMatECReference is libopus silk/VQ_WMat_EC.c silk_VQ_WMat_EC_c.
func vqWMatECReference(ind *int8, resNrgQ15, rateDistQ8, gainQ7 *int32, xxQ17, xXQ17 []int32, cbQ7 []int8, cbGainQ7, clQ5 []uint8, subfrLen int, maxGainQ7 int32, l int) {
	var negxXQ24 [5]int32
	for i := range negxXQ24 {
		negxXQ24[i] = -(xXQ17[i] << 7)
	}
	*rateDistQ8 = math.MaxInt32
	*resNrgQ15 = math.MaxInt32
	*ind = 0
	row := cbQ7
	for k := range l {
		gainTmpQ7 := int32(cbGainQ7[k])
		sum1Q15 := int32(math.Floor(1.001*(1<<15) + 0.5))
		penalty := max(gainTmpQ7-maxGainQ7, 0) << 11

		sum2Q24 := negxXQ24[0] + xxQ17[1]*int32(row[1])
		sum2Q24 += xxQ17[2] * int32(row[2])
		sum2Q24 += xxQ17[3] * int32(row[3])
		sum2Q24 += xxQ17[4] * int32(row[4])
		sum2Q24 <<= 1
		sum2Q24 += xxQ17[0] * int32(row[0])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(row[0]))

		sum2Q24 = negxXQ24[1] + xxQ17[7]*int32(row[2])
		sum2Q24 += xxQ17[8] * int32(row[3])
		sum2Q24 += xxQ17[9] * int32(row[4])
		sum2Q24 <<= 1
		sum2Q24 += xxQ17[6] * int32(row[1])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(row[1]))

		sum2Q24 = negxXQ24[2] + xxQ17[13]*int32(row[3])
		sum2Q24 += xxQ17[14] * int32(row[4])
		sum2Q24 <<= 1
		sum2Q24 += xxQ17[12] * int32(row[2])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(row[2]))

		sum2Q24 = negxXQ24[3] + xxQ17[19]*int32(row[4])
		sum2Q24 <<= 1
		sum2Q24 += xxQ17[18] * int32(row[3])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(row[3]))

		sum2Q24 = negxXQ24[4] << 1
		sum2Q24 += xxQ17[24] * int32(row[4])
		sum1Q15 = silkSMLAWB(sum1Q15, sum2Q24, int32(row[4]))

		if sum1Q15 >= 0 {
			bitsResQ8 := silkSMULBB(int32(subfrLen), silkLin2Log(sum1Q15+penalty)-(15<<7))
			bitsTotQ8 := bitsResQ8 + int32(clQ5[k])<<2
			if bitsTotQ8 <= *rateDistQ8 {
				*rateDistQ8 = bitsTotQ8
				*resNrgQ15 = sum1Q15 + penalty
				*ind = int8(k)
				*gainQ7 = gainTmpQ7
			}
		}
		row = row[ltpOrderConst:]
	}
}

func TestSILKVQWMatECMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7a3e))
	var xxQ17 [ltpOrderConst * ltpOrderConst]int32
	var xXQ17 [ltpOrderConst]int32
	for iter := range 6000 {
		scale := int32(1) << (10 + rng.Intn(11))
		for i := range xxQ17 {
			xxQ17[i] = rng.Int31n(2*scale) - scale
		}
		for i := range xXQ17 {
			xXQ17[i] = rng.Int31n(scale) - scale/2
		}
		cb := rng.Intn(3)
		subfrLen := 40 + rng.Intn(81)
		maxGainQ7 := rng.Int31n(256)
		var gotInd, wantInd int8
		var gotRes, wantRes, gotRate, wantRate int32
		gotGain, wantGain := int32(iter), int32(iter)
		silkVQWMatEC(&gotInd, &gotRes, &gotRate, &gotGain, xxQ17[:], xXQ17[:], silk_LTP_vq_ptrs_Q7[cb], silk_LTP_vq_gain_ptrs_Q7[cb], silk_LTP_gain_BITS_Q5_ptrs[cb], subfrLen, maxGainQ7, int(silk_LTP_vq_sizes[cb]))
		vqWMatECReference(&wantInd, &wantRes, &wantRate, &wantGain, xxQ17[:], xXQ17[:], silk_LTP_vq_ptrs_Q7[cb], silk_LTP_vq_gain_ptrs_Q7[cb], silk_LTP_gain_BITS_Q5_ptrs[cb], subfrLen, maxGainQ7, int(silk_LTP_vq_sizes[cb]))
		if gotInd != wantInd || gotRes != wantRes || gotRate != wantRate || gotGain != wantGain {
			t.Fatalf("iter %d: got (%d %d %d %d) want (%d %d %d %d)", iter, gotInd, gotRes, gotRate, gotGain, wantInd, wantRes, wantRate, wantGain)
		}
	}
}
