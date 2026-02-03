package silk

// EncodeFrame encodes a complete SILK frame to bitstream.
// Returns encoded bytes. If a range encoder was pre-set via SetRangeEncoder(),
// it will be used (for hybrid mode) and nil is returned since the caller
// manages the shared encoder.
//
// When FEC (Forward Error Correction) is enabled via SetFEC(true), this function
// also encodes LBRR (Low Bitrate Redundancy) data for the previous frame.
// The LBRR data is embedded at the start of each packet and allows the decoder
// to recover from packet loss by using the redundant encoding.
//
// Reference: libopus silk/float/encode_frame_FLP.c
func (e *Encoder) EncodeFrame(pcm []float32, lookahead []float32, vadFlag bool) []byte {
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples
	numSubframes := len(pcm) / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
		subframeSamples = len(pcm)
	}
	if numSubframes > maxNbSubfr {
		numSubframes = maxNbSubfr
	}
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	// Update target SNR based on configured bitrate and frame size.
	if e.targetRateBps > 0 {
		e.controlSNR(e.targetRateBps, numSubframes)
	}

	// Quantize input to int16 precision to match libopus float API behavior.
	pcm = e.quantizePCMToInt16(pcm)

	// Check if we have a pre-set range encoder (hybrid mode)
	// Note: rangeEncoder is set externally via SetRangeEncoder() for hybrid mode.
	// In standalone mode, rangeEncoder should be nil at the start of each frame.
	useSharedEncoder := e.rangeEncoder != nil

	if !useSharedEncoder {
		// Standalone SILK mode: each call produces a separate packet
		// Reset frame counter and encoding state so frame is encoded as independent
		// This ensures gain indices are absolute, not deltas from previous packet
		// The decoder also resets nFramesDecoded=0 at the start of each packet
		e.nFramesEncoded = 0

		// Create our own range encoder using scratch buffer
		// Allocate extra space for potential LBRR data
		bufSize := len(pcm) / 3
		if bufSize < 80 {
			bufSize = 80
		}
		// Add extra for LBRR if enabled
		if e.lbrrEnabled {
			bufSize += 50
		}
		if bufSize < maxSilkPacketBytes {
			bufSize = maxSilkPacketBytes
		}
		output := ensureByteSlice(&e.scratchOutput, bufSize)
		e.scratchRangeEncoder.Init(output)
		e.rangeEncoder = &e.scratchRangeEncoder
	}

	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}

	// Step 1: Determine activity and defaults
	var signalType, quantOffset int
	speechActivityQ8 := 0
	if vadFlag {
		signalType = typeUnvoiced
		quantOffset = 0
		if e.speechActivitySet {
			speechActivityQ8 = e.speechActivityQ8
		} else {
			speechActivityQ8 = 200
		}
	} else {
		signalType = typeNoVoiceActivity
		quantOffset = 0
		if e.speechActivitySet {
			speechActivityQ8 = e.speechActivityQ8
		} else {
			speechActivityQ8 = 50
		}
	}

	// Step 1.1: Update noise shaping lookahead buffer and select delayed frame
	framePCM := e.updateShapeBuffer(pcm, frameSamples)

	// Step 1.2: Update pitch analysis buffer with delayed frame
	pitchBufFrameLen := len(framePCM)
	if pitchBufFrameLen > 0 && len(e.pitchAnalysisBuf) > 0 {
		if len(e.pitchAnalysisBuf) > pitchBufFrameLen {
			copy(e.pitchAnalysisBuf, e.pitchAnalysisBuf[pitchBufFrameLen:])
		}
		start := len(e.pitchAnalysisBuf) - pitchBufFrameLen
		if start < 0 {
			start = 0
			pitchBufFrameLen = len(e.pitchAnalysisBuf)
		}
		copy(e.pitchAnalysisBuf[start:], framePCM[:pitchBufFrameLen])
	}

	// Step 1.5: Encode LBRR data/header placeholder (standalone SILK only)
	// Reserve VAD/LBRR header bits and emit any LBRR data from the previous packet.
	// In hybrid mode, the Opus layer handles the SILK header.
	if !useSharedEncoder {
		e.encodeLBRRData(e.rangeEncoder, 1, true) // mono only in standalone
	}

	// Step 2: Pitch detection and LTP (voiced only)
	var pitchLags []int
	var lagIndex, contourIndex int
	var pitchParams pitchEncodeParams
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	var ltpIndices [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)
	residual, residual32, resStart, _ := e.computePitchResidual(numSubframes, lookahead)
	if signalType != typeNoVoiceActivity {
		// Use pitch residual for more accurate pitch detection (libopus parity).
		searchThres1 := float64(e.pitchEstimationThresholdQ16) / 65536.0
		prevSignalType := 0
		if e.isPreviousFrameVoiced {
			prevSignalType = 2
		}
		thrhld := 0.6 - 0.004*float64(e.pitchEstimationLPCOrder) -
			0.1*float64(speechActivityQ8)/256.0 -
			0.15*float64(prevSignalType>>1) -
			0.1*float64(e.inputTiltQ15)/32768.0
		if thrhld < 0 {
			thrhld = 0
		} else if thrhld > 1 {
			thrhld = 1
		}
		pitchLags, lagIndex, contourIndex = e.detectPitch(residual32, numSubframes, searchThres1, thrhld)

		// Update LTP correlation for noise shaping (from pitch detection)
		e.ltpCorr = float32(e.pitchState.ltpCorr)
		if e.ltpCorr > 1.0 {
			e.ltpCorr = 1.0
		}

		if e.ltpCorr > 0 {
			signalType = typeVoiced
			pitchParams = e.preparePitchLags(pitchLags, numSubframes, lagIndex, contourIndex)
			ltpCoeffs, ltpIndices, perIndex, predGainQ7 = e.analyzeLTPQuantized(residual, resStart, pitchLags, numSubframes, subframeSamples)
			ltpScaleIndex = e.computeLTPScaleIndex(predGainQ7, condCoding)
		} else {
			signalType = typeUnvoiced
			e.sumLogGainQ7 = 0
		}
	} else {
		e.ltpCorr = 0
		e.sumLogGainQ7 = 0
	}

	// Step 3: Noise shaping analysis (sparseness quant offset, gains, shaping AR)
	noiseParams, gains, quantOffset := e.noiseShapeAnalysis(
		framePCM,
		residual,
		resStart,
		signalType,
		speechActivityQ8,
		e.lastLPCGain,
		pitchLags,
		quantOffset,
		numSubframes,
		subframeSamples,
		lookahead,
	)

	// Step 4: Build LTP residual and compute LPC from it
	fsKHz := config.SampleRate / 1000
	ltpMemSamples := ltpMemLengthMs * fsKHz
	pitchBuf := e.inputBuffer
	frameStart := ltpMemSamples
	if frameStart+frameSamples > len(pitchBuf) {
		if len(pitchBuf) > frameSamples {
			frameStart = len(pitchBuf) - frameSamples
		} else {
			frameStart = 0
		}
	}
	ltpRes := e.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubframes, subframeSamples, signalType)
	codingQuality := float32(0.0)
	if noiseParams != nil {
		codingQuality = noiseParams.CodingQuality
	}
	minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, !e.haveEncoded)
	lpcQ12, lsfQ15, interpIdx := e.computeLPCAndNLSFWithInterp(ltpRes, numSubframes, subframeSamples, minInvGainVal)
	stage1Idx, residuals, interpIdx := e.quantizeLSFWithInterp(lsfQ15, e.bandwidth, signalType, speechActivityQ8, numSubframes, interpIdx)
	// Reconstruct quantized NLSF and build predictor coefficients for NSQ.
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	interpIdx = e.buildPredCoefQ12(predCoefQ12, lsfQ15, interpIdx)

	// Step 6: Residual energy and gain processing
	resNrg := e.computeResidualEnergies(ltpRes, predCoefQ12, interpIdx, gains, numSubframes, subframeSamples)
	processedQuantOffset := applyGainProcessing(gains, resNrg, predGainQ7, e.snrDBQ7, signalType, e.inputTiltQ15, subframeSamples)
	if signalType == typeVoiced {
		quantOffset = processedQuantOffset
	}
	if noiseParams != nil {
		noiseParams.LambdaQ10 = computeLambdaQ10(signalType, speechActivityQ8, quantOffset, noiseParams.CodingQuality, noiseParams.InputQuality)
	}

	// Step 7: Encode frame type and gains (now that quantOffset is final)
	e.encodeFrameType(vadFlag, signalType, quantOffset)
	gainsQ16 := e.encodeSubframeGains(gains, signalType, numSubframes, condCoding)

	// Step 8: Encode LSF parameters
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)

	// Step 9: Encode pitch and LTP (voiced only)
	if signalType == typeVoiced {
		e.encodePitchLagsWithParams(pitchParams, condCoding)
		e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
		// Encode LTP scale index (required for voiced frames).
		if condCoding == codeIndependently {
			e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
		}
	}

	seed := e.frameCounter & 3
	e.frameCounter++

	frameIndices := sideInfoIndices{
		signalType:       int8(signalType),
		quantOffsetType:  int8(quantOffset),
		NLSFInterpCoefQ2: int8(interpIdx),
		Seed:             int8(seed),
	}
	for i := 0; i < numSubframes && i < len(e.scratchGainInd); i++ {
		frameIndices.GainsIndices[i] = e.scratchGainInd[i]
	}
	frameIndices.NLSFIndices[0] = int8(stage1Idx)
	nlsfOrder := e.lpcOrder
	if nlsfOrder > len(residuals) {
		nlsfOrder = len(residuals)
	}
	for i := 0; i < nlsfOrder; i++ {
		frameIndices.NLSFIndices[i+1] = int8(residuals[i])
	}
	if signalType == typeVoiced {
		frameIndices.lagIndex = int16(pitchParams.lagIdx)
		frameIndices.contourIndex = int8(pitchParams.contourIdx)
		frameIndices.PERIndex = int8(perIndex)
		frameIndices.LTPScaleIndex = int8(ltpScaleIndex)
		for i := 0; i < numSubframes; i++ {
			frameIndices.LTPIndex[i] = ltpIndices[i]
		}
	}

	// Step 10: LBRR Encoding (FEC) for this frame
	e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, seed, numSubframes, subframeSamples, frameSamples, speechActivityQ8)

	// Step 7: Encode seed (LAST in indices, BEFORE pulses)
	// Per libopus: seed = frameCounter++ & 3
	e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

	// Step 11: Compute excitation using Noise Shaping Quantization (NSQ)
	// Per libopus silk_encode_pulses(), pulses are encoded for full frame_length

	// Use NSQ for proper noise-shaped quantization with adaptive parameters
	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}
	allExcitation := e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, seed, numSubframes, subframeSamples, frameSamples, e.nsqState)

	// Encode ALL pulses for the entire frame at once
	e.encodePulses(allExcitation, signalType, quantOffset)

	// Update state for next frame
	e.isPreviousFrameVoiced = (signalType == 2)
	copy(e.prevLSFQ15, lsfQ15)
	e.nFramesEncoded++
	e.MarkEncoded()

	// Patch VAD/LBRR header bits for standalone packets.
	if !useSharedEncoder {
		flags := 0
		if vadFlag {
			flags = 1
		}
		flags = (flags << 1) | e.lbrrFlag
		nBitsHeader := (e.nFramesPerPacket + 1) * 1
		e.rangeEncoder.PatchInitialBits(uint32(flags), uint(nBitsHeader))
	}

	// Finalize encoding
	if useSharedEncoder {
		// Hybrid mode: caller manages the range encoder
		// Capture range state for FinalRange() before returning
		e.lastRng = e.rangeEncoder.Range()
		return nil
	}

	// Standalone mode: get the encoded bytes and clear the range encoder
	// so the next frame creates a fresh one
	// Capture range state BEFORE Done() clears it
	e.lastRng = e.rangeEncoder.Range()
	result := e.rangeEncoder.Done()
	e.rangeEncoder = nil
	return result
}

