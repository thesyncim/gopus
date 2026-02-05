package silk

// Delayed-decision NSQ ported from libopus silk/NSQ_del_dec.c.

type nsqDelDecState struct {
	sLPCQ14   [maxSubFrameLength + nsqLpcBufLength]int32
	randState [decisionDelay]int32
	qQ10      [decisionDelay]int32
	xqQ14     [decisionDelay]int32
	predQ15   [decisionDelay]int32
	shapeQ14  [decisionDelay]int32
	sAR2Q14   [maxShapeLpcOrder]int32
	lfARQ14   int32
	diffQ14   int32
	seed      int32
	seedInit  int32
	rdQ10     int32
}

type nsqSampleState struct {
	qQ10       int32
	rdQ10      int32
	xqQ14      int32
	lfARQ14    int32
	diffQ14    int32
	sLTPShpQ14 int32
	lpcExcQ14  int32
}

type nsqSamplePair [2]nsqSampleState

var (
	nsqDelDecDebugXQ14        []int32
	nsqDelDecDebugGainQ10     []int32
	nsqDelDecDebugSLTPQ15     []int32
	nsqDelDecDebugSLTP        []int16
	nsqDelDecDebugXScQ10      []int32
	nsqDelDecDebugXScSubfrLen int
	nsqDelDecDebugDelayedGain []int32
	nsqDelDecDebugScaleSubfr  int
	nsqDelDecDebugScaleIdx    int
	nsqDelDecDebugScaleInv    int32
	nsqDelDecDebugScaleSLTP   int16
	nsqDelDecDebugScaleOut    int32
	nsqDelDecDebugScaleGain   int32
	nsqDelDecDebugScaleHit    bool
)

func setNSQDelDecDebug(xqQ14, gainQ10 []int32) {
	nsqDelDecDebugXQ14 = xqQ14
	nsqDelDecDebugGainQ10 = gainQ10
}

func setNSQDelDecDebugSLTP(sltpQ15 []int32) {
	nsqDelDecDebugSLTPQ15 = sltpQ15
}

func setNSQDelDecDebugSLTPRaw(sltp []int16) {
	nsqDelDecDebugSLTP = sltp
}

func setNSQDelDecDebugXSc(xsc []int32, subfrLen int) {
	nsqDelDecDebugXScQ10 = xsc
	nsqDelDecDebugXScSubfrLen = subfrLen
}

func setNSQDelDecDebugDelayedGain(gainQ10 []int32) {
	nsqDelDecDebugDelayedGain = gainQ10
}

func setNSQDelDecDebugScale(subfr, idx int) {
	nsqDelDecDebugScaleSubfr = subfr
	nsqDelDecDebugScaleIdx = idx
	nsqDelDecDebugScaleInv = 0
	nsqDelDecDebugScaleSLTP = 0
	nsqDelDecDebugScaleOut = 0
	nsqDelDecDebugScaleGain = 0
	nsqDelDecDebugScaleHit = false
}

