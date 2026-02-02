package silk

import "math"

// silkNLSFWeightsLaroia computes Laroia NLSF weights (Q2).
// Reference: libopus silk/NLSF_VQ_weights_laroia.c
func silkNLSFWeightsLaroia(wQ2 []int16, nlsfQ15 []int16, order int) {
	if order <= 0 || order&1 != 0 {
		return
	}
	if len(wQ2) < order || len(nlsfQ15) < order {
		return
	}

	tmp1 := silkMaxInt(int(nlsfQ15[0]), 1)
	tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
	tmp2 := silkMaxInt(int(nlsfQ15[1]-nlsfQ15[0]), 1)
	tmp2 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp2)))
	wQ2[0] = int16(silkMinInt(tmp1+tmp2, 32767))

	for k := 1; k < order-1; k += 2 {
		tmp1 = silkMaxInt(int(nlsfQ15[k+1]-nlsfQ15[k]), 1)
		tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
		wQ2[k] = int16(silkMinInt(tmp1+tmp2, 32767))

		tmp2 = silkMaxInt(int(nlsfQ15[k+2]-nlsfQ15[k+1]), 1)
		tmp2 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp2)))
		wQ2[k+1] = int16(silkMinInt(tmp1+tmp2, 32767))
	}

	tmp1 = silkMaxInt(int(int32(1<<15)-int32(nlsfQ15[order-1])), 1)
	tmp1 = int(silkDiv32_16(int32(1<<(15+nlsfWQ)), int32(tmp1)))
	wQ2[order-1] = int16(silkMinInt(tmp1+tmp2, 32767))
}

