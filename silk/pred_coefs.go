package silk

import "math"

func computeMinInvGain(predGainQ7 int32, codingQuality float32, firstFrame bool) float64 {
	if firstFrame {
		return 1.0 / maxPredictionPowerGainAfterReset
	}

	predGain := float64(predGainQ7) / 128.0
	minInvGainVal := math.Pow(2.0, predGain/3.0) / maxPredictionPowerGain
	minInvGainVal /= 0.25 + 0.75*float64(codingQuality)

	if minInvGainVal < 1.0/maxPredictionPowerGain {
		minInvGainVal = 1.0 / maxPredictionPowerGain
	}
	if minInvGainVal > 1.0 {
		minInvGainVal = 1.0
	}
	return minInvGainVal
}

func (e *Encoder) buildLTPResidual(pitchBuf []float32, frameStart int, gains []float32, pitchLags []int, ltpCoeffs LTPCoeffsArray, numSubframes, subframeSamples int, signalType int) []float32 {
	preLen := e.lpcOrder
	outLen := numSubframes * (subframeSamples + preLen)
	ltpRes := ensureFloat32Slice(&e.scratchLtpResF32, outLen)

	for i := range ltpRes {
		ltpRes[i] = 0
	}

	getSample := func(idx int) float32 {
		if idx < 0 || idx >= len(pitchBuf) {
			return 0
		}
		return float32(floatToInt16Round(pitchBuf[idx] * float32(silkSampleScale)))
	}

	for k := 0; k < numSubframes; k++ {
		outBase := k * (subframeSamples + preLen)
		xStart := frameStart - preLen + k*subframeSamples
		invGain := float32(1.0)
		if k < len(gains) && gains[k] > 0 {
			invGain = 1.0 / gains[k]
		}
		if signalType == typeVoiced && k < len(pitchLags) {
			pitchLag := pitchLags[k]
			for i := 0; i < subframeSamples+preLen; i++ {
				xIdx := xStart + i
				x := getSample(xIdx)
				lagBase := xIdx - pitchLag
				var pred float32
				for j := 0; j < ltpOrderConst; j++ {
					lagIdx := lagBase + (ltpOrderConst/2 - j)
					b := float32(ltpCoeffs[k][j]) / 128.0
					pred += b * getSample(lagIdx)
				}
				ltpRes[outBase+i] = (x - pred) * invGain
			}
		} else {
			for i := 0; i < subframeSamples+preLen; i++ {
				xIdx := xStart + i
				ltpRes[outBase+i] = getSample(xIdx) * invGain
			}
		}
	}

	return ltpRes
}

func (e *Encoder) computeLPCFromLTPResidual(ltpRes []float32, numSubframes, subframeSamples int) []int16 {
	order := e.lpcOrder
	if order <= 0 {
		return ensureInt16Slice(&e.scratchLpcQ12, 0)
	}

	subfrLen := subframeSamples + order
	totalLen := numSubframes * subfrLen
	if totalLen <= 0 {
		return ensureInt16Slice(&e.scratchLpcQ12, order)
	}

	x := ensureFloat64Slice(&e.scratchLtpInput, totalLen)
	for i := 0; i < totalLen && i < len(ltpRes); i++ {
		x[i] = float64(ltpRes[i])
	}

	a, _ := e.burgModifiedFLPZeroAlloc(x, minInvGain, subfrLen, numSubframes, order)

	lpcQ12 := ensureInt16Slice(&e.scratchLpcQ12, order)
	for i := 0; i < order; i++ {
		lpcQ12[i] = float64ToInt16Round(a[i] * 4096.0)
	}

	return lpcQ12
}

