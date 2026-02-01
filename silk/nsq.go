// Package silk implements SILK Noise Shaping Quantization (NSQ).
// Reference: libopus silk/NSQ.c and silk/NSQ_del_dec.c
package silk

// NSQ constants from libopus define.h
const (
	nsqLpcBufLength    = 16               // NSQ_LPC_BUF_LENGTH = MAX_LPC_ORDER
	maxShapeLpcOrder   = 24               // MAX_SHAPE_LPC_ORDER
	harmShapeFirTaps   = 3                // HARM_SHAPE_FIR_TAPS
	ltpOrderConst      = 5                // LTP_ORDER
	decisionDelay      = 40               // DECISION_DELAY
	quantLevelAdjQ10   = 80               // QUANT_LEVEL_ADJUST_Q10
	offsetVLQ10        = 32               // OFFSET_VL_Q10
	offsetVHQ10        = 100              // OFFSET_VH_Q10
	offsetUVLQ10       = 100              // OFFSET_UVL_Q10
	offsetUVHQ10       = 240              // OFFSET_UVH_Q10
	maxFrameLengthNSQ  = 320              // MAX_FRAME_LENGTH for 20ms at 16kHz
	ltpMemLength       = 320              // LTP_MEM_LENGTH = 20ms * 16kHz
)

// NSQState holds the noise shaping quantizer state.
// Mirrors libopus silk_nsq_state structure.
type NSQState struct {
	// Buffer for quantized output signal
	xq [2 * maxFrameLengthNSQ]int16

	// Long-term shaping state (Q14)
	sLTPShpQ14 [2 * maxFrameLengthNSQ]int32

	// Short-term LPC state (Q14)
	sLPCQ14 [maxSubFrameLength + nsqLpcBufLength]int32

	// AR noise shaping state (Q14)
	sAR2Q14 [maxShapeLpcOrder]int32

	// Low-frequency AR shaping state (Q14)
	sLFARShpQ14 int32

	// Difference shaping state (Q14)
	sDiffShpQ14 int32

	// Previous pitch lag
	lagPrev int

	// LTP buffer index
	sLTPBufIdx int

	// LTP shaping buffer index
	sLTPShpBufIdx int

	// Random seed for dithering
	randSeed int32

	// Previous gain (Q16)
	prevGainQ16 int32

	// Rewhitening flag
	rewhiteFlag int

	// Pre-allocated scratch buffers for zero-allocation encoding
	scratchPulses  []int8  // Size: maxFrameLengthNSQ = 320
	scratchXq      []int16 // Size: maxFrameLengthNSQ = 320
	scratchSLTPQ15 []int32 // Size: ltpMemLength + maxFrameLengthNSQ = 640
	scratchSLTP    []int16 // Size: ltpMemLength + maxFrameLengthNSQ = 640
	scratchXScQ10  []int32 // Size: maxSubFrameLength = 80
}

// NewNSQState creates a new NSQ state with proper initialization.
func NewNSQState() *NSQState {
	state := &NSQState{
		prevGainQ16: 65536, // 1.0 in Q16
		// Pre-allocate scratch buffers for zero-allocation encoding
		scratchPulses:  make([]int8, maxFrameLengthNSQ),
		scratchXq:      make([]int16, maxFrameLengthNSQ),
		scratchSLTPQ15: make([]int32, ltpMemLength+maxFrameLengthNSQ),
		scratchSLTP:    make([]int16, ltpMemLength+maxFrameLengthNSQ),
		scratchXScQ10:  make([]int32, maxSubFrameLength),
	}
	return state
}

// Reset clears the NSQ state for a new stream.
func (s *NSQState) Reset() {
	for i := range s.xq {
		s.xq[i] = 0
	}
	for i := range s.sLTPShpQ14 {
		s.sLTPShpQ14[i] = 0
	}
	for i := range s.sLPCQ14 {
		s.sLPCQ14[i] = 0
	}
	for i := range s.sAR2Q14 {
		s.sAR2Q14[i] = 0
	}
	s.sLFARShpQ14 = 0
	s.sDiffShpQ14 = 0
	s.lagPrev = 0
	s.sLTPBufIdx = 0
	s.sLTPShpBufIdx = 0
	s.randSeed = 0
	s.prevGainQ16 = 65536
	s.rewhiteFlag = 0
}