// computeNSQExcitation computes excitation using Noise Shaping Quantization.
// This provides proper libopus-matching noise shaping for better audio quality.
// The noise shaping parameters are computed adaptively based on signal characteristics.
func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, predCoefQ12 []int16, nlsfInterpQ2 int, gainsQ16 []int32, pitchLags []int, ltpCoeffs LTPCoeffsArray, ltpScaleQ14 int, signalType, quantOffset, speechActivityQ8 int, noiseParams *NoiseShapeParams, seed, numSubframes, subframeSamples, frameSamples int, nsqState *NSQState) []int32 {
	// Convert PCM to int16 for NSQ using scratch buffer
	inputQ0 := ensureInt16Slice(&e.scratchInputQ0, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		// Match libopus FLOAT2INT16 (clamp before rounding).
		inputQ0[i] = float32ToInt16(pcm[i])
	}

	// Ensure gainsQ16 has numSubframes entries (pad with minimum gain if needed).
	if len(gainsQ16) < numSubframes {
		tmp := ensureInt32Slice(&e.scratchGainsQ16, numSubframes)
		copy(tmp, gainsQ16)
		for i := len(gainsQ16); i < numSubframes; i++ {
			tmp[i] = 1 << 16
		}
		gainsQ16 = tmp
	}

	// Prepare pitch lags (default to 0 for unvoiced) using scratch buffer
	pitchL := ensureIntSlice(&e.scratchPitchL, numSubframes)
	for i := range pitchL {
		pitchL[i] = 0 // Clear first
	}
	if pitchLags != nil {
		copy(pitchL, pitchLags)
	}

	// Compute noise shaping AR coefficients
	shapeLPCOrder := e.shapingLPCOrder
	if shapeLPCOrder <= 0 {
		shapeLPCOrder = len(lpcQ12)
	}
	if shapeLPCOrder > maxShapeLpcOrder {
		shapeLPCOrder = maxShapeLpcOrder
	}
	if shapeLPCOrder < 2 {
		shapeLPCOrder = 2
	}
	if shapeLPCOrder&1 != 0 {
		shapeLPCOrder--
	}

	var arShpQ13 []int16
	if noiseParams != nil && len(noiseParams.ARShpQ13) >= numSubframes*maxShapeLpcOrder {
		arShpQ13 = noiseParams.ARShpQ13[:numSubframes*maxShapeLpcOrder]
	} else {
		// Fallback: use LPC coefficients with bandwidth expansion
		arShpQ13 = ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
		for i := range arShpQ13 {
			arShpQ13[i] = 0
		}
		for sf := 0; sf < numSubframes; sf++ {
			for i := 0; i < shapeLPCOrder && i < len(lpcQ12); i++ {
				arShpQ13[sf*maxShapeLpcOrder+i] = int16(int32(lpcQ12[i]) * 2 * 94 / 100)
			}
		}
	}

	// LTP coefficients (Q14) derived from quantized codebook taps.
	ltpCoefQ14 := ensureInt16Slice(&e.scratchLtpCoefQ14, numSubframes*ltpOrderConst)
	for i := range ltpCoefQ14 {
		ltpCoefQ14[i] = 0 // Clear
	}
	if signalType == typeVoiced {
		for sf := 0; sf < numSubframes && sf < len(ltpCoeffs); sf++ {
			for tap := 0; tap < ltpOrderConst; tap++ {
				// Codebook taps are Q7 (128 = 1.0). Convert to Q14.
				ltpCoefQ14[sf*ltpOrderConst+tap] = int16(ltpCoeffs[sf][tap]) << 7
			}
		}
	}

	// Prediction coefficients (quantized NLSF -> LPC). Expect caller-provided buffer.
	if len(predCoefQ12) < 2*maxLPCOrder {
		predCoefQ12 = ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
		for i := range predCoefQ12 {
			predCoefQ12[i] = 0
		}
		for i := 0; i < len(lpcQ12) && i < maxLPCOrder; i++ {
			predCoefQ12[i] = lpcQ12[i]
			predCoefQ12[maxLPCOrder+i] = lpcQ12[i]
		}
		nlsfInterpQ2 = 4
	}

	// Compute adaptive noise shaping parameters using libopus-matching algorithm
	// Reference: libopus silk/float/noise_shape_analysis_FLP.c
	if noiseParams == nil {
		if e.noiseShapeState == nil {
			e.noiseShapeState = NewNoiseShapeState()
		}

		// Get sample rate in kHz for noise shaping computation
		fsKHz := e.sampleRate / 1000
		if fsKHz < 8 {
			fsKHz = 8
		}

		// Compute adaptive noise shaping parameters
		inputQualityBandsQ15 := [4]int{-1, -1, -1, -1}
		if e.speechActivitySet {
			inputQualityBandsQ15 = e.inputQualityBandsQ15
		}
		noiseParams = e.noiseShapeState.ComputeNoiseShapeParams(
			signalType,
			speechActivityQ8,
			e.ltpCorr,
			pitchLags,
			float64(e.snrDBQ7)/128.0,
			quantOffset,
			inputQualityBandsQ15,
			numSubframes,
			fsKHz,
		)
	}

	// Use the adaptive parameters
	harmShapeGainQ14 := ensureIntSlice(&e.scratchHarmShapeGainQ14, numSubframes)
	tiltQ14 := ensureIntSlice(&e.scratchTiltQ14, numSubframes)
	lfShpQ14 := ensureInt32Slice(&e.scratchLfShpQ14, numSubframes)
	copy(harmShapeGainQ14, noiseParams.HarmShapeGainQ14)
	copy(tiltQ14, noiseParams.TiltQ14)
	copy(lfShpQ14, noiseParams.LFShpQ14)

	// Lambda (rate-distortion tradeoff) from adaptive computation
	lambdaQ10 := noiseParams.LambdaQ10

	if signalType != typeVoiced {
		ltpScaleQ14 = 0
	}

	// Set up NSQ parameters
	params := &NSQParams{
		SignalType:       signalType,
		QuantOffsetType:  quantOffset,
		PredCoefQ12:      predCoefQ12,
		NLSFInterpCoefQ2: nlsfInterpQ2,
		LTPCoefQ14:       ltpCoefQ14,
		ARShpQ13:         arShpQ13,
		HarmShapeGainQ14: harmShapeGainQ14,
		TiltQ14:          tiltQ14,
		LFShpQ14:         lfShpQ14,
		GainsQ16:         gainsQ16,
		PitchL:           pitchL,
		LambdaQ10:        lambdaQ10,
		LTPScaleQ14:      int(ltpScaleQ14),
		FrameLength:      frameSamples,
		SubfrLength:      subframeSamples,
		NbSubfr:          numSubframes,
		LTPMemLength:     ltpMemLength,
		PredLPCOrder:     len(lpcQ12),
		ShapeLPCOrder:    shapeLPCOrder,
		Seed:             seed,
	}

	// Run NSQ
	state := nsqState
	if state == nil {
		state = e.nsqState
	}
	pulses, _ := NoiseShapeQuantize(state, inputQ0, params)

	// Convert pulses to int32 for encoding using scratch buffer
	excitation := ensureInt32Slice(&e.scratchExcitation, frameSamples)
	for i := 0; i < len(pulses) && i < frameSamples; i++ {
		excitation[i] = int32(pulses[i])
	}

	return excitation
}

