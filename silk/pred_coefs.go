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

	minInvGain32 := float32(minInvGainVal)

	if e.trace != nil && e.trace.NLSF != nil {
		tr := e.trace.NLSF
		tr.LTPResLen = totalLen
		tr.LTPResHash = hashFloat32Slice(ltpRes[:totalLen])
		if tr.CaptureLTPRes {
			tr.LTPRes = append(tr.LTPRes[:0], ltpRes[:totalLen]...)
		}
		tr.MinInvGain = float64(minInvGain32)
		tr.LPCOrder = order
		tr.NbSubfr = numSubframes
		tr.SubfrLen = subframeSamples
		tr.SubfrLenWithOrder = subfrLen
		tr.FirstFrameAfterReset = !e.haveEncoded
	}

	aFull, resNrg := e.burgModifiedFLPZeroAllocF32(ltpRes[:totalLen], minInvGain32, subfrLen, numSubframes, order)
	resNrg32 := float32(resNrg)
	fullTotalEnergy := e.lastTotalEnergy
	fullInvGain := e.lastInvGain
	fullNumSamples := e.lastNumSamples
	x := ensureFloat64Slice(&e.scratchLtpInput, totalLen)
	for i := 0; i < totalLen; i++ {
		x[i] = float64(ltpRes[i])
	}

	for i := 0; i < order; i++ {
		a32 := float32(aFull[i])
		lpcQ12[i] = float64ToInt16Round(float64(a32 * 4096.0))
	}

	lpcQ16 := ensureInt32Slice(&e.scratchLPCQ16, order)
	for i := 0; i < order; i++ {
		a32 := float32(aFull[i])
		lpcQ16[i] = float64ToInt32Round(float64(a32 * 65536.0))
	}
	silkA2NLSF(lsfQ15, lpcQ16, order)

	interpIdx := 4
	useInterp := e.complexity >= 4 && e.haveEncoded && numSubframes == maxNbSubfr
	if useInterp {
		halfOffset := (maxNbSubfr / 2) * subfrLen
		if halfOffset+subfrLen*(maxNbSubfr/2) <= totalLen {
			aLast, resNrgLast := e.burgModifiedFLPZeroAllocF32(ltpRes[halfOffset:], minInvGain32, subfrLen, maxNbSubfr/2, order)
			resNrgLast32 := float32(resNrgLast)
			lsfLast := ensureInt16Slice(&e.scratchNLSFTempQ15, order)
			for i := 0; i < order; i++ {
				a32 := float32(aLast[i])
				lpcQ16[i] = float64ToInt32Round(float64(a32 * 65536.0))
			}
			silkA2NLSF(lsfLast, lpcQ16, order)

			// Restore full-frame energy stats for gain processing.
			e.lastTotalEnergy = fullTotalEnergy
			e.lastInvGain = fullInvGain
			e.lastNumSamples = fullNumSamples

			resNrg32 -= resNrgLast32

			resNrg2nd := float32(math.MaxFloat32)
			analyzeLen := 2 * subfrLen
			if analyzeLen <= totalLen {
				var interpNLSF [maxLPCOrder]int16
				var lpcTmpQ12 [maxLPCOrder]int16
				lpcTmpF64 := ensureFloat64Slice(&e.scratchPredCoefF64A, order)
				lpcRes := ensureFloat64Slice(&e.scratchLpcResF64, analyzeLen)

				for k := 3; k >= 0; k-- {
					interpolateNLSF(interpNLSF[:order], e.prevLSFQ15, lsfLast, k, order)
					// silk_NLSF2A_FLP calls silk_NLSF2A fixed-point
					if !silkNLSF2A(lpcTmpQ12[:order], interpNLSF[:order], order) {
						fallback := lsfToLPCDirect(interpNLSF[:order])
						copy(lpcTmpQ12[:order], fallback[:order])
					}
					for i := 0; i < order; i++ {
						lpcTmpF64[i] = float64(lpcTmpQ12[i]) / 4096.0
					}
					lpcAnalysisFilterFLP(lpcRes, lpcTmpF64, x, analyzeLen, order)

					// Match libopus float32 comparison precision
					resNrgInterp := float32(energyF64(lpcRes[order:], subframeSamples))
					resNrgInterp += float32(energyF64(lpcRes[order+subfrLen:], subframeSamples))

					if resNrgInterp < resNrg32 {
						resNrg32 = resNrgInterp
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

	if e.trace != nil && e.trace.NLSF != nil {
		tr := e.trace.NLSF
		tr.UseInterp = useInterp
		tr.InterpIdx = interpIdx
		tr.RawNLSFQ15 = append(tr.RawNLSFQ15[:0], lsfQ15...)
		tr.PrevNLSFQ15 = append(tr.PrevNLSFQ15[:0], e.prevLSFQ15...)
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

	coefToF32 := func(dst []float32, src []int16) {
		for i := 0; i < order; i++ {
			dst[i] = float32(src[i]) / 4096.0
		}
	}

	coef0 := ensureFloat32Slice(&e.scratchPredCoefF32A, order)
	coef1 := ensureFloat32Slice(&e.scratchPredCoefF32B, order)
	coefToF32(coef1, predCoefQ12[maxLPCOrder:maxLPCOrder+order])
	if interpIdx < 4 {
		coefToF32(coef0, predCoefQ12[:order])
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
		x := ensureFloat32Slice(&e.scratchLtpResF32, length)
		copy(x, ltpRes[:length])
		lpcRes := ensureFloat32Slice(&e.scratchLpcResF32, length)
		lpcAnalysisFilterF32(lpcRes, coeffs, x, length, order)
		for k := 0; k < subframesInFirstHalf; k++ {
			start := order + k*subfrLen
			end := start + subframeSamples
			if end > len(lpcRes) {
				end = len(lpcRes)
			}
			energy := float32(0)
			for i := start; i < end; i++ {
				energy += lpcRes[i] * lpcRes[i]
			}
			if k < len(gains) {
				energy *= gains[k] * gains[k]
			}
			resNrg[k] = float64(energy)
		}
	}

	if numSubframes == 4 {
		offset := 2 * subfrLen
		length := 2 * subfrLen
		if offset+length > len(ltpRes) {
			length = len(ltpRes) - offset
		}
		if length > 0 {
			x := ensureFloat32Slice(&e.scratchLtpResF32, length)
			copy(x, ltpRes[offset:offset+length])
			lpcRes := ensureFloat32Slice(&e.scratchLpcResF32, length)
			lpcAnalysisFilterF32(lpcRes, coef1, x, length, order)
			for k := 0; k < 2; k++ {
				start := order + k*subfrLen
				end := start + subframeSamples
				if end > len(lpcRes) {
					end = len(lpcRes)
				}
				energy := float32(0)
				for i := start; i < end; i++ {
					energy += lpcRes[i] * lpcRes[i]
				}
				idx := k + 2
				if idx < len(gains) {
					energy *= gains[idx] * gains[idx]
				}
				resNrg[idx] = float64(energy)
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

		// Match libopus process_gains_FLP.c sigmoid path for voiced gain reduction.
		s := float32(1.0 - 0.5*(1.0/(1.0+math.Exp(float64(-0.25*(predGainDB-12.0))))))
		for k := range gains {
			gains[k] *= s
		}
	}

	snrDB := float32(snrDBQ7) / 128.0
	invMaxSqrVal := float32(math.Pow(2.0, float64(0.33*(21.0-snrDB)))) / float32(subframeSamples)

	for k := range gains {
		energy := gains[k] * gains[k]
		if k < len(resNrg) {
			energy += float32(resNrg[k]) * invMaxSqrVal
		}
		g := float32(math.Sqrt(float64(energy)))
		if g > 32767.0 {
			g = 32767.0
		}
		gains[k] = g
	}

	return quantOffsetType
}