// NoiseShapeQuantizeDelDec performs delayed-decision noise shaping quantization.
// Returns pulses, reconstructed samples, and the seed to encode.
func NoiseShapeQuantizeDelDec(nsq *NSQState, input []int16, params *NSQParams) ([]int8, []int16, int) {
	frameLength := params.FrameLength
	subfrLength := params.SubfrLength
	nbSubfr := params.NbSubfr
	ltpMemLength := params.LTPMemLength
	predictLPCOrder := params.PredLPCOrder
	shapingLPCOrder := params.ShapeLPCOrder
	warpingQ16 := params.WarpingQ16
	nStates := params.NStatesDelayedDecision
	if nStates < 1 {
		nStates = 1
	}
	if nStates > maxDelDecStates {
		nStates = maxDelDecStates
	}

	if frameLength <= 0 {
		return nil, nil, params.Seed
	}

	pulses := nsq.scratchPulses
	if len(pulses) < frameLength {
		pulses = make([]int8, frameLength)
		nsq.scratchPulses = pulses
	}
	pulses = pulses[:frameLength]
	for i := range pulses {
		pulses[i] = 0
	}

	xq := nsq.scratchXq
	if len(xq) < frameLength {
		xq = make([]int16, frameLength)
		nsq.scratchXq = xq
	}
	xq = xq[:frameLength]
	for i := range xq {
		xq[i] = 0
	}

	var sLTPQ15 []int32
	if nsq.scratchSLTPQ15 != nil && len(nsq.scratchSLTPQ15) >= ltpMemLength+frameLength {
		sLTPQ15 = nsq.scratchSLTPQ15[:ltpMemLength+frameLength]
		for i := range sLTPQ15 {
			sLTPQ15[i] = 0
		}
	} else {
		sLTPQ15 = make([]int32, ltpMemLength+frameLength)
	}

	var sLTP []int16
	if nsq.scratchSLTP != nil && len(nsq.scratchSLTP) >= ltpMemLength+frameLength {
		sLTP = nsq.scratchSLTP[:ltpMemLength+frameLength]
		for i := range sLTP {
			sLTP[i] = 0
		}
	} else {
		sLTP = make([]int16, ltpMemLength+frameLength)
	}

	var xScQ10 []int32
	if nsq.scratchXScQ10 != nil && len(nsq.scratchXScQ10) >= subfrLength {
		xScQ10 = nsq.scratchXScQ10[:subfrLength]
		for i := range xScQ10 {
			xScQ10[i] = 0
		}
	} else {
		xScQ10 = make([]int32, subfrLength)
	}

	// LSF interpolation flag
	lsfInterpFlag := 1
	if params.NLSFInterpCoefQ2 == 4 {
		lsfInterpFlag = 0
	}

	// Initialize delayed decision states
	psDelDec := nsq.delDecStates[:nStates]
	for k := 0; k < nStates; k++ {
		psDelDec[k] = nsqDelDecState{}
		psDD := &psDelDec[k]
		psDD.seed = int32((k + params.Seed) & 3)
		psDD.seedInit = psDD.seed
		psDD.rdQ10 = 0
		psDD.lfARQ14 = nsq.sLFARShpQ14
		psDD.diffQ14 = nsq.sDiffShpQ14
		if ltpMemLength-1 >= 0 && ltpMemLength-1 < len(nsq.sLTPShpQ14) {
			psDD.shapeQ14[0] = nsq.sLTPShpQ14[ltpMemLength-1]
		}
		copy(psDD.sLPCQ14[:nsqLpcBufLength], nsq.sLPCQ14[:])
		copy(psDD.sAR2Q14[:], nsq.sAR2Q14[:])
	}

	lag := nsq.lagPrev
	decDelay := decisionDelay
	if decDelay > subfrLength {
		decDelay = subfrLength
	}
	if params.SignalType == typeVoiced {
		for k := 0; k < nbSubfr && k < len(params.PitchL); k++ {
			tmp := params.PitchL[k] - ltpOrderConst/2 - 1
			if tmp < decDelay {
				decDelay = tmp
			}
		}
	} else if lag > 0 {
		tmp := lag - ltpOrderConst/2 - 1
		if tmp < decDelay {
			decDelay = tmp
		}
	}
	if decDelay < 0 {
		decDelay = 0
	}
	if decDelay < 1 {
		decDelay = 1
	}

	var delayedGainQ10 [decisionDelay]int32
	smplBufIdx := 0

	pxq := nsq.xq[ltpMemLength : ltpMemLength+frameLength]
	nsq.sLTPShpBufIdx = ltpMemLength
	nsq.sLTPBufIdx = ltpMemLength

	subfr := 0
	inputOffset := 0
	frameOffset := 0

	for k := 0; k < nbSubfr; k++ {
		A_Q12 := params.PredCoefQ12[((k>>1)|(1-lsfInterpFlag))*maxLPCOrder:]
		B_Q14 := params.LTPCoefQ14[k*ltpOrderConst:]
		AR_shp_Q13 := params.ARShpQ13[k*maxShapeLpcOrder:]

		harmShapeFIRPackedQ14 := int32(silk_RSHIFT(int32(params.HarmShapeGainQ14[k]), 2))
		harmShapeFIRPackedQ14 |= silk_LSHIFT32(int32(silk_RSHIFT(int32(params.HarmShapeGainQ14[k]), 1)), 16)

		nsq.rewhiteFlag = 0
		if params.SignalType == typeVoiced {
			lag = params.PitchL[k]
			if (k & (3 - (lsfInterpFlag << 1))) == 0 {
				if k == 2 {
					// RESET DELAYED DECISIONS
					rdMin := psDelDec[0].rdQ10
					winner := 0
					for i := 1; i < nStates; i++ {
						if psDelDec[i].rdQ10 < rdMin {
							rdMin = psDelDec[i].rdQ10
							winner = i
						}
					}
					for i := 0; i < nStates; i++ {
						if i != winner {
							psDelDec[i].rdQ10 += (silk_int32_MAX >> 4)
						}
					}

					psDD := &psDelDec[winner]
					lastSmplIdx := smplBufIdx + decDelay
					for i := 0; i < decDelay; i++ {
						lastSmplIdx = (lastSmplIdx - 1) % decisionDelay
						if lastSmplIdx < 0 {
							lastSmplIdx += decisionDelay
						}
						outIdx := frameOffset - decDelay + i
						if outIdx >= 0 && outIdx < len(pulses) {
							pulses[outIdx] = int8(silk_RSHIFT_ROUND(psDD.qQ10[lastSmplIdx], 10))
							gainIdx := 1
							if gainIdx >= len(params.GainsQ16) {
								gainIdx = len(params.GainsQ16) - 1
							}
							pxq[outIdx] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(psDD.xqQ14[lastSmplIdx], params.GainsQ16[gainIdx]), 14)))
						}
						if nsq.sLTPShpBufIdx-decDelay+i >= 0 && nsq.sLTPShpBufIdx-decDelay+i < len(nsq.sLTPShpQ14) {
							nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-decDelay+i] = psDD.shapeQ14[lastSmplIdx]
						}
					}
					subfr = 0
				}

				startIdx := ltpMemLength - lag - predictLPCOrder - ltpOrderConst/2
				if startIdx > 0 {
					rewhitenLTP(sLTP, nsq.xq[:], startIdx, k*subfrLength, A_Q12, ltpMemLength-startIdx, predictLPCOrder)
					nsq.sLTPBufIdx = ltpMemLength
					nsq.rewhiteFlag = 1
				}
			}
		}

		nsqDelDecScaleStates(nsq, psDelDec, input[inputOffset:inputOffset+subfrLength], xScQ10, sLTP, sLTPQ15, k, nStates, params.LTPScaleQ14, params.GainsQ16, params.PitchL, params.SignalType, decDelay, params.LTPMemLength)

		noiseShapeQuantizerDelDec(nsq, psDelDec, params.SignalType, xScQ10, pulses, pxq, sLTPQ15, delayedGainQ10[:], A_Q12, B_Q14, AR_shp_Q13,
			lag, harmShapeFIRPackedQ14, params.TiltQ14[k], params.LFShpQ14[k], params.GainsQ16[k], params.LambdaQ10, getQuantizationOffset(params.SignalType, params.QuantOffsetType),
			subfrLength, subfr, shapingLPCOrder, predictLPCOrder, warpingQ16, nStates, &smplBufIdx, decDelay, frameOffset)

		inputOffset += subfrLength
		frameOffset += subfrLength
		subfr++
	}

	// Find winner
	rdMin := psDelDec[0].rdQ10
	winner := 0
	for k := 1; k < nStates; k++ {
		if psDelDec[k].rdQ10 < rdMin {
			rdMin = psDelDec[k].rdQ10
			winner = k
		}
	}

	psDD := &psDelDec[winner]
	seedOut := int(psDD.seedInit)
	lastSmplIdx := smplBufIdx + decDelay
	gainQ10 := int32(params.GainsQ16[nbSubfr-1] >> 6)
	for i := 0; i < decDelay; i++ {
		lastSmplIdx = (lastSmplIdx - 1) % decisionDelay
		if lastSmplIdx < 0 {
			lastSmplIdx += decisionDelay
		}
		outIdx := frameLength - decDelay + i
		if outIdx >= 0 && outIdx < len(pulses) {
			pulses[outIdx] = int8(silk_RSHIFT_ROUND(psDD.qQ10[lastSmplIdx], 10))
			pxq[outIdx] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(psDD.xqQ14[lastSmplIdx], gainQ10), 8)))
		}
		if nsq.sLTPShpBufIdx-decDelay+i >= 0 && nsq.sLTPShpBufIdx-decDelay+i < len(nsq.sLTPShpQ14) {
			nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-decDelay+i] = psDD.shapeQ14[lastSmplIdx]
		}
	}
	copy(nsq.sLPCQ14[:nsqLpcBufLength], psDD.sLPCQ14[subfrLength:subfrLength+nsqLpcBufLength])
	copy(nsq.sAR2Q14[:], psDD.sAR2Q14[:])

	nsq.sLFARShpQ14 = psDD.lfARQ14
	nsq.sDiffShpQ14 = psDD.diffQ14
	if nbSubfr > 0 && nbSubfr-1 < len(params.PitchL) {
		nsq.lagPrev = params.PitchL[nbSubfr-1]
	}

	// Output buffer points into NSQ state (no extra allocation).
	outXQ := nsq.xq[ltpMemLength : ltpMemLength+frameLength]

	// Shift buffers for next frame
	copy(nsq.xq[:ltpMemLength], nsq.xq[frameLength:frameLength+ltpMemLength])
	copy(nsq.sLTPShpQ14[:ltpMemLength], nsq.sLTPShpQ14[frameLength:frameLength+ltpMemLength])

	if nsqDelDecDebugSLTPQ15 != nil {
		copy(nsqDelDecDebugSLTPQ15, sLTPQ15)
	}
	if nsqDelDecDebugSLTP != nil {
		copy(nsqDelDecDebugSLTP, sLTP)
	}
	if nsqDelDecDebugDelayedGain != nil {
		copy(nsqDelDecDebugDelayedGain, delayedGainQ10[:])
	}

	return pulses, outXQ, seedOut
}