// EncodePacketWithFEC encodes a complete SILK packet with FEC support.
// This function encodes VAD flags, LBRR (FEC) data from the previous packet,
// and then the main frame data.
//
// The packet structure is:
//  1. VAD flags (1 bit per frame)
//  2. LBRR flag (1 bit indicating if any LBRR data follows)
//  3. LBRR flags (only if LBRR flag is 1 and nFramesPerPacket > 1)
//  4. LBRR indices and pulses for each frame with LBRR
//  5. Main frame encoding for each frame
//
// Reference: libopus silk/enc_API.c silk_Encode lines 355-405
func (e *Encoder) EncodePacketWithFEC(pcm []float32, lookahead []float32, vadFlags []bool) []byte {
	// Reset per-packet state for standalone encoding
	e.ResetPacketState()

	// Determine frames per packet based on input size
	config := GetBandwidthConfig(e.bandwidth)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame baseline
	if len(pcm) < frameSamples {
		frameSamples = len(pcm) // 10ms frame or shorter input
	}
	nFrames := len(pcm) / frameSamples
	if nFrames < 1 {
		nFrames = 1
	}
	if nFrames > maxFramesPerPacket {
		nFrames = maxFramesPerPacket
	}
	e.nFramesPerPacket = nFrames
	e.nFramesEncoded = 0

	// Create range encoder (reuse scratch buffer)
	bufSize := len(pcm)/2 + 100
	if bufSize < 150 {
		bufSize = 150
	}
	if bufSize < maxSilkPacketBytes {
		bufSize = maxSilkPacketBytes
	}
	output := ensureByteSlice(&e.scratchOutput, bufSize)
	e.scratchRangeEncoder.Init(output)
	e.rangeEncoder = &e.scratchRangeEncoder

	// Step 1: Reserve header bits and encode any LBRR data from previous packet.
	// This mirrors libopus: placeholder bits are patched after frame encoding.
	e.encodeLBRRData(e.rangeEncoder, 1, true) // nChannels = 1 for mono

	// Step 2: Encode each frame
	var vadFlagsLocal [maxFramesPerPacket]bool
	for i := 0; i < nFrames; i++ {
		startSample := i * frameSamples
		endSample := startSample + frameSamples
		if endSample > len(pcm) {
			endSample = len(pcm)
		}
		framePCM := pcm[startSample:endSample]

		 vadFlag := true
				if vadFlags != nil && i < len(vadFlags) {
					vadFlag = vadFlags[i]
				} else {
					// Fallback VAD
					vadFlag = true // Assume active if flags not provided
				}
				vadFlagsLocal[i] = vadFlag
		
				// Determine lookahead for this frame
				var frameLookahead []float32
				if i < nFrames-1 {
					// Lookahead is the next frame in the packet
					nextStart := (i + 1) * frameSamples
					// We provide the full next frame as lookahead buffer
					if nextStart < len(pcm) {
						frameLookahead = pcm[nextStart:]
						// Limit lookahead size if needed? No, computePitchResidual handles it.
						if len(frameLookahead) > frameSamples {
							frameLookahead = frameLookahead[:frameSamples]
						}
					}
				} else {
					// Last frame: use external lookahead
					frameLookahead = lookahead
				}
		
				// Encode the frame (updates state)
				e.encodeFrameInternal(framePCM, frameLookahead, vadFlag)
				e.nFramesEncoded++
			}
	// Patch VAD/LBRR header bits for this packet.
	flags := 0
	for i := 0; i < nFrames; i++ {
		flags <<= 1
		if vadFlagsLocal[i] {
			flags |= 1
		}
	}
	flags = (flags << 1) | e.lbrrFlag
	nBitsHeader := (e.nFramesPerPacket + 1) * 1
	e.rangeEncoder.PatchInitialBits(uint32(flags), uint(nBitsHeader))

	// Finalize and return
	e.lastRng = e.rangeEncoder.Range()
	result := e.rangeEncoder.Done()
	e.rangeEncoder = nil

	// Reset frame counter for next packet
	e.nFramesEncoded = 0

	return result
}