// NSQParams holds parameters for noise shaping quantization.
type NSQParams struct {
	// Signal type: 0=inactive, 1=unvoiced, 2=voiced
	SignalType int

	// Quantization offset type: 0=low, 1=high
	QuantOffsetType int

	// LPC prediction coefficients (Q12)
	PredCoefQ12 []int16

	// LTP coefficients (Q14), 5 per subframe
	LTPCoefQ14 []int16

	// Noise shaping AR coefficients (Q13)
	ARShpQ13 []int16

	// Harmonic shaping gain (Q14)
	HarmShapeGainQ14 []int

	// Spectral tilt (Q14)
	TiltQ14 []int

	// Low-frequency shaping (Q14)
	LFShpQ14 []int32

	// Quantization gains (Q16) per subframe
	GainsQ16 []int32

	// Pitch lags per subframe
	PitchL []int

	// Rate/distortion tradeoff (Lambda, Q10)
	LambdaQ10 int

	// LTP scale (Q14) for first subframe
	LTPScaleQ14 int

	// Frame configuration
	FrameLength   int
	SubfrLength   int
	NbSubfr       int
	LTPMemLength  int
	PredLPCOrder  int
	ShapeLPCOrder int

	// LCG seed for dithering
	Seed int
}

// NoiseShapeQuantize performs noise shaping quantization on input samples.
// This is the main NSQ function matching libopus silk_NSQ_c.
//
// Parameters:
//   - nsq: NSQ state (modified)
//   - input: Input signal (Q0, scaled)
//   - params: NSQ parameters
//
// Returns:
//   - pulses: Quantized pulse signal (int8 per sample)
//   - xq: Quantized/reconstructed signal
func NoiseShapeQuantize(nsq *NSQState, input []int16, params *NSQParams) ([]int8, []int16) {
	frameLength := params.FrameLength
	subfrLength := params.SubfrLength
	nbSubfr := params.NbSubfr
	ltpMemLength := params.LTPMemLength

	// Initialize random seed
	nsq.randSeed = int32(params.Seed)

	// Get quantization offset
	offsetQ10 := getQuantizationOffset(params.SignalType, params.QuantOffsetType)

	// Set unvoiced lag to previous, overwrite for voiced
	lag := nsq.lagPrev

	// Use pre-allocated output buffers if available, otherwise allocate
	var pulses []int8
	if nsq.scratchPulses != nil && len(nsq.scratchPulses) >= frameLength {
		pulses = nsq.scratchPulses[:frameLength]
		for i := range pulses {
			pulses[i] = 0
		}
	} else {
		pulses = make([]int8, frameLength)
	}

	var xq []int16
	if nsq.scratchXq != nil && len(nsq.scratchXq) >= frameLength {
		xq = nsq.scratchXq[:frameLength]
		for i := range xq {
			xq[i] = 0
		}
	} else {
		xq = make([]int16, frameLength)
	}

	// Use pre-allocated working buffers if available
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

	// Check LSF interpolation
	lsfInterpFlag := 1 // Assume interpolation enabled

	// Set up pointers
	nsq.sLTPShpBufIdx = ltpMemLength
	nsq.sLTPBufIdx = ltpMemLength

	// Process each subframe
	for k := 0; k < nbSubfr; k++ {
		// Get coefficients for this subframe
		predCoefIdx := ((k >> 1) | (1 - lsfInterpFlag)) * maxLPCOrder
		aQ12 := params.PredCoefQ12[predCoefIdx : predCoefIdx+params.PredLPCOrder]
		bQ14 := params.LTPCoefQ14[k*ltpOrderConst : (k+1)*ltpOrderConst]
		arShpQ13 := params.ARShpQ13[k*maxShapeLpcOrder : (k+1)*maxShapeLpcOrder]

		// Pack harmonic shape FIR coefficients
		harmShapeFIRPackedQ14 := int32(params.HarmShapeGainQ14[k]>>2) |
			(int32(params.HarmShapeGainQ14[k]>>1) << 16)

		nsq.rewhiteFlag = 0
		if params.SignalType == typeVoiced {
			lag = params.PitchL[k]

			// Re-whitening for voiced frames at specific subframes
			if (k & (3 - (lsfInterpFlag << 1))) == 0 {
				// Compute start index for rewhitening
				startIdx := ltpMemLength - lag - params.PredLPCOrder - ltpOrderConst/2
				if startIdx < 0 {
					startIdx = 0
				}

				// Rewhiten with LPC analysis filter
				rewhitenLTP(sLTP, nsq.xq[:], startIdx, k*subfrLength, aQ12, ltpMemLength-startIdx, params.PredLPCOrder)

				nsq.rewhiteFlag = 1
				nsq.sLTPBufIdx = ltpMemLength
			}
		}

		// Scale states
		scaleNSQStates(nsq, input[k*subfrLength:], xScQ10, sLTP, sLTPQ15,
			k, params.LTPScaleQ14, params.GainsQ16, params.PitchL,
			params.SignalType, subfrLength, ltpMemLength)

		// Noise shape quantizer for this subframe
		noiseShapeQuantizerSubframe(
			nsq,
			params.SignalType,
			xScQ10,
			pulses[k*subfrLength:(k+1)*subfrLength],
			xq[k*subfrLength:(k+1)*subfrLength],
			sLTPQ15,
			aQ12,
			bQ14,
			arShpQ13[:params.ShapeLPCOrder],
			lag,
			harmShapeFIRPackedQ14,
			params.TiltQ14[k],
			params.LFShpQ14[k],
			params.GainsQ16[k],
			params.LambdaQ10,
			offsetQ10,
			subfrLength,
			params.ShapeLPCOrder,
			params.PredLPCOrder,
		)
	}

	// Update state for next frame
	nsq.lagPrev = params.PitchL[nbSubfr-1]

	// Shift buffers
	copy(nsq.xq[:ltpMemLength], nsq.xq[frameLength:frameLength+ltpMemLength])
	copy(nsq.sLTPShpQ14[:ltpMemLength], nsq.sLTPShpQ14[frameLength:frameLength+ltpMemLength])

	return pulses, xq
}