func nsqDelDecScaleStates(
	nsq *NSQState,
	psDelDec []nsqDelDecState,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	nStatesDelayedDecision int,
	ltpScaleQ14 int,
	gainsQ16 []int32,
	pitchL []int,
	signalType int,
	decisionDelayActive int,
	ltpMemLength int,
) {
	lag := pitchL[subfr]
	invGainQ31 := silk_INVERSE32_varQ(silk_max(gainsQ16[subfr], 1), 47)
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	for i := 0; i < len(xScQ10) && i < len(x16); i++ {
		xScQ10[i] = int32((int64(x16[i]) * int64(invGainQ26)) >> 16)
	}
	if nsqDelDecDebugXScQ10 != nil && nsqDelDecDebugXScSubfrLen > 0 {
		start := subfr * nsqDelDecDebugXScSubfrLen
		end := start + nsqDelDecDebugXScSubfrLen
		if start >= 0 && start < len(nsqDelDecDebugXScQ10) {
			if end > len(nsqDelDecDebugXScQ10) {
				end = len(nsqDelDecDebugXScQ10)
			}
			n := end - start
			if n > len(xScQ10) {
				n = len(xScQ10)
			}
			if n > 0 {
				copy(nsqDelDecDebugXScQ10[start:start+n], xScQ10[:n])
			}
		}
	}

	if nsq.rewhiteFlag != 0 {
		if subfr == 0 {
			invGainQ31 = silk_LSHIFT32(silk_SMULWB(invGainQ31, int32(ltpScaleQ14)), 2)
		}
		start := nsq.sLTPBufIdx - lag - ltpOrderConst/2
		if start < 0 {
			start = 0
		}
		for i := start; i < nsq.sLTPBufIdx && i < len(sLTPQ15) && i < len(sLTP); i++ {
			sLTPQ15[i] = silk_SMULWB(invGainQ31, int32(sLTP[i]))
			if nsqDelDecDebugScaleSubfr == subfr && nsqDelDecDebugScaleIdx == i {
				nsqDelDecDebugScaleInv = invGainQ31
				nsqDelDecDebugScaleSLTP = sLTP[i]
				nsqDelDecDebugScaleOut = sLTPQ15[i]
				if subfr >= 0 && subfr < len(gainsQ16) {
					nsqDelDecDebugScaleGain = gainsQ16[subfr]
				}
				nsqDelDecDebugScaleHit = true
			}
		}
	}

	if gainsQ16[subfr] != nsq.prevGainQ16 {
		gainAdjQ16 := silk_DIV32_varQ(nsq.prevGainQ16, gainsQ16[subfr], 16)

		start := nsq.sLTPShpBufIdx - ltpMemLength
		if start < 0 {
			start = 0
		}
		for i := start; i < nsq.sLTPShpBufIdx && i < len(nsq.sLTPShpQ14); i++ {
			nsq.sLTPShpQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLTPShpQ14[i])
		}

		if signalType == typeVoiced && nsq.rewhiteFlag == 0 {
			start := nsq.sLTPBufIdx - lag - ltpOrderConst/2
			if start < 0 {
				start = 0
			}
			for i := start; i < nsq.sLTPBufIdx-decisionDelayActive && i < len(sLTPQ15); i++ {
				sLTPQ15[i] = silk_SMULWW(gainAdjQ16, sLTPQ15[i])
			}
		}

		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD := &psDelDec[k]
			psDD.lfARQ14 = silk_SMULWW(gainAdjQ16, psDD.lfARQ14)
			psDD.diffQ14 = silk_SMULWW(gainAdjQ16, psDD.diffQ14)
			for i := 0; i < nsqLpcBufLength; i++ {
				psDD.sLPCQ14[i] = silk_SMULWW(gainAdjQ16, psDD.sLPCQ14[i])
			}
			for i := 0; i < maxShapeLpcOrder; i++ {
				psDD.sAR2Q14[i] = silk_SMULWW(gainAdjQ16, psDD.sAR2Q14[i])
			}
			for i := 0; i < decisionDelay; i++ {
				psDD.predQ15[i] = silk_SMULWW(gainAdjQ16, psDD.predQ15[i])
				psDD.shapeQ14[i] = silk_SMULWW(gainAdjQ16, psDD.shapeQ14[i])
			}
		}

		nsq.prevGainQ16 = gainsQ16[subfr]
	}
}