// encodeFrameInternal encodes a single frame within a packet.
// This is used by EncodePacketWithFEC and doesn't manage the range encoder.
func (e *Encoder) encodeFrameInternal(pcm []float32, lookahead []float32, vadFlag bool) {
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples
	numSubframes := len(pcm) / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
		subframeSamples = len(pcm)
	}
	if numSubframes > maxNbSubfr {
		numSubframes = maxNbSubfr
	}
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	// Quantize input to int16 precision to match libopus float API behavior.
	pcm = e.quantizePCMToInt16(pcm)

	// Step 1: Classify frame (VAD)
	var signalType, quantOffset int
	var speechActivityQ8 int
	if vadFlag {
		signalType, quantOffset = e.classifyFrame(pcm)
		speechActivityQ8 = 200
	} else {
		signalType, quantOffset = 0, 0
		speechActivityQ8 = 50
	}

	// Step 1.1: Update noise shaping lookahead buffer and select delayed frame
	framePCM := e.updateShapeBuffer(pcm, frameSamples)

	// Step 1.2: Update pitch analysis buffer with delayed frame
	pitchBufFrameLen := len(framePCM)
	if pitchBufFrameLen > 0 && len(e.pitchAnalysisBuf) > 0 {
		if len(e.pitchAnalysisBuf) > pitchBufFrameLen {
			copy(e.pitchAnalysisBuf, e.pitchAnalysisBuf[pitchBufFrameLen:])
		}
		start := len(e.pitchAnalysisBuf) - pitchBufFrameLen
		if start < 0 {
			start = 0
			pitchBufFrameLen = len(e.pitchAnalysisBuf)
		}
		copy(e.pitchAnalysisBuf[start:], framePCM[:pitchBufFrameLen])
	}

	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}

	// Step 2: Pitch detection and LTP (voiced only)
	var pitchLags []int
	var lagIndex, contourIndex int
	var pitchParams pitchEncodeParams
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	var ltpIndices [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)
	residual, residual32, resStart, _ := e.computePitchResidual(numSubframes, lookahead)
	if signalType == 2 {
		// Use pitch residual for more accurate pitch detection (libopus parity).
		searchThres1 := float64(e.pitchEstimationThresholdQ16) / 65536.0
		prevSignalType := 0
		if e.isPreviousFrameVoiced {
			prevSignalType = 2
		}
		thrhld := 0.6 - 0.004*float64(e.pitchEstimationLPCOrder) -
			0.1*float64(speechActivityQ8)/256.0 -
			0.15*float64(prevSignalType>>1)
		if thrhld < 0 {
			thrhld = 0
		} else if thrhld > 1 {
			thrhld = 1
		}
		pitchLags, lagIndex, contourIndex = e.detectPitch(residual32, numSubframes, searchThres1, thrhld)
		pitchParams = e.preparePitchLags(pitchLags, numSubframes, lagIndex, contourIndex)

		// Update LTP correlation for noise shaping (from pitch detection)
		e.ltpCorr = float32(e.pitchState.ltpCorr)
		if e.ltpCorr > 1.0 {
			e.ltpCorr = 1.0
		}

		ltpCoeffs, ltpIndices, perIndex, predGainQ7 = e.analyzeLTPQuantized(residual, resStart, pitchLags, numSubframes, subframeSamples)

		ltpScaleIndex = e.computeLTPScaleIndex(predGainQ7, condCoding)
	} else {
		// Reset LTP correlation for unvoiced frames
		e.ltpCorr = 0
		e.sumLogGainQ7 = 0
	}

	// Step 3: Noise shaping analysis (sparseness quant offset, gains, shaping AR)
	noiseParams, gains, quantOffset := e.noiseShapeAnalysis(
		framePCM,
		residual,
		resStart,
		signalType,
		speechActivityQ8,
		e.lastLPCGain,
		pitchLags,
		quantOffset,
		numSubframes,
		subframeSamples,
		lookahead,
	)

	// Step 4: Build LTP residual and compute LPC from it
	fsKHz := config.SampleRate / 1000
	ltpMemSamples := ltpMemLengthMs * fsKHz
	pitchBuf := e.inputBuffer
	frameStart := ltpMemSamples
	if frameStart+frameSamples > len(pitchBuf) {
		if len(pitchBuf) > frameSamples {
			frameStart = len(pitchBuf) - frameSamples
		} else {
			frameStart = 0
		}
	}
	ltpRes := e.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, numSubframes, subframeSamples, signalType)
	codingQuality := float32(0.0)
	if noiseParams != nil {
		codingQuality = noiseParams.CodingQuality
	}
	minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, !e.haveEncoded)
	lpcQ12, lsfQ15, interpIdx := e.computeLPCAndNLSFWithInterp(ltpRes, numSubframes, subframeSamples, minInvGainVal)
	stage1Idx, residuals, interpIdx := e.quantizeLSFWithInterp(lsfQ15, e.bandwidth, signalType, speechActivityQ8, numSubframes, interpIdx)
	// Reconstruct quantized NLSF and build predictor coefficients for NSQ.
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	interpIdx = e.buildPredCoefQ12(predCoefQ12, lsfQ15, interpIdx)

	// Step 6: Residual energy and gain processing
	resNrg := e.computeResidualEnergies(ltpRes, predCoefQ12, interpIdx, gains, numSubframes, subframeSamples)
	processedQuantOffset := applyGainProcessing(gains, resNrg, predGainQ7, e.snrDBQ7, signalType, e.inputTiltQ15, subframeSamples)
	if signalType == typeVoiced {
		quantOffset = processedQuantOffset
	}
	if noiseParams != nil {
		noiseParams.LambdaQ10 = computeLambdaQ10(signalType, speechActivityQ8, quantOffset, noiseParams.CodingQuality, noiseParams.InputQuality)
	}

	// Step 7: Encode frame type and gains (now that quantOffset is final)
	e.encodeFrameType(vadFlag, signalType, quantOffset)
	gainsQ16 := e.encodeSubframeGains(gains, signalType, numSubframes, condCoding)

	// Step 8: Encode LSF parameters
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)

	// Step 9: Encode pitch and LTP (voiced only)
	if signalType == typeVoiced {
		e.encodePitchLagsWithParams(pitchParams, condCoding)
		e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
		// Encode LTP scale index (required for voiced frames).
		if condCoding == codeIndependently {
			e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
		}
	}

	seed := e.frameCounter & 3
	e.frameCounter++

	frameIndices := sideInfoIndices{
		signalType:       int8(signalType),
		quantOffsetType:  int8(quantOffset),
		NLSFInterpCoefQ2: int8(interpIdx),
		Seed:             int8(seed),
	}
	for i := 0; i < numSubframes && i < len(e.scratchGainInd); i++ {
		frameIndices.GainsIndices[i] = e.scratchGainInd[i]
	}
	frameIndices.NLSFIndices[0] = int8(stage1Idx)
	nlsfOrder := e.lpcOrder
	if nlsfOrder > len(residuals) {
		nlsfOrder = len(residuals)
	}
	for i := 0; i < nlsfOrder; i++ {
		frameIndices.NLSFIndices[i+1] = int8(residuals[i])
	}
	if signalType == typeVoiced {
		frameIndices.lagIndex = int16(pitchParams.lagIdx)
		frameIndices.contourIndex = int8(pitchParams.contourIdx)
		frameIndices.PERIndex = int8(perIndex)
		frameIndices.LTPScaleIndex = int8(ltpScaleIndex)
		for i := 0; i < numSubframes; i++ {
			frameIndices.LTPIndex[i] = ltpIndices[i]
		}
	}

	// Step 10: LBRR Encoding (FEC) for this frame
	e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, seed, numSubframes, subframeSamples, frameSamples, speechActivityQ8)

	// Step 7: Encode seed
	e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

	// Step 11: Compute and encode excitation

	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}
	allExcitation := e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, seed, numSubframes, subframeSamples, frameSamples, e.nsqState)
	e.encodePulses(allExcitation, signalType, quantOffset)

	// Update state
	e.isPreviousFrameVoiced = (signalType == 2)
	copy(e.prevLSFQ15, lsfQ15)
	e.nFramesEncoded++
	e.MarkEncoded()
}