// noiseShapeQuantizerSubframe quantizes one subframe with noise shaping.
// Matches libopus silk_noise_shape_quantizer.
func noiseShapeQuantizerSubframe(
	nsq *NSQState,
	signalType int,
	xScQ10 []int32,
	pulses []int8,
	xq []int16,
	sLTPQ15 []int32,
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
	shapingLPCOrder int,
	predictLPCOrder int,
) {
	// Get pointers into LTP buffers
	shpLagPtr := nsq.sLTPShpBufIdx - lag + harmShapeFirTaps/2
	predLagPtr := nsq.sLTPBufIdx - lag + ltpOrderConst/2

	gainQ10 := gainQ16 >> 6

	// Set up short-term AR state pointer
	psLPCQ14Idx := nsqLpcBufLength - 1

	for i := 0; i < length; i++ {
		// Generate dither
		nsq.randSeed = silk_RAND(nsq.randSeed)

		// Short-term prediction (LPC)
		lpcPredQ10 := shortTermPrediction(nsq.sLPCQ14[:], psLPCQ14Idx, aQ12, predictLPCOrder)

		// Long-term prediction (LTP) for voiced
		var ltpPredQ13 int32
		if signalType == typeVoiced && predLagPtr >= 0 && predLagPtr < len(sLTPQ15) {
			ltpPredQ13 = 2 // Rounding bias
			for j := 0; j < ltpOrderConst; j++ {
				idx := predLagPtr - j
				if idx >= 0 && idx < len(sLTPQ15) {
					ltpPredQ13 += silk_SMLAWB(0, sLTPQ15[idx], int32(bQ14[j]))
				}
			}
			predLagPtr++
		}

		// Noise shape feedback (AR filter)
		nARQ12 := noiseShapeFeedback(nsq.sDiffShpQ14, nsq.sAR2Q14[:], arShpQ13, shapingLPCOrder)

		// Add tilt component
		nARQ12 = silk_SMLAWB(nARQ12, nsq.sLFARShpQ14, int32(tiltQ14))

		// Low-frequency shaping
		nLFQ12 := int32(0)
		if nsq.sLTPShpBufIdx > 0 {
			nLFQ12 = silk_SMULWB(nsq.sLTPShpQ14[nsq.sLTPShpBufIdx-1], lfShpQ14)
		}
		nLFQ12 = silk_SMLAWT(nLFQ12, nsq.sLFARShpQ14, lfShpQ14)

		// Combine prediction and noise shaping
		tmp1 := silk_SUB32(silk_LSHIFT32(lpcPredQ10, 2), nARQ12) // Q12
		tmp1 = silk_SUB32(tmp1, nLFQ12)                          // Q12

		var nLTPQ13 int32
		if lag > 0 && shpLagPtr >= 0 && shpLagPtr < len(nsq.sLTPShpQ14) {
			// Symmetric FIR for harmonic shaping
			shp0, shp1, shp2 := int32(0), int32(0), int32(0)
			if shpLagPtr >= 0 && shpLagPtr < len(nsq.sLTPShpQ14) {
				shp0 = nsq.sLTPShpQ14[shpLagPtr]
			}
			if shpLagPtr-1 >= 0 && shpLagPtr-1 < len(nsq.sLTPShpQ14) {
				shp1 = nsq.sLTPShpQ14[shpLagPtr-1]
			}
			if shpLagPtr-2 >= 0 && shpLagPtr-2 < len(nsq.sLTPShpQ14) {
				shp2 = nsq.sLTPShpQ14[shpLagPtr-2]
			}
			nLTPQ13 = silk_SMULWB(silk_ADD_SAT32(shp0, shp2), harmShapeFIRPackedQ14)
			nLTPQ13 = silk_SMLAWT(nLTPQ13, shp1, harmShapeFIRPackedQ14)
			nLTPQ13 = silk_LSHIFT32(nLTPQ13, 1)
			shpLagPtr++

			tmp2 := silk_SUB32(ltpPredQ13, nLTPQ13)            // Q13
			tmp1 = silk_ADD32(tmp2, silk_LSHIFT32(tmp1, 1))    // Q13
			tmp1 = silk_RSHIFT_ROUND(tmp1, 3)                  // Q10
		} else {
			tmp1 = silk_RSHIFT_ROUND(tmp1, 2) // Q10
		}

		// Residual error
		rQ10 := silk_SUB32(xScQ10[i], tmp1)

		// Flip sign depending on dither
		if nsq.randSeed < 0 {
			rQ10 = -rQ10
		}

		// Limit range
		rQ10 = silk_LIMIT_32(rQ10, -(31 << 10), 30<<10)

		// Rate-distortion quantization
		q1Q10, q2Q10, rd1Q20, rd2Q20 := computeRDQuantization(rQ10, offsetQ10, lambdaQ10)

		// Compute distortion for both candidates
		rrQ10 := silk_SUB32(rQ10, q1Q10)
		rd1Q20 = silk_SMLABB(rd1Q20, rrQ10, rrQ10)
		rrQ10 = silk_SUB32(rQ10, q2Q10)
		rd2Q20 = silk_SMLABB(rd2Q20, rrQ10, rrQ10)

		// Select best quantization level
		if rd2Q20 < rd1Q20 {
			q1Q10 = q2Q10
		}

		// Store pulse
		pulses[i] = int8(silk_RSHIFT_ROUND(q1Q10, 10))

		// Compute excitation
		excQ14 := silk_LSHIFT32(q1Q10, 4)
		if nsq.randSeed < 0 {
			excQ14 = -excQ14
		}

		// Add predictions
		lpcExcQ14 := silk_ADD_LSHIFT32(excQ14, ltpPredQ13, 1)
		xqQ14 := silk_ADD32(lpcExcQ14, silk_LSHIFT32(lpcPredQ10, 4))

		// Scale back to output level
		xq[i] = int16(silk_SAT16(silk_RSHIFT_ROUND(silk_SMULWW(xqQ14, gainQ10), 8)))

		// Update states
		psLPCQ14Idx++
		if psLPCQ14Idx < len(nsq.sLPCQ14) {
			nsq.sLPCQ14[psLPCQ14Idx] = xqQ14
		}

		nsq.sDiffShpQ14 = silk_SUB32(xqQ14, silk_LSHIFT32(xScQ10[i], 4))
		sLFARShpQ14 := silk_SUB32(nsq.sDiffShpQ14, silk_LSHIFT32(nARQ12, 2))
		nsq.sLFARShpQ14 = sLFARShpQ14

		if nsq.sLTPShpBufIdx < len(nsq.sLTPShpQ14) {
			nsq.sLTPShpQ14[nsq.sLTPShpBufIdx] = silk_SUB32(sLFARShpQ14, silk_LSHIFT32(nLFQ12, 2))
		}
		if nsq.sLTPBufIdx < len(sLTPQ15) {
			sLTPQ15[nsq.sLTPBufIdx] = silk_LSHIFT32(lpcExcQ14, 1)
		}

		nsq.sLTPShpBufIdx++
		nsq.sLTPBufIdx++

		// Update dither based on quantized signal
		nsq.randSeed = silk_ADD32(nsq.randSeed, int32(pulses[i]))
	}

	// Update LPC synth buffer
	copy(nsq.sLPCQ14[:nsqLpcBufLength], nsq.sLPCQ14[length:length+nsqLpcBufLength])
}