func noiseShapeQuantizerDelDec(
	nsq *NSQState,
	psDelDec []nsqDelDecState,
	signalType int,
	xQ10 []int32,
	pulses []int8,
	xq []int16,
	sLTPQ15 []int32,
	delayedGainQ10 []int32,
	aQ12 []int16,
	bQ14 []int16,
	arShpQ13 []int16,
	lag int,
	harmShapeFIRPackedQ14 int32,
	tiltQ14 int,
	lfShpQ14 int32,
	gainQ16 int32,
	lambdaQ10 int,
	offsetQ10 int,
	length int,
	subfr int,
	shapingLPCOrder int,
	predictLPCOrder int,
	warpingQ16 int,
	nStatesDelayedDecision int,
	smplBufIdx *int,
	decisionDelayActive int,
	frameOffset int,
) {
	var psSampleState [maxDelDecStates]nsqSamplePair

	shpLagPtrIdx := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtrIdx := nsq.sLTPBufIdx - lag + ltpOrderConst/2
	gainQ10 := int32(gainQ16 >> 6)

	for i := 0; i < length; i++ {
		var ltpPredQ14 int32
		if signalType == typeVoiced {
			ltpPredQ14 = 2
			for tap := 0; tap < ltpOrderConst; tap++ {
				idx := predLagPtrIdx - tap
				if idx >= 0 && idx < len(sLTPQ15) {
					ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[idx], int32(bQ14[tap]))
				}
			}
			ltpPredQ14 = silk_LSHIFT32(ltpPredQ14, 1)
			predLagPtrIdx++
		}

		var nLTPQ14 int32
		if lag > 0 {
			shp0, shp1, shp2 := int32(0), int32(0), int32(0)
			if shpLagPtrIdx >= 0 && shpLagPtrIdx < len(nsq.sLTPShpQ14) {
				shp0 = nsq.sLTPShpQ14[shpLagPtrIdx]
			}
			if shpLagPtrIdx-1 >= 0 && shpLagPtrIdx-1 < len(nsq.sLTPShpQ14) {
				shp1 = nsq.sLTPShpQ14[shpLagPtrIdx-1]
			}
			if shpLagPtrIdx-2 >= 0 && shpLagPtrIdx-2 < len(nsq.sLTPShpQ14) {
				shp2 = nsq.sLTPShpQ14[shpLagPtrIdx-2]
			}
			nLTPQ14 = silk_SMULWB(silk_ADD_SAT32(shp0, shp2), harmShapeFIRPackedQ14)
			nLTPQ14 = silk_SMLAWT(nLTPQ14, shp1, harmShapeFIRPackedQ14)
			nLTPQ14 = silk_SUB_LSHIFT32(ltpPredQ14, nLTPQ14, 2)
			shpLagPtrIdx++
		}

		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD := &psDelDec[k]
			psSS := &psSampleState[k]

			psDD.seed = silk_RAND(psDD.seed)

			psLPCIdx := nsqLpcBufLength - 1 + i
			lpcPredQ14 := shortTermPrediction(psDD.sLPCQ14[:], psLPCIdx, aQ12, predictLPCOrder)
			lpcPredQ14 = silk_LSHIFT32(lpcPredQ14, 4)

			tmp2 := silk_SMLAWB(psDD.diffQ14, psDD.sAR2Q14[0], int32(warpingQ16))
			tmp1 := silk_SMLAWB(psDD.sAR2Q14[0], silk_SUB32_ovflw(psDD.sAR2Q14[1], tmp2), int32(warpingQ16))
			psDD.sAR2Q14[0] = tmp2
			nARQ14 := int32(shapingLPCOrder >> 1)
			nARQ14 = silk_SMLAWB(nARQ14, tmp2, int32(arShpQ13[0]))
			for j := 2; j < shapingLPCOrder; j += 2 {
				tmp2 = silk_SMLAWB(psDD.sAR2Q14[j-1], silk_SUB32_ovflw(psDD.sAR2Q14[j+0], tmp1), int32(warpingQ16))
				psDD.sAR2Q14[j-1] = tmp1
				nARQ14 = silk_SMLAWB(nARQ14, tmp1, int32(arShpQ13[j-1]))
				tmp1 = silk_SMLAWB(psDD.sAR2Q14[j+0], silk_SUB32_ovflw(psDD.sAR2Q14[j+1], tmp2), int32(warpingQ16))
				psDD.sAR2Q14[j+0] = tmp2
				nARQ14 = silk_SMLAWB(nARQ14, tmp2, int32(arShpQ13[j]))
			}
			psDD.sAR2Q14[shapingLPCOrder-1] = tmp1
			nARQ14 = silk_SMLAWB(nARQ14, tmp1, int32(arShpQ13[shapingLPCOrder-1]))
			nARQ14 = silk_LSHIFT32(nARQ14, 1)
			nARQ14 = silk_SMLAWB(nARQ14, psDD.lfARQ14, int32(tiltQ14))
			nARQ14 = silk_LSHIFT32(nARQ14, 2)

			nLFQ14 := silk_SMULWB(psDD.shapeQ14[*smplBufIdx], lfShpQ14)
			nLFQ14 = silk_SMLAWT(nLFQ14, psDD.lfARQ14, lfShpQ14)
			nLFQ14 = silk_LSHIFT32(nLFQ14, 2)

			tmpA := silk_ADD_SAT32(nARQ14, nLFQ14)
			tmpB := silk_ADD32_ovflw(nLTPQ14, lpcPredQ14)
			tmpA = silk_SUB_SAT32(tmpB, tmpA)
			tmpA = silk_RSHIFT_ROUND(tmpA, 4)
			rQ10 := silk_SUB32(xQ10[i], tmpA)
			if psDD.seed < 0 {
				rQ10 = -rQ10
			}
			rQ10 = silk_LIMIT_32(rQ10, -(31 << 10), 30<<10)

			q1Q10 := silk_SUB32(rQ10, int32(offsetQ10))
			q1Q0 := silk_RSHIFT(q1Q10, 10)
			if lambdaQ10 > 2048 {
				rdoOffset := lambdaQ10/2 - 512
				if q1Q10 > int32(rdoOffset) {
					q1Q0 = silk_RSHIFT(q1Q10-int32(rdoOffset), 10)
				} else if q1Q10 < -int32(rdoOffset) {
					q1Q0 = silk_RSHIFT(q1Q10+int32(rdoOffset), 10)
				} else if q1Q10 < 0 {
					q1Q0 = -1
				} else {
					q1Q0 = 0
				}
			}
			var q2Q10, rd1Q10, rd2Q10 int32
			if q1Q0 > 0 {
				q1Q10 = silk_SUB32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
				q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
				q2Q10 = silk_ADD32(q1Q10, 1024)
				rd1Q10 = silk_SMULBB(q1Q10, int32(lambdaQ10))
				rd2Q10 = silk_SMULBB(q2Q10, int32(lambdaQ10))
			} else if q1Q0 == 0 {
				q1Q10 = int32(offsetQ10)
				q2Q10 = silk_ADD32(q1Q10, 1024-quantLevelAdjQ10)
				rd1Q10 = silk_SMULBB(q1Q10, int32(lambdaQ10))
				rd2Q10 = silk_SMULBB(q2Q10, int32(lambdaQ10))
			} else if q1Q0 == -1 {
				q2Q10 = int32(offsetQ10)
				q1Q10 = silk_SUB32(q2Q10, 1024-quantLevelAdjQ10)
				rd1Q10 = silk_SMULBB(-q1Q10, int32(lambdaQ10))
				rd2Q10 = silk_SMULBB(q2Q10, int32(lambdaQ10))
			} else {
				q1Q10 = silk_ADD32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
				q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
				q2Q10 = silk_ADD32(q1Q10, 1024)
				rd1Q10 = silk_SMULBB(-q1Q10, int32(lambdaQ10))
				rd2Q10 = silk_SMULBB(-q2Q10, int32(lambdaQ10))
			}
			rrQ10 := silk_SUB32(rQ10, q1Q10)
			rd1Q10 = silk_RSHIFT(silk_SMLABB(rd1Q10, rrQ10, rrQ10), 10)
			rrQ10 = silk_SUB32(rQ10, q2Q10)
			rd2Q10 = silk_RSHIFT(silk_SMLABB(rd2Q10, rrQ10, rrQ10), 10)

			if rd1Q10 < rd2Q10 {
				psSS[0].rdQ10 = silk_ADD32(psDD.rdQ10, rd1Q10)
				psSS[1].rdQ10 = silk_ADD32(psDD.rdQ10, rd2Q10)
				psSS[0].qQ10 = q1Q10
				psSS[1].qQ10 = q2Q10
			} else {
				psSS[0].rdQ10 = silk_ADD32(psDD.rdQ10, rd2Q10)
				psSS[1].rdQ10 = silk_ADD32(psDD.rdQ10, rd1Q10)
				psSS[0].qQ10 = q2Q10
				psSS[1].qQ10 = q1Q10
			}

			excQ14 := silk_LSHIFT32(psSS[0].qQ10, 4)
			if psDD.seed < 0 {
				excQ14 = -excQ14
			}
			lpcExcQ14 := silk_ADD32(excQ14, ltpPredQ14)
			xqQ14 := silk_ADD32_ovflw(lpcExcQ14, lpcPredQ14)
			psSS[0].diffQ14 = silk_SUB32_ovflw(xqQ14, silk_LSHIFT32(xQ10[i], 4))
			sLFAR := silk_SUB32_ovflw(psSS[0].diffQ14, nARQ14)
			psSS[0].sLTPShpQ14 = silk_SUB_SAT32(sLFAR, nLFQ14)
			psSS[0].lfARQ14 = sLFAR
			psSS[0].lpcExcQ14 = lpcExcQ14
			psSS[0].xqQ14 = xqQ14

			excQ14 = silk_LSHIFT32(psSS[1].qQ10, 4)
			if psDD.seed < 0 {
				excQ14 = -excQ14
			}
			lpcExcQ14 = silk_ADD32(excQ14, ltpPredQ14)
			xqQ14 = silk_ADD32_ovflw(lpcExcQ14, lpcPredQ14)
			psSS[1].diffQ14 = silk_SUB32_ovflw(xqQ14, silk_LSHIFT32(xQ10[i], 4))
			sLFAR = silk_SUB32_ovflw(psSS[1].diffQ14, nARQ14)
			psSS[1].sLTPShpQ14 = silk_SUB_SAT32(sLFAR, nLFQ14)
			psSS[1].lfARQ14 = sLFAR
			psSS[1].lpcExcQ14 = lpcExcQ14
			psSS[1].xqQ14 = xqQ14
		}

		*smplBufIdx = (*smplBufIdx - 1) % decisionDelay
		if *smplBufIdx < 0 {
			*smplBufIdx += decisionDelay
		}
		lastSmplIdx := (*smplBufIdx + decisionDelayActive) % decisionDelay

		rdMin := psSampleState[0][0].rdQ10
		winner := 0
		for k := 1; k < nStatesDelayedDecision; k++ {
			if psSampleState[k][0].rdQ10 < rdMin {
				rdMin = psSampleState[k][0].rdQ10
				winner = k
			}
		}

		winnerRand := psDelDec[winner].randState[lastSmplIdx]
		for k := 0; k < nStatesDelayedDecision; k++ {
			if psDelDec[k].randState[lastSmplIdx] != winnerRand {
				psSampleState[k][0].rdQ10 = silk_ADD32(psSampleState[k][0].rdQ10, silk_int32_MAX>>4)
				psSampleState[k][1].rdQ10 = silk_ADD32(psSampleState[k][1].rdQ10, silk_int32_MAX>>4)
			}
		}

		rdMax := psSampleState[0][0].rdQ10
		rdMin2 := psSampleState[0][1].rdQ10
		rdMaxInd := 0
		rdMinInd := 0
		for k := 1; k < nStatesDelayedDecision; k++ {
			if psSampleState[k][0].rdQ10 > rdMax {
				rdMax = psSampleState[k][0].rdQ10
				rdMaxInd = k
			}
			if psSampleState[k][1].rdQ10 < rdMin2 {
				rdMin2 = psSampleState[k][1].rdQ10
				rdMinInd = k
			}
		}

		if rdMin2 < rdMax {
			copyDelDecStateFromOffset(&psDelDec[rdMaxInd], &psDelDec[rdMinInd], i)
			psSampleState[rdMaxInd][0] = psSampleState[rdMinInd][1]
		}

		psDD := &psDelDec[winner]
		if subfr > 0 || i >= decisionDelayActive {
			outIdx := frameOffset + i - decisionDelayActive
			if outIdx >= 0 && outIdx < len(pulses) {
				pulses[outIdx] = int8(silk_RSHIFT_ROUND(psDD.qQ10[lastSmplIdx], 10))
				xq[outIdx] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(psDD.xqQ14[lastSmplIdx], delayedGainQ10[lastSmplIdx]), 8)))
			}
			if nsq.sLTPShpBufIdx-decisionDelayActive >= 0 && nsq.sLTPShpBufIdx-decisionDelayActive < len(nsq.sLTPShpQ14) {
				nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-decisionDelayActive] = psDD.shapeQ14[lastSmplIdx]
			}
			ltpOutIdx := nsq.sLTPBufIdx - decisionDelayActive
			if ltpOutIdx >= 0 && ltpOutIdx < len(sLTPQ15) {
				sLTPQ15[ltpOutIdx] = psDD.predQ15[lastSmplIdx]
			}
		}
		nsq.sLTPShpBufIdx++
		nsq.sLTPBufIdx++

		for k := 0; k < nStatesDelayedDecision; k++ {
			psDD = &psDelDec[k]
			psSS := &psSampleState[k][0]
			psDD.lfARQ14 = psSS.lfARQ14
			psDD.diffQ14 = psSS.diffQ14
			psDD.sLPCQ14[nsqLpcBufLength+i] = psSS.xqQ14
			psDD.xqQ14[*smplBufIdx] = psSS.xqQ14
			psDD.qQ10[*smplBufIdx] = psSS.qQ10
			psDD.predQ15[*smplBufIdx] = silk_LSHIFT32(psSS.lpcExcQ14, 1)
			psDD.shapeQ14[*smplBufIdx] = psSS.sLTPShpQ14
			psDD.seed = silk_ADD32_ovflw(psDD.seed, silk_RSHIFT_ROUND(psSS.qQ10, 10))
			psDD.randState[*smplBufIdx] = psDD.seed
			psDD.rdQ10 = psSS.rdQ10
		}
		delayedGainQ10[*smplBufIdx] = gainQ10
	}

	for k := 0; k < nStatesDelayedDecision; k++ {
		psDD := &psDelDec[k]
		copy(psDD.sLPCQ14[:nsqLpcBufLength], psDD.sLPCQ14[length:length+nsqLpcBufLength])
	}
}

