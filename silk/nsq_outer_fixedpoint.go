//go:build gopus_fixedpoint

package silk

// silkNSQFixed is the bit-exact Go port of the libopus FIXED_POINT silk_NSQ_c
// outer state machine (silk/NSQ.c). It wraps the per-sample inner noise-shape
// quantizer (silkNoiseShapeQuantizerFixed) with the input/LTP scaling, the
// per-subframe coefficient pointer setup and HarmShapeFIRPacked packing, the
// voiced rewhitening (silk_LPC_analysis_filter), the subframe loop, and the
// end-of-frame xq/sLTP_shp history memmoves.
//
// The encoder-state fields the C function reads from silk_encoder_state are
// passed explicitly: ltpMemLength, frameLength, subfrLength, nbSubfr,
// predictLPCOrder, shapingLPCOrder. The SideInfoIndices fields are seed,
// signalType, quantOffsetType, nlsfInterpCoefQ2.
//
// State read/written on nsq: xq, sLTPShpQ14, sLPCQ14, sAR2Q14, sLFARShpQ14,
// sDiffShpQ14, lagPrev, sLTPBufIdx, sLTPShpBufIdx, randSeed, prevGainQ16,
// rewhiteFlag. pulses receives the frame's quantized pulse signal.
//
// Coefficient arrays mirror the C parameters: predCoefQ12 is laid out as up to
// two MAX_LPC_ORDER blocks (interpolated + non-interpolated), ltpCoefQ14 is
// LTP_ORDER per subframe, arQ13 is MAX_SHAPE_LPC_ORDER per subframe, and the
// HarmShapeGain/Tilt/LFShp/Gains/pitchL arrays are one entry per subframe.
func silkNSQFixed(
	nsq *NSQState,
	seed int,
	signalType int,
	quantOffsetType int,
	nlsfInterpCoefQ2 int,
	x16 []int16,
	pulses []int8,
	predCoefQ12 []int16,
	ltpCoefQ14 []int16,
	arQ13 []int16,
	harmShapeGainQ14 []int32,
	tiltQ14 []int32,
	lfShpQ14 []int32,
	gainsQ16 []int32,
	pitchL []int32,
	lambdaQ10 int32,
	ltpScaleQ14 int32,
	ltpMemLength int,
	frameLength int,
	subfrLength int,
	nbSubfr int,
	predictLPCOrder int,
	shapingLPCOrder int,
) {
	nsq.randSeed = int32(seed)

	// Set unvoiced lag to the previous one, overwrite later for voiced.
	lag := int(nsq.lagPrev)

	offsetQ10 := int(quantOffsets[signalType>>1][quantOffsetType])

	lsfInterpolationFlag := 1
	if nlsfInterpCoefQ2 == 4 {
		lsfInterpolationFlag = 0
	}

	sLTPQ15 := make([]int32, ltpMemLength+frameLength)
	sLTP := make([]int16, ltpMemLength+frameLength)
	xScQ10 := make([]int32, subfrLength)

	// Set up pointers to start of sub frame.
	nsq.sLTPShpBufIdx = ltpMemLength
	nsq.sLTPBufIdx = ltpMemLength
	pxqOffset := ltpMemLength

	for k := 0; k < nbSubfr; k++ {
		aQ12Off := ((k >> 1) | (1 - lsfInterpolationFlag)) * maxLPCOrder
		aQ12 := predCoefQ12[aQ12Off : aQ12Off+predictLPCOrder]
		bQ14 := ltpCoefQ14[k*ltpOrderConst : (k+1)*ltpOrderConst]
		arShpQ13 := arQ13[k*maxShapeLpcOrder : k*maxShapeLpcOrder+shapingLPCOrder]

		// Noise shape parameters: pack the symmetric FIR coefficients.
		harmShapeFIRPackedQ14 := silk_RSHIFT(harmShapeGainQ14[k], 2)
		harmShapeFIRPackedQ14 |= silk_LSHIFT32(silk_RSHIFT(harmShapeGainQ14[k], 1), 16)

		nsq.rewhiteFlag = 0
		if signalType == typeVoiced {
			lag = int(pitchL[k])

			// Re-whitening.
			if (k & (3 - (lsfInterpolationFlag << 1))) == 0 {
				// Rewhiten with new A coefs.
				startIdx := ltpMemLength - lag - predictLPCOrder - ltpOrderConst/2
				silkLPCAnalysisFilterFixed(
					sLTP[startIdx:],
					nsq.xq[startIdx+k*subfrLength:],
					aQ12[:predictLPCOrder],
					ltpMemLength-startIdx,
					predictLPCOrder,
				)

				nsq.rewhiteFlag = 1
				nsq.sLTPBufIdx = ltpMemLength
			}
		}

		silkNSQScaleStatesFixed(
			nsq,
			x16[k*subfrLength:],
			xScQ10,
			sLTP,
			sLTPQ15,
			k,
			ltpScaleQ14,
			gainsQ16,
			pitchL,
			signalType,
			subfrLength,
			ltpMemLength,
		)

		silkNoiseShapeQuantizerFixed(
			nsq,
			signalType,
			xScQ10,
			pulses[k*subfrLength:(k+1)*subfrLength],
			nsq.xq[pxqOffset:pxqOffset+subfrLength],
			sLTPQ15,
			aQ12,
			bQ14,
			arShpQ13,
			lag,
			harmShapeFIRPackedQ14,
			tiltQ14[k],
			lfShpQ14[k],
			gainsQ16[k],
			lambdaQ10,
			offsetQ10,
			subfrLength,
			shapingLPCOrder,
			predictLPCOrder,
		)

		pxqOffset += subfrLength
	}

	// Update lagPrev for next frame.
	nsq.lagPrev = pitchL[nbSubfr-1]

	// Save quantized speech and noise shaping signals.
	copy(nsq.xq[:ltpMemLength], nsq.xq[frameLength:frameLength+ltpMemLength])
	copy(nsq.sLTPShpQ14[:ltpMemLength], nsq.sLTPShpQ14[frameLength:frameLength+ltpMemLength])
}