// encodeFrameType encodes VAD flag, signal type, and quantization offset.
// Uses ICDFFrameTypeVADActive from tables.go
func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	if !vadFlag {
		// Inactive frame - minimal encoding
		// For inactive frames, signal type is 0, use different handling
		e.rangeEncoder.EncodeICDF16(0, ICDFFrameTypeVADActive, 8)
		return
	}

	// Active frame: encode signal type and quant offset
	// idx = (signalType-1)*2 + quantOffset for signalType 1,2
	// signalType 0 (inactive) handled above
	if signalType < 1 {
		signalType = 1 // Default to unvoiced if inactive with VAD
	}
	idx := (signalType-1)*2 + quantOffset
	if idx < 0 {
		idx = 0
	}
	if idx > 3 {
		idx = 3
	}
	e.rangeEncoder.EncodeICDF16(idx, ICDFFrameTypeVADActive, 8)
}

// quantizePCMToInt16 rounds input samples to int16 precision and returns
// the quantized float32 values (scaled back to [-1, 1]).
func (e *Encoder) quantizePCMToInt16(pcm []float32) []float32 {
	if len(pcm) == 0 {
		return pcm
	}
	quantized := ensureFloat32Slice(&e.scratchPCMQuant, len(pcm))
	scale := float32(silkSampleScale)
	invScale := float32(1.0 / silkSampleScale)
	for i, v := range pcm {
		quantized[i] = float32(floatToInt16Round(v*scale)) * invScale
	}
	return quantized
}