// shortTermPrediction computes LPC prediction.
// Matches libopus silk_noise_shape_quantizer_short_prediction_c.
func shortTermPrediction(sLPCQ14 []int32, idx int, aQ12 []int16, order int) int32 {
	// Rounding bias
	out := int32(order >> 1)

	for k := 0; k < order && idx-k >= 0; k++ {
		out = silk_SMLAWB(out, sLPCQ14[idx-k], int32(aQ12[k]))
	}

	return out
}

// noiseShapeFeedback computes AR noise shaping feedback.
// Matches libopus silk_NSQ_noise_shape_feedback_loop_c.
func noiseShapeFeedback(sDiffShpQ14 int32, sAR2Q14 []int32, arShpQ13 []int16, order int) int32 {
	tmp2 := sDiffShpQ14
	tmp1 := sAR2Q14[0]
	sAR2Q14[0] = tmp2

	out := int32(order >> 1)
	out = silk_SMLAWB(out, tmp2, int32(arShpQ13[0]))

	for j := 2; j < order; j += 2 {
		tmp2 = sAR2Q14[j-1]
		sAR2Q14[j-1] = tmp1
		out = silk_SMLAWB(out, tmp1, int32(arShpQ13[j-1]))

		tmp1 = sAR2Q14[j]
		sAR2Q14[j] = tmp2
		out = silk_SMLAWB(out, tmp2, int32(arShpQ13[j]))
	}

	if order > 0 {
		sAR2Q14[order-1] = tmp1
		out = silk_SMLAWB(out, tmp1, int32(arShpQ13[order-1]))
	}

	// Q11 -> Q12
	out = silk_LSHIFT32(out, 1)
	return out
}