// silkNSQScaleStatesFixed is the bit-exact port of silk_nsq_scale_states
// (silk/NSQ.c). It scales the subframe input by 1/Gain into xScQ10, rescales
// the re-whitened LTP state after rewhitening, and applies the changing-gain
// rescale to all carried-over NSQ states.
func silkNSQScaleStatesFixed(
	nsq *NSQState,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	ltpScaleQ14 int32,
	gainsQ16 []int32,
	pitchL []int32,
	signalType int,
	subfrLength int,
	ltpMemLength int,
) {
	lag := int(pitchL[subfr])
	invGainQ31 := silk_INVERSE32_varQ(silk_max(gainsQ16[subfr], 1), 47)

	// Scale input.
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	for i := 0; i < subfrLength; i++ {
		xScQ10[i] = silkSMULWW(int32(x16[i]), invGainQ26)
	}

	// After rewhitening the LTP state is un-scaled, so scale with inv_gain_Q16.
	if nsq.rewhiteFlag != 0 {
		if subfr == 0 {
			// Do LTP downscaling.
			invGainQ31 = silk_LSHIFT32(silk_SMULWB(invGainQ31, ltpScaleQ14), 2)
		}
		for i := nsq.sLTPBufIdx - lag - ltpOrderConst/2; i < nsq.sLTPBufIdx; i++ {
			sLTPQ15[i] = silk_SMULWB(invGainQ31, int32(sLTP[i]))
		}
	}

	// Adjust for changing gain.
	if gainsQ16[subfr] != nsq.prevGainQ16 {
		gainAdjQ16 := silk_DIV32_varQ(nsq.prevGainQ16, gainsQ16[subfr], 16)

		// Scale long-term shaping state.
		for i := nsq.sLTPShpBufIdx - ltpMemLength; i < nsq.sLTPShpBufIdx; i++ {
			nsq.sLTPShpQ14[i] = silkSMULWW(gainAdjQ16, nsq.sLTPShpQ14[i])
		}

		// Scale long-term prediction state.
		if signalType == typeVoiced && nsq.rewhiteFlag == 0 {
			for i := nsq.sLTPBufIdx - lag - ltpOrderConst/2; i < nsq.sLTPBufIdx; i++ {
				sLTPQ15[i] = silkSMULWW(gainAdjQ16, sLTPQ15[i])
			}
		}

		nsq.sLFARShpQ14 = silkSMULWW(gainAdjQ16, nsq.sLFARShpQ14)
		nsq.sDiffShpQ14 = silkSMULWW(gainAdjQ16, nsq.sDiffShpQ14)

		// Scale short-term prediction and shaping states.
		for i := 0; i < nsqLpcBufLength; i++ {
			nsq.sLPCQ14[i] = silkSMULWW(gainAdjQ16, nsq.sLPCQ14[i])
		}
		for i := 0; i < maxShapeLpcOrder; i++ {
			nsq.sAR2Q14[i] = silkSMULWW(gainAdjQ16, nsq.sAR2Q14[i])
		}

		// Save inverse gain.
		nsq.prevGainQ16 = gainsQ16[subfr]
	}
}