// silkNLSFVQ computes quantization error for each codebook vector (Q24).
// Reference: libopus silk/NLSF_VQ.c
func silkNLSFVQ(errQ24 []int32, inQ15 []int16, cbQ8 []uint8, cbWghtQ9 []int16, nVectors, order int) {
	if order <= 0 || order&1 != 0 {
		return
	}
	if len(errQ24) < nVectors || len(inQ15) < order {
		return
	}

	cbIdx := 0
	wIdx := 0
	for i := 0; i < nVectors; i++ {
		var sumErrQ24 int32
		var predQ24 int32
		for m := order - 2; m >= 0; m -= 2 {
			diffQ15 := int32(inQ15[m+1]) - (int32(cbQ8[cbIdx+m+1]) << 7)
			diffwQ24 := silkSMULBB(diffQ15, int32(cbWghtQ9[wIdx+m+1]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24

			diffQ15 = int32(inQ15[m]) - (int32(cbQ8[cbIdx+m]) << 7)
			diffwQ24 = silkSMULBB(diffQ15, int32(cbWghtQ9[wIdx+m]))
			sumErrQ24 = silkAddSat32(sumErrQ24, silkAbs32(diffwQ24-(predQ24>>1)))
			predQ24 = diffwQ24
		}
		errQ24[i] = sumErrQ24
		cbIdx += order
		wIdx += order
	}
}

// silkNLSFDelDecQuant performs delayed-decision quantization for NLSF residuals.
// Returns RD value in Q25 and fills indices with residual indices.
// Reference: libopus silk/NLSF_del_dec_quant.c
func silkNLSFDelDecQuant(indices []int8, xQ10 []int16, wQ5 []int16, predQ8 []uint8, ecIx []int16,
	ecRatesQ5 []uint8, quantStepSizeQ16 int, invQuantStepSizeQ6 int, muQ20 int32, order int) int32 {
	if order <= 0 || len(indices) < order {
		return 0
	}

	var ind [nlsfQuantDelDecStates][maxLPCOrder]int8
	var prevOutQ10 [2 * nlsfQuantDelDecStates]int16
	var rdQ25 [2 * nlsfQuantDelDecStates]int32
	var rdMinQ25 [nlsfQuantDelDecStates]int32
	var rdMaxQ25 [nlsfQuantDelDecStates]int32
	var indSort [nlsfQuantDelDecStates]int
	var out0Table [2 * nlsfQuantMaxAmplitudeExt]int32
	var out1Table [2 * nlsfQuantMaxAmplitudeExt]int32

	for i := -nlsfQuantMaxAmplitudeExt; i <= nlsfQuantMaxAmplitudeExt-1; i++ {
		out0Q10 := int32(i) << 10
		out1Q10 := out0Q10 + 1024
		if i > 0 {
			out0Q10 -= nlsfQuantLevelAdjQ10
			out1Q10 -= nlsfQuantLevelAdjQ10
		} else if i == 0 {
			out1Q10 -= nlsfQuantLevelAdjQ10
		} else if i == -1 {
			out0Q10 += nlsfQuantLevelAdjQ10
		} else {
			out0Q10 += nlsfQuantLevelAdjQ10
			out1Q10 += nlsfQuantLevelAdjQ10
		}
		out0Table[i+nlsfQuantMaxAmplitudeExt] = int32(silkRSHIFT(silkSMULBB(out0Q10, int32(quantStepSizeQ16)), 16))
		out1Table[i+nlsfQuantMaxAmplitudeExt] = int32(silkRSHIFT(silkSMULBB(out1Q10, int32(quantStepSizeQ16)), 16))
	}

	nStates := 1
	rdQ25[0] = 0
	prevOutQ10[0] = 0
	for i := order - 1; i >= 0; i-- {
		rates := ecRatesQ5[ecIx[i]:]
		inQ10 := int32(xQ10[i])
		for j := 0; j < nStates; j++ {
			predQ10 := int32(silkRSHIFT(silkSMULBB(int32(predQ8[i]), int32(prevOutQ10[j])), 8))
			resQ10 := inQ10 - predQ10
			indTmp := int(silkRSHIFT(silkSMULBB(int32(invQuantStepSizeQ6), resQ10), 16))
			indTmp = silkLimitInt(indTmp, -nlsfQuantMaxAmplitudeExt, nlsfQuantMaxAmplitudeExt-1)
			ind[j][i] = int8(indTmp)

			out0Q10 := out0Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10
			out1Q10 := out1Table[indTmp+nlsfQuantMaxAmplitudeExt] + predQ10
			prevOutQ10[j] = int16(out0Q10)
			prevOutQ10[j+nStates] = int16(out1Q10)

			var rate0Q5 int32
			var rate1Q5 int32
			if indTmp+1 >= nlsfQuantMaxAmplitude {
				if indTmp+1 == nlsfQuantMaxAmplitude {
					rate0Q5 = int32(rates[indTmp+nlsfQuantMaxAmplitude])
					rate1Q5 = 280
				} else {
					rate0Q5 = 280 - 43*int32(nlsfQuantMaxAmplitude) + 43*int32(indTmp)
					rate1Q5 = rate0Q5 + 43
				}
			} else if indTmp <= -nlsfQuantMaxAmplitude {
				if indTmp == -nlsfQuantMaxAmplitude {
					rate0Q5 = 280
					rate1Q5 = int32(rates[indTmp+1+nlsfQuantMaxAmplitude])
				} else {
					rate0Q5 = 280 - 43*int32(nlsfQuantMaxAmplitude) - 43*int32(indTmp)
					rate1Q5 = rate0Q5 - 43
				}
			} else {
				rate0Q5 = int32(rates[indTmp+nlsfQuantMaxAmplitude])
				rate1Q5 = int32(rates[indTmp+1+nlsfQuantMaxAmplitude])
			}

			rdTmp := rdQ25[j]
			diffQ10 := inQ10 - out0Q10
			rdQ25[j] = silkSMLABB(silkMLA(rdTmp, silkSMULBB(diffQ10, diffQ10), int32(wQ5[i])), muQ20, rate0Q5)

			diffQ10 = inQ10 - out1Q10
			rdQ25[j+nStates] = silkSMLABB(silkMLA(rdTmp, silkSMULBB(diffQ10, diffQ10), int32(wQ5[i])), muQ20, rate1Q5)
		}

		if nStates <= nlsfQuantDelDecStates/2 {
			for j := 0; j < nStates; j++ {
				ind[j+nStates][i] = ind[j][i] + 1
			}
			nStates <<= 1
			for j := nStates; j < nlsfQuantDelDecStates; j++ {
				ind[j][i] = ind[j-nStates][i]
			}
		} else {
			for j := 0; j < nlsfQuantDelDecStates; j++ {
				if rdQ25[j] > rdQ25[j+nlsfQuantDelDecStates] {
					rdMaxQ25[j] = rdQ25[j]
					rdMinQ25[j] = rdQ25[j+nlsfQuantDelDecStates]
					rdQ25[j] = rdMinQ25[j]
					rdQ25[j+nlsfQuantDelDecStates] = rdMaxQ25[j]
					out0 := prevOutQ10[j]
					prevOutQ10[j] = prevOutQ10[j+nlsfQuantDelDecStates]
					prevOutQ10[j+nlsfQuantDelDecStates] = out0
					indSort[j] = j + nlsfQuantDelDecStates
				} else {
					rdMinQ25[j] = rdQ25[j]
					rdMaxQ25[j] = rdQ25[j+nlsfQuantDelDecStates]
					indSort[j] = j
				}
			}
			for {
				minMaxQ25 := int32(math.MaxInt32)
				maxMinQ25 := int32(0)
				indMinMax := 0
				indMaxMin := 0
				for j := 0; j < nlsfQuantDelDecStates; j++ {
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
				indSort[indMaxMin] = indSort[indMinMax] ^ nlsfQuantDelDecStates
				rdQ25[indMaxMin] = rdQ25[indMinMax+nlsfQuantDelDecStates]
				prevOutQ10[indMaxMin] = prevOutQ10[indMinMax+nlsfQuantDelDecStates]
				rdMinQ25[indMaxMin] = 0
				rdMaxQ25[indMinMax] = math.MaxInt32
				copy(ind[indMaxMin][:], ind[indMinMax][:])
			}
			for j := 0; j < nlsfQuantDelDecStates; j++ {
				ind[j][i] += int8(indSort[j] >> nlsfQuantDelDecStatesLog2)
			}
		}
	}

	indTmp := 0
	minQ25 := int32(math.MaxInt32)
	for j := 0; j < 2*nlsfQuantDelDecStates; j++ {
		if minQ25 > rdQ25[j] {
			minQ25 = rdQ25[j]
			indTmp = j
		}
	}

	for j := 0; j < order; j++ {
		indices[j] = ind[indTmp&(nlsfQuantDelDecStates-1)][j]
	}
	indices[0] += int8(indTmp >> nlsfQuantDelDecStatesLog2)
	return minQ25
}

// computeNLSFMuQ20 returns the NLSF_mu parameter (Q20) based on speech activity.
func computeNLSFMuQ20(speechActivityQ8 int, numSubframes int) int32 {
	base := int32(math.Round(0.003 * (1 << 20)))
	slope := int32(math.Round(-0.001 * (1 << 28)))
	mu := base + int32((int64(slope)*int64(speechActivityQ8))>>16)
	if numSubframes == 2 {
		mu += mu >> 1
	}
	if mu < 1 {
		mu = 1
	}
	return mu
}

// nlsfEncode performs MSVQ-based NLSF encoding aligned with libopus.
// Returns stage1 index and residual indices (length = order).
func (e *Encoder) nlsfEncode(nlsfQ15 []int16, cb *nlsfCB, wQ2 []int16, muQ20 int32, nSurvivors int, signalType int) (int, []int) {
	order := cb.order
	if len(nlsfQ15) < order {
		residuals := ensureIntSlice(&e.scratchLsfResiduals, order)
		for i := range residuals {
			residuals[i] = 0
		}
		return 0, residuals
	}

	var errQ24 [32]int32
	silkNLSFVQ(errQ24[:cb.nVectors], nlsfQ15, cb.cb1NLSFQ8, cb.cb1WghtQ9, cb.nVectors, order)

	var indices [32]int
	for i := 0; i < cb.nVectors; i++ {
		indices[i] = i
	}

	// Select nSurvivors smallest errors (partial selection sort).
	if nSurvivors > cb.nVectors {
		nSurvivors = cb.nVectors
	}
	for i := 0; i < nSurvivors; i++ {
		best := i
		for j := i + 1; j < cb.nVectors; j++ {
			if errQ24[indices[j]] < errQ24[indices[best]] {
				best = j
			}
		}
		indices[i], indices[best] = indices[best], indices[i]
	}

	bestStage1 := indices[0]
	bestRD := int32(math.MaxInt32)
	var bestRes [maxLPCOrder]int8

	var resQ10 [maxLPCOrder]int16
	var wAdjQ5 [maxLPCOrder]int16
	var ecIx [maxLPCOrder]int16
	var predQ8 [maxLPCOrder]uint8
	var tmpIndices [maxLPCOrder]int8

	for s := 0; s < nSurvivors; s++ {
		ind1 := indices[s]
		baseIdx := ind1 * order

		for i := 0; i < order; i++ {
			wTmpQ9 := int32(cb.cb1WghtQ9[baseIdx+i])
			diff := int32(nlsfQ15[i]) - (int32(cb.cb1NLSFQ8[baseIdx+i]) << 7)
			resQ10[i] = int16(silkRSHIFT(silkSMULBB(diff, wTmpQ9), 14))
			denom := silkSMULBB(wTmpQ9, wTmpQ9)
			if denom == 0 {
				denom = 1
			}
			wAdjQ5[i] = int16(silk_DIV32_varQ(int32(wQ2[i]), denom, 21))
		}

		silkNLSFUnpack(ecIx[:order], predQ8[:order], cb, ind1)
		rdQ25 := silkNLSFDelDecQuant(tmpIndices[:], resQ10[:], wAdjQ5[:], predQ8[:], ecIx[:], cb.ecRatesQ5, cb.quantStepSizeQ16, cb.invQuantStepSizeQ6, muQ20, order)

		// Add rate for first stage
		icdf := cb.cb1ICDF[(signalType>>1)*cb.nVectors:]
		var probQ8 int32
		if ind1 == 0 {
			probQ8 = 256 - int32(icdf[0])
		} else {
			probQ8 = int32(icdf[ind1-1]) - int32(icdf[ind1])
		}
		bitsQ7 := int32((8 << 7)) - silkLin2Log(probQ8)
		rdQ25 = silkSMLABB(rdQ25, bitsQ7, muQ20>>2)

		if rdQ25 < bestRD {
			bestRD = rdQ25
			bestStage1 = ind1
			copy(bestRes[:order], tmpIndices[:order])
		}
	}

	residuals := ensureIntSlice(&e.scratchLsfResiduals, order)
	for i := 0; i < order; i++ {
		residuals[i] = int(bestRes[i])
	}

	return bestStage1, residuals
}