// computeRDQuantization finds two quantization candidates with R-D cost.
func computeRDQuantization(rQ10 int32, offsetQ10, lambdaQ10 int) (q1Q10, q2Q10, rd1Q20, rd2Q20 int32) {
	q1Q10 = silk_SUB32(rQ10, int32(offsetQ10))
	q1Q0 := silk_RSHIFT(q1Q10, 10)

	// For aggressive RDO, adjust bias
	if lambdaQ10 > 2048 {
		rdoOffset := int32(lambdaQ10/2 - 512)
		if q1Q10 > rdoOffset {
			q1Q0 = silk_RSHIFT(q1Q10-rdoOffset, 10)
		} else if q1Q10 < -rdoOffset {
			q1Q0 = silk_RSHIFT(q1Q10+rdoOffset, 10)
		} else if q1Q10 < 0 {
			q1Q0 = -1
		} else {
			q1Q0 = 0
		}
	}

	if q1Q0 > 0 {
		q1Q10 = silk_SUB32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(q1Q10, int32(lambdaQ10))
		rd2Q20 = silk_SMULBB(q2Q10, int32(lambdaQ10))
	} else if q1Q0 == 0 {
		q1Q10 = int32(offsetQ10)
		q2Q10 = silk_ADD32(q1Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(q1Q10, int32(lambdaQ10))
		rd2Q20 = silk_SMULBB(q2Q10, int32(lambdaQ10))
	} else if q1Q0 == -1 {
		q2Q10 = int32(offsetQ10)
		q1Q10 = silk_SUB32(q2Q10, 1024-quantLevelAdjQ10)
		rd1Q20 = silk_SMULBB(-q1Q10, int32(lambdaQ10))
		rd2Q20 = silk_SMULBB(q2Q10, int32(lambdaQ10))
	} else {
		q1Q10 = silk_ADD32(silk_LSHIFT32(q1Q0, 10), quantLevelAdjQ10)
		q1Q10 = silk_ADD32(q1Q10, int32(offsetQ10))
		q2Q10 = silk_ADD32(q1Q10, 1024)
		rd1Q20 = silk_SMULBB(-q1Q10, int32(lambdaQ10))
		rd2Q20 = silk_SMULBB(-q2Q10, int32(lambdaQ10))
	}

	return q1Q10, q2Q10, rd1Q20, rd2Q20
}

// scaleNSQStates scales NSQ states for gain changes.
// Matches libopus silk_nsq_scale_states.
func scaleNSQStates(
	nsq *NSQState,
	x16 []int16,
	xScQ10 []int32,
	sLTP []int16,
	sLTPQ15 []int32,
	subfr int,
	ltpScaleQ14 int,
	gainsQ16 []int32,
	pitchL []int,
	signalType int,
	subfrLength int,
	ltpMemLength int,
) {
	lag := pitchL[subfr]
	invGainQ31 := silk_INVERSE32_varQ(silk_max(gainsQ16[subfr], 1), 47)

	// Scale input
	invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
	for i := 0; i < subfrLength && i < len(x16); i++ {
		xScQ10[i] = silk_SMULWW(int32(x16[i]), invGainQ26)
	}

	// After rewhitening, scale LTP state
	if nsq.rewhiteFlag != 0 {
		if subfr == 0 {
			// LTP downscaling for first subframe
			invGainQ31 = silk_LSHIFT32(silk_SMULWB(invGainQ31, int32(ltpScaleQ14)), 2)
		}
		startIdx := nsq.sLTPBufIdx - lag - ltpOrderConst/2
		if startIdx < 0 {
			startIdx = 0
		}
		for i := startIdx; i < nsq.sLTPBufIdx && i < len(sLTPQ15) && i < len(sLTP); i++ {
			sLTPQ15[i] = silk_SMULWB(invGainQ31, int32(sLTP[i]))
		}
	}

	// Adjust for changing gain
	if gainsQ16[subfr] != nsq.prevGainQ16 {
		gainAdjQ16 := silk_DIV32_varQ(nsq.prevGainQ16, gainsQ16[subfr], 16)

		// Scale long-term shaping state
		startIdx := nsq.sLTPShpBufIdx - ltpMemLength
		if startIdx < 0 {
			startIdx = 0
		}
		for i := startIdx; i < nsq.sLTPShpBufIdx && i < len(nsq.sLTPShpQ14); i++ {
			nsq.sLTPShpQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLTPShpQ14[i])
		}

		// Scale long-term prediction state
		if signalType == typeVoiced && nsq.rewhiteFlag == 0 {
			startIdx := nsq.sLTPBufIdx - lag - ltpOrderConst/2
			if startIdx < 0 {
				startIdx = 0
			}
			for i := startIdx; i < nsq.sLTPBufIdx && i < len(sLTPQ15); i++ {
				sLTPQ15[i] = silk_SMULWW(gainAdjQ16, sLTPQ15[i])
			}
		}

		nsq.sLFARShpQ14 = silk_SMULWW(gainAdjQ16, nsq.sLFARShpQ14)
		nsq.sDiffShpQ14 = silk_SMULWW(gainAdjQ16, nsq.sDiffShpQ14)

		// Scale short-term states
		for i := 0; i < nsqLpcBufLength; i++ {
			nsq.sLPCQ14[i] = silk_SMULWW(gainAdjQ16, nsq.sLPCQ14[i])
		}
		for i := 0; i < maxShapeLpcOrder; i++ {
			nsq.sAR2Q14[i] = silk_SMULWW(gainAdjQ16, nsq.sAR2Q14[i])
		}

		nsq.prevGainQ16 = gainsQ16[subfr]
	}
}