// copyDelDecStateFromOffset copies src into dst starting at an int32 offset,
// matching libopus' partial memcpy in NSQ_del_dec.
func copyDelDecStateFromOffset(dst, src *nsqDelDecState, offset int) {
	if offset <= 0 {
		*dst = *src
		return
	}
	idx := 0
	for i := 0; i < len(dst.sLPCQ14); i++ {
		if idx >= offset {
			dst.sLPCQ14[i] = src.sLPCQ14[i]
		}
		idx++
	}
	for i := 0; i < len(dst.randState); i++ {
		if idx >= offset {
			dst.randState[i] = src.randState[i]
		}
		idx++
	}
	for i := 0; i < len(dst.qQ10); i++ {
		if idx >= offset {
			dst.qQ10[i] = src.qQ10[i]
		}
		idx++
	}
	for i := 0; i < len(dst.xqQ14); i++ {
		if idx >= offset {
			dst.xqQ14[i] = src.xqQ14[i]
		}
		idx++
	}
	for i := 0; i < len(dst.predQ15); i++ {
		if idx >= offset {
			dst.predQ15[i] = src.predQ15[i]
		}
		idx++
	}
	for i := 0; i < len(dst.shapeQ14); i++ {
		if idx >= offset {
			dst.shapeQ14[i] = src.shapeQ14[i]
		}
		idx++
	}
	for i := 0; i < len(dst.sAR2Q14); i++ {
		if idx >= offset {
			dst.sAR2Q14[i] = src.sAR2Q14[i]
		}
		idx++
	}
	if idx >= offset {
		dst.lfARQ14 = src.lfARQ14
	}
	idx++
	if idx >= offset {
		dst.diffQ14 = src.diffQ14
	}
	idx++
	if idx >= offset {
		dst.seed = src.seed
	}
	idx++
	if idx >= offset {
		dst.seedInit = src.seedInit
	}
	idx++
	if idx >= offset {
		dst.rdQ10 = src.rdQ10
	}
}