// updateShapeBuffer updates the noise shaping lookahead buffer (x_buf) and
// returns the delayed frame slice to encode.
func (e *Encoder) updateShapeBuffer(pcm []float32, frameSamples int) []float32 {
	if frameSamples <= 0 {
		return pcm
	}
	fsKHz := e.sampleRate / 1000
	if fsKHz < 1 {
		fsKHz = 1
	}
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	needed := keep + frameSamples

	shapeBuf := e.inputBuffer
	if len(shapeBuf) < needed {
		shapeBuf = make([]float32, needed)
		e.inputBuffer = shapeBuf
	}

	if keep > 0 && frameSamples > 0 && frameSamples+keep <= len(shapeBuf) {
		copy(shapeBuf[:keep], shapeBuf[frameSamples:frameSamples+keep])
	}

	insertOffset := keep
	if insertOffset+frameSamples > len(shapeBuf) {
		if insertOffset >= len(shapeBuf) {
			return pcm
		}
		frameSamples = len(shapeBuf) - insertOffset
	}
	insert := shapeBuf[insertOffset : insertOffset+frameSamples]
	n := copy(insert, pcm)
	for i := n; i < frameSamples; i++ {
		insert[i] = 0
	}

	start := ltpMemSamples
	if start+frameSamples > len(shapeBuf) {
		if start >= len(shapeBuf) {
			return pcm
		}
		frameSamples = len(shapeBuf) - start
	}
	return shapeBuf[start : start+frameSamples]
}