// rewhitenLTP applies LPC analysis filter for rewhitening.
func rewhitenLTP(sLTP []int16, xq []int16, startIdx, offset int, aQ12 []int16, length, order int) {
	for i := startIdx; i < startIdx+length && i < len(sLTP); i++ {
		xqIdx := i + offset
		if xqIdx < 0 || xqIdx >= len(xq) {
			continue
		}

		// LPC analysis filter: e[n] = x[n] - sum(a[k] * x[n-k-1])
		out := int32(xq[xqIdx]) << 12 // Q12
		for k := 0; k < order; k++ {
			prevIdx := xqIdx - k - 1
			if prevIdx >= 0 && prevIdx < len(xq) {
				out -= int32(aQ12[k]) * int32(xq[prevIdx])
			}
		}
		sLTP[i] = int16(silk_RSHIFT_ROUND(out, 12))
	}
}

// getQuantizationOffset returns the quantization offset based on signal type and offset type.
func getQuantizationOffset(signalType, quantOffsetType int) int {
	// Per libopus: silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]
	offsets := [][]int{
		{offsetUVLQ10, offsetUVHQ10}, // Unvoiced (signalType 0, 1)
		{offsetVLQ10, offsetVHQ10},   // Voiced (signalType 2, 3)
	}

	sigIdx := signalType >> 1
	if sigIdx < 0 {
		sigIdx = 0
	}
	if sigIdx > 1 {
		sigIdx = 1
	}
	if quantOffsetType < 0 {
		quantOffsetType = 0
	}
	if quantOffsetType > 1 {
		quantOffsetType = 1
	}

	return offsets[sigIdx][quantOffsetType]
}