func (e *Encoder) computeLPCAndNLSFWithInterp(ltpRes []float32, numSubframes, subframeSamples int, minInvGainVal float64) ([]int16, []int16, int) {
	order := e.lpcOrder
	lpcQ12 := ensureInt16Slice(&e.scratchLpcQ12, order)
	lsfQ15 := ensureInt16Slice(&e.scratchLSFQ15, order)

	if order <= 0 {
		return lpcQ12, lsfQ15, 4
	}

	subfrLen := subframeSamples + order
	totalLen := numSubframes * subfrLen
	if totalLen <= 0 || totalLen > len(ltpRes) {
		for i := 0; i < order; i++ {
			lpcQ12[i] = 0
			lsfQ15[i] = int16((i + 1) * 32767 / (order + 1))
		}
		return lpcQ12, lsfQ15, 4
	}

	x := ensureFloat64Slice(&e.scratchLtpInput, totalLen)
	for i := 0; i < totalLen; i++ {
		x[i] = float64(ltpRes[i])
	}

	aFull, resNrg := e.burgModifiedFLPZeroAlloc(x, minInvGainVal, subfrLen, numSubframes, order)
	fullTotalEnergy := e.lastTotalEnergy
	fullInvGain := e.lastInvGain
	fullNumSamples := e.lastNumSamples

	for i := 0; i < order; i++ {
		lpcQ12[i] = float64ToInt16Round(aFull[i] * 4096.0)
	}

	lpcQ16 := ensureInt32Slice(&e.scratchLPCQ16, order)
	for i := 0; i < order; i++ {
		lpcQ16[i] = float64ToInt32Round(aFull[i] * 65536.0)
	}
	silkA2NLSF(lsfQ15, lpcQ16, order)

	interpIdx := 4
	useInterp := e.complexity >= 4 && e.haveEncoded && numSubframes == maxNbSubfr
	if useInterp {
		halfOffset := (maxNbSubfr / 2) * subfrLen
		if halfOffset+subfrLen*(maxNbSubfr/2) <= totalLen {
			aLast, resNrgLast := e.burgModifiedFLPZeroAlloc(x[halfOffset:], minInvGainVal, subfrLen, maxNbSubfr/2, order)
			lsfLast := ensureInt16Slice(&e.scratchNLSFTempQ15, order)
			for i := 0; i < order; i++ {
				lpcQ16[i] = float64ToInt32Round(aLast[i] * 65536.0)
			}
			silkA2NLSF(lsfLast, lpcQ16, order)

			// Restore full-frame energy stats for gain processing.
			e.lastTotalEnergy = fullTotalEnergy
			e.lastInvGain = fullInvGain
			e.lastNumSamples = fullNumSamples

			resNrg -= resNrgLast

			resNrg2nd := math.MaxFloat64
			analyzeLen := 2 * subfrLen
			if analyzeLen <= totalLen {
				var interpNLSF [maxLPCOrder]int16
				var lpcTmpQ12 [maxLPCOrder]int16
				lpcTmpF64 := ensureFloat64Slice(&e.scratchPredCoefF64A, order)
				lpcRes := ensureFloat64Slice(&e.scratchLpcResF64, analyzeLen)

				for k := 3; k >= 0; k-- {
					interpolateNLSF(interpNLSF[:order], e.prevLSFQ15, lsfLast, k, order)
					if !silkNLSF2A(lpcTmpQ12[:order], interpNLSF[:order], order) {
						fallback := lsfToLPCDirect(interpNLSF[:order])
						copy(lpcTmpQ12[:order], fallback[:order])
					}
					for i := 0; i < order; i++ {
						lpcTmpF64[i] = float64(lpcTmpQ12[i]) / 4096.0
					}
					lpcAnalysisFilterFLP(lpcRes, lpcTmpF64, x, analyzeLen, order)

					resNrgInterp := energyF64(lpcRes[order:], subfrLen-order)
					resNrgInterp += energyF64(lpcRes[order+subfrLen:], subfrLen-order)

					if resNrgInterp < resNrg {
						resNrg = resNrgInterp
						interpIdx = k
					} else if resNrgInterp > resNrg2nd {
						break
					}
					resNrg2nd = resNrgInterp
				}
			}

			if interpIdx < 4 {
				copy(lsfQ15, lsfLast)
			}
		}
	}

	return lpcQ12, lsfQ15, interpIdx
}

