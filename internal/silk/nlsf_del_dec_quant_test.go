package silk

import (
	"math"
	"math/rand"
	"testing"
)

// nlsfDelDecQuantReference is libopus silk/NLSF_del_dec_quant.c
// silk_NLSF_del_dec_quant written statement for statement.
func nlsfDelDecQuantReference(indices []int8, xQ10, wQ5 []int16, predCoefQ8 []uint8, ecIx []int16,
	ecRatesQ5 []uint8, quantStepSizeQ16, invQuantStepSizeQ6 int16, muQ20 int32, order int) int32 {
	const states = nlsfQuantDelDecStates
	var indSort [states]int
	var ind [states][maxLPCOrder]int8
	var prevOutQ10 [2 * states]int16
	var rdQ25 [2 * states]int32
	var rdMinQ25, rdMaxQ25 [states]int32
	var out0Table, out1Table [2 * nlsfQuantMaxAmplitudeExt]int32

	for i := -nlsfQuantMaxAmplitudeExt; i <= nlsfQuantMaxAmplitudeExt-1; i++ {
		out0 := int32(int16(i << 10))
		out1 := int32(int16(out0 + 1024))
		switch {
		case i > 0:
			out0 = int32(int16(out0 - nlsfQuantLevelAdjQ10))
			out1 = int32(int16(out1 - nlsfQuantLevelAdjQ10))
		case i == 0:
			out1 = int32(int16(out1 - nlsfQuantLevelAdjQ10))
		case i == -1:
			out0 = int32(int16(out0 + nlsfQuantLevelAdjQ10))
		default:
			out0 = int32(int16(out0 + nlsfQuantLevelAdjQ10))
			out1 = int32(int16(out1 + nlsfQuantLevelAdjQ10))
		}
		out0Table[i+nlsfQuantMaxAmplitudeExt] = silkRSHIFT(silkSMULBB(out0, int32(quantStepSizeQ16)), 16)
		out1Table[i+nlsfQuantMaxAmplitudeExt] = silkRSHIFT(silkSMULBB(out1, int32(quantStepSizeQ16)), 16)
	}

	nStates := 1
	for i := order - 1; i >= 0; i-- {
		rates := ecRatesQ5[ecIx[i]:]
		inQ10 := int32(xQ10[i])
		for j := range nStates {
			predQ10 := silkRSHIFT(silkSMULBB(int32(int16(predCoefQ8[i])), int32(prevOutQ10[j])), 8)
			resQ10 := int32(int16(inQ10 - predQ10))
			indTmp := int(silkRSHIFT(silkSMULBB(int32(invQuantStepSizeQ6), resQ10), 16))
			indTmp = min(max(indTmp, -nlsfQuantMaxAmplitudeExt), nlsfQuantMaxAmplitudeExt-1)
			ind[j][i] = int8(indTmp)

			out0Q10 := int16(out0Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10)
			out1Q10 := int16(out1Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10)
			prevOutQ10[j] = out0Q10
			prevOutQ10[j+nStates] = out1Q10

			var rate0Q5, rate1Q5 int32
			if indTmp+1 >= nlsfQuantMaxAmplitude {
				if indTmp+1 == nlsfQuantMaxAmplitude {
					rate0Q5 = int32(rates[indTmp+nlsfQuantMaxAmplitude])
					rate1Q5 = 280
				} else {
					rate0Q5 = silkSMLABB(280-43*nlsfQuantMaxAmplitude, 43, int32(indTmp))
					rate1Q5 = rate0Q5 + 43
				}
			} else if indTmp <= -nlsfQuantMaxAmplitude {
				if indTmp == -nlsfQuantMaxAmplitude {
					rate0Q5 = 280
					rate1Q5 = int32(rates[indTmp+1+nlsfQuantMaxAmplitude])
				} else {
					rate0Q5 = silkSMLABB(280-43*nlsfQuantMaxAmplitude, -43, int32(indTmp))
					rate1Q5 = rate0Q5 - 43
				}
			} else {
				rate0Q5 = int32(rates[indTmp+nlsfQuantMaxAmplitude])
				rate1Q5 = int32(rates[indTmp+1+nlsfQuantMaxAmplitude])
			}
			rdTmpQ25 := rdQ25[j]
			diffQ10 := int32(int16(inQ10 - int32(out0Q10)))
			rdQ25[j] = silkSMLABB(rdTmpQ25+silkSMULBB(diffQ10, diffQ10)*int32(wQ5[i]), muQ20, rate0Q5)
			diffQ10 = int32(int16(inQ10 - int32(out1Q10)))
			rdQ25[j+nStates] = silkSMLABB(rdTmpQ25+silkSMULBB(diffQ10, diffQ10)*int32(wQ5[i]), muQ20, rate1Q5)
		}
		if nStates <= states/2 {
			for j := range nStates {
				ind[j+nStates][i] = ind[j][i] + 1
			}
			nStates <<= 1
			for j := nStates; j < states; j++ {
				ind[j][i] = ind[j-nStates][i]
			}
			continue
		}
		for j := range states {
			if rdQ25[j] > rdQ25[j+states] {
				rdMaxQ25[j] = rdQ25[j]
				rdMinQ25[j] = rdQ25[j+states]
				rdQ25[j] = rdMinQ25[j]
				rdQ25[j+states] = rdMaxQ25[j]
				prevOutQ10[j], prevOutQ10[j+states] = prevOutQ10[j+states], prevOutQ10[j]
				indSort[j] = j + states
			} else {
				rdMinQ25[j] = rdQ25[j]
				rdMaxQ25[j] = rdQ25[j+states]
				indSort[j] = j
			}
		}
		for {
			minMaxQ25 := int32(math.MaxInt32)
			maxMinQ25 := int32(0)
			indMinMax, indMaxMin := 0, 0
			for j := range states {
				if minMaxQ25 > rdMaxQ25[j] {
					minMaxQ25 = rdMaxQ25[j]
					indMinMax = j
				}
				if maxMinQ25 < rdMinQ25[j] {
					maxMinQ25 = rdMinQ25[j]
					indMaxMin = j
				}
			}
			if minMaxQ25 >= maxMinQ25 {
				break
			}
			indSort[indMaxMin] = indSort[indMinMax] ^ states
			rdQ25[indMaxMin] = rdQ25[indMinMax+states]
			prevOutQ10[indMaxMin] = prevOutQ10[indMinMax+states]
			rdMinQ25[indMaxMin] = 0
			rdMaxQ25[indMinMax] = math.MaxInt32
			ind[indMaxMin] = ind[indMinMax]
		}
		for j := range states {
			ind[j][i] += int8(indSort[j] >> nlsfQuantDelDecStatesLog2)
		}
	}

	indTmp := 0
	minQ25 := int32(math.MaxInt32)
	for j := range 2 * states {
		if minQ25 > rdQ25[j] {
			minQ25 = rdQ25[j]
			indTmp = j
		}
	}
	for j := range order {
		indices[j] = ind[indTmp&(states-1)][j]
	}
	indices[0] += int8(indTmp >> nlsfQuantDelDecStatesLog2)
	return minQ25
}

func TestSILKNLSFDelDecQuantMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xde1dec))
	for iter := range 4000 {
		cb := &silk_NLSF_CB_WB
		if iter%2 == 1 {
			cb = &silk_NLSF_CB_NB_MB
		}
		order := int(cb.order)
		var xQ10, wQ5, ecIx [maxLPCOrder]int16
		var predQ8 [maxLPCOrder]uint8
		silkNLSFUnpack(ecIx[:order], predQ8[:order], cb, rng.Intn(int(cb.nVectors)))
		for j := range order {
			switch iter % 4 {
			case 0:
				xQ10[j] = int16(rng.Intn(60001) - 30000)
			default:
				xQ10[j] = int16(rng.Intn(4001) - 2000)
			}
			wQ5[j] = int16(rng.Intn(4096))
		}
		muQ20 := int32(rng.Intn(1 << 16))
		if iter%5 == 0 {
			muQ20 = rng.Int31()
		}
		var got, want [maxLPCOrder]int8
		gotRD := silkNLSFDelDecQuant(got[:], xQ10[:], wQ5[:], predQ8[:], ecIx[:], cb.ecRatesQ5, cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20, order)
		wantRD := nlsfDelDecQuantReference(want[:], xQ10[:], wQ5[:], predQ8[:], ecIx[:], cb.ecRatesQ5, cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20, order)
		if gotRD != wantRD || got != want {
			t.Fatalf("iter %d: RD %d indices %v, want RD %d indices %v", iter, gotRD, got[:order], wantRD, want[:order])
		}
	}
}