// Fixed-point math helpers matching libopus SigProc_FIX.h

func silk_RAND(seed int32) int32 {
	// Linear congruential generator
	return seed*196314165 + 907633515
}

func silk_SMLAWB(a, b, c int32) int32 {
	// a + ((b * (int16)(c)) >> 16)
	// Per libopus: uses signed int16 extraction
	c16 := int16(c) // Extract low 16 bits as signed
	return a + int32((int64(b)*int64(c16))>>16)
}

func silk_SMLAWT(a, b, c int32) int32 {
	// a + ((b * (c >> 16)) >> 16)
	return a + ((b * (c >> 16)) >> 16)
}

func silk_SMULWB(a, b int32) int32 {
	// (a * (int16)(b)) >> 16
	// Per libopus: uses signed int16 extraction
	b16 := int16(b)
	return int32((int64(a) * int64(b16)) >> 16)
}

func silk_SMULWW(a, b int32) int32 {
	// Full 32x32 multiply returning high 32 bits
	result := int64(a) * int64(b)
	return int32(result >> 16)
}

func silk_SMULBB(a, b int32) int32 {
	// Low 16-bit * low 16-bit
	return (a & 0xFFFF) * (b & 0xFFFF)
}

func silk_SMLABB(a, b, c int32) int32 {
	return a + silk_SMULBB(b, c)
}

func silk_LSHIFT32(a int32, shift int) int32 {
	if shift < 0 {
		return a >> (-shift)
	}
	return a << shift
}

func silk_RSHIFT(a int32, shift int) int32 {
	if shift < 0 {
		return a << (-shift)
	}
	return a >> shift
}

func silk_RSHIFT_ROUND(a int32, shift int) int32 {
	if shift <= 0 {
		return a << (-shift)
	}
	return (a + (1 << (shift - 1))) >> shift
}

func silk_ADD32(a, b int32) int32 {
	return a + b
}

func silk_SUB32(a, b int32) int32 {
	return a - b
}

func silk_ADD_SAT32(a, b int32) int32 {
	result := int64(a) + int64(b)
	if result > 0x7FFFFFFF {
		return 0x7FFFFFFF
	}
	if result < -0x80000000 {
		return -0x80000000
	}
	return int32(result)
}

func silk_ADD_LSHIFT32(a, b int32, shift int) int32 {
	return a + (b << shift)
}

func silk_LIMIT_32(val, minVal, maxVal int32) int32 {
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

func silk_SAT16(a int32) int32 {
	if a > 32767 {
		return 32767
	}
	if a < -32768 {
		return -32768
	}
	return a
}

func silk_max(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// silk_INVERSE32_varQ computes 1/a in Qres format
func silk_INVERSE32_varQ(a int32, qres int) int32 {
	if a <= 0 {
		return 0x7FFFFFFF
	}

	// Approximate inverse using direct division
	// result = (1 << qres) / a
	result := int32((int64(1) << qres) / int64(a))

	return result
}

// silk_DIV32_varQ computes a/b in Qres format
func silk_DIV32_varQ(a, b int32, qres int) int32 {
	if b == 0 {
		if a >= 0 {
			return 0x7FFFFFFF
		}
		return -0x80000000
	}

	result := int32((int64(a) << qres) / int64(b))
	return result
}