func (e *Encoder) computeResidualEnergies(ltpRes []float32, predCoefQ12 []int16, interpIdx int, gains []float32, numSubframes, subframeSamples int) []float64 {
	order := e.lpcOrder
	subfrLen := subframeSamples + order
	resNrg := ensureFloat64Slice(&e.scratchResNrg, numSubframes)
	for i := range resNrg {
		resNrg[i] = 0
	}

	if numSubframes == 0 || subfrLen <= order {
		return resNrg
	}

	coefToF64 := func(dst []float64, src []int16) {
		for i := 0; i < order; i++ {
			dst[i] = float64(src[i]) / 4096.0
		}
	}

	coef0 := ensureFloat64Slice(&e.scratchPredCoefF64A, order)
	coef1 := ensureFloat64Slice(&e.scratchPredCoefF64B, order)
	coefToF64(coef1, predCoefQ12[maxLPCOrder:maxLPCOrder+order])
	if interpIdx < 4 {
		coefToF64(coef0, predCoefQ12[:order])
	} else {
		copy(coef0, coef1)
	}

	coeffs := coef0
	if interpIdx >= 4 {
		coeffs = coef1
	}
	subframesInFirstHalf := numSubframes
	if subframesInFirstHalf > 2 {
		subframesInFirstHalf = 2
	}
	length := subframesInFirstHalf * subfrLen
	if length > len(ltpRes) {
		length = len(ltpRes)
	}
	if length > 0 {
		x := ensureFloat64Slice(&e.scratchLtpResF64, length)
		for i := 0; i < length; i++ {
			x[i] = float64(ltpRes[i])
		}
		lpcRes := ensureFloat64Slice(&e.scratchLpcResF64, length)
		lpcAnalysisFilterFLP(lpcRes, coeffs, x, length, order)
		for k := 0; k < subframesInFirstHalf; k++ {
			start := order + k*subframeSamples
			end := start + subframeSamples
			if end > len(lpcRes) {
				end = len(lpcRes)
			}
			var energy float64
			for i := start; i < end; i++ {
				energy += lpcRes[i] * lpcRes[i]
			}
			if k < len(gains) {
				energy *= float64(gains[k] * gains[k])
			}
			resNrg[k] = energy
		}
	}

	if numSubframes == 4 {
		offset := 2 * subfrLen
		length := 2 * subfrLen
		if offset+length > len(ltpRes) {
			length = len(ltpRes) - offset
		}
		if length > 0 {
			x := ensureFloat64Slice(&e.scratchLtpResF64, length)
			for i := 0; i < length; i++ {
				x[i] = float64(ltpRes[offset+i])
			}
			lpcRes := ensureFloat64Slice(&e.scratchLpcResF64, length)
			lpcAnalysisFilterFLP(lpcRes, coef1, x, length, order)
			for k := 0; k < 2; k++ {
				start := order + k*subframeSamples
				end := start + subframeSamples
				if end > len(lpcRes) {
					end = len(lpcRes)
				}
				var energy float64
				for i := start; i < end; i++ {
					energy += lpcRes[i] * lpcRes[i]
				}
				idx := k + 2
				if idx < len(gains) {
					energy *= float64(gains[idx] * gains[idx])
				}
				resNrg[idx] = energy
			}
		}
	}

	return resNrg
}

func applyGainProcessing(gains []float32, resNrg []float64, predGainQ7 int32, snrDBQ7 int, signalType int, inputTiltQ15 int, subframeSamples int) int {
	quantOffsetType := 0
	if signalType == typeVoiced {
		predGainDB := float32(predGainQ7) / 128.0
		inputTilt := float32(inputTiltQ15) / 32768.0
		if predGainDB+inputTilt > 1.0 {
			quantOffsetType = 0
		} else {
			quantOffsetType = 1
		}

		s := float32(1.0 - 0.5*Sigmoid(0.25*(predGainDB-12.0)))
		for k := range gains {
			gains[k] *= s
		}
	}

	snrDB := float64(snrDBQ7) / 128.0
	invMaxSqrVal := math.Pow(2.0, 0.33*(21.0-snrDB)) / float64(subframeSamples)
	if invMaxSqrVal < 0 {
		invMaxSqrVal = 0
	}

	for k := range gains {
		energy := float64(gains[k] * gains[k])
		if k < len(resNrg) {
			energy += resNrg[k] * invMaxSqrVal
		}
		g := math.Sqrt(energy)
		if g > 32767.0 {
			g = 32767.0
		}
		if g < 1.0 {
			g = 1.0
		}
		gains[k] = float32(g)
	}

	return quantOffsetType
}
