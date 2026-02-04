package silk

// EncodeFrame encodes a complete SILK frame to bitstream.
// Returns encoded bytes. If a range encoder was pre-set via SetRangeEncoder(),
// it will be used (for hybrid mode) and nil is returned since the caller
// manages the shared encoder.
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
		// Total target bits for packet
		payloadSizeMs := (frameSamples * 1000) / config.SampleRate
		nBits := (e.targetRateBps * payloadSizeMs) / 1000 * 8

		// Divide by number of uncoded frames left in packet
		nBits /= e.nFramesPerPacket

		// Convert to bits/second
		targetRate := 0
		if payloadSizeMs == 10 {
			targetRate = nBits * 100
		} else {
			targetRate = nBits * 50
		}

		// Bit reservoir logic from libopus silk/enc_API.c:
		if e.nBitsExceeded > 0 {
			targetRate -= (e.nBitsExceeded * 1000) / 500
		}

		// Compare actual vs target bits so far in this packet (bitsBalance)
		if e.nFramesEncoded > 0 && e.rangeEncoder != nil {
			bitsBalance := e.rangeEncoder.Tell() - nBits*e.nFramesEncoded
			targetRate -= (bitsBalance * 1000) / 500
		}

		// Never exceed input bitrate, and maintain minimum for quality.
		if targetRate > e.targetRateBps {
			targetRate = e.targetRateBps
		}
		if targetRate < 5000 {
			targetRate = 5000
		}

		e.controlSNR(targetRate, numSubframes)
	}

	// Quantize input to int16 precision to match libopus float API behavior.
	pcm = e.quantizePCMToInt16(pcm)

	// Check if we have a pre-set range encoder (hybrid mode)
	useSharedEncoder := e.rangeEncoder != nil
	if !useSharedEncoder {
		e.ResetPacketState()
	}

	if !useSharedEncoder {
		e.rangeEncoder = nil // Safety clear
		bufSize := len(pcm) / 3
		if bufSize < 80 {
			bufSize = 80
		}
		if e.lbrrEnabled {
			bufSize += 50
		}
		if bufSize < maxSilkPacketBytes {
			bufSize = maxSilkPacketBytes
		}
		output := ensureByteSlice(&e.scratchOutput, bufSize)
		e.scratchRangeEncoder.Init(output)
		e.rangeEncoder = &e.scratchRangeEncoder
		e.nFramesPerPacket = 1
		e.encodeLBRRData(e.rangeEncoder, 1, true)
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

	// Step 2: Pitch detection and LTP
	var pitchLags []int
	var lagIndex, contourIndex int
	var pitchParams pitchEncodeParams
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	var ltpIndices [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)
	residual, residual32, resStart, _ := e.computePitchResidual(numSubframes)
	if signalType != typeNoVoiceActivity {
		searchThres1 := float64(e.pitchEstimationThresholdQ16) / 65536.0
		prevSignalType := 0
		if e.isPreviousFrameVoiced {
			prevSignalType = 2
		}
		thrhld := 0.6 - 0.004*float64(e.pitchEstimationLPCOrder) -
			0.1*float64(speechActivityQ8)/256.0 -
			0.15*float64(prevSignalType>>1) -
			0.1*float64(e.inputTiltQ15)/32768.0
		rawThrhld := thrhld
		if thrhld < 0 {
			thrhld = 0
		} else if thrhld > 1 {
			thrhld = 1
		}
		if e.trace != nil && e.trace.Pitch != nil {
			tr := e.trace.Pitch
			tr.SearchThres1 = searchThres1
			tr.Thrhld = rawThrhld
			tr.ThrhldClamped = thrhld
			tr.PitchEstThresholdQ16 = e.pitchEstimationThresholdQ16
			tr.PrevLag = e.pitchState.prevLag
			tr.PrevSignal = prevSignalType
			tr.SignalType = signalType
			tr.SpeechQ8 = speechActivityQ8
			tr.InputTiltQ15 = e.inputTiltQ15
			tr.LPCOrder = e.pitchEstimationLPCOrder
			tr.Complexity = e.pitchEstimationComplexity
			tr.FirstFrameAfterReset = !e.haveEncoded
		}
		if !e.haveEncoded {
			// Match libopus: skip pitch analysis on the first frame after reset.
			pitchLags = make([]int, numSubframes)
			lagIndex = 0
			contourIndex = 0
			e.ltpCorr = 0
			signalType = typeUnvoiced
			e.sumLogGainQ7 = 0
			if e.trace != nil && e.trace.Pitch != nil && e.trace.Pitch.CapturePitchLags {
				tr := e.trace.Pitch
				tr.PitchLags = append(tr.PitchLags[:0], pitchLags...)
				tr.LagIndex = lagIndex
				tr.Contour = contourIndex
				tr.LTPCorr = 0
			}
		} else {
			pitchLags, lagIndex, contourIndex = e.detectPitch(residual32, numSubframes, searchThres1, thrhld)
			if e.trace != nil && e.trace.Pitch != nil && e.trace.Pitch.CapturePitchLags {
				tr := e.trace.Pitch
				tr.PitchLags = append(tr.PitchLags[:0], pitchLags...)
				tr.LagIndex = lagIndex
				tr.Contour = contourIndex
				tr.LTPCorr = float32(e.pitchState.ltpCorr)
			}
			e.ltpCorr = float32(e.pitchState.ltpCorr)
			if e.ltpCorr > 1.0 {
				e.ltpCorr = 1.0
			}
			if e.ltpCorr > 0 {
				signalType = typeVoiced
				pitchParams = e.preparePitchLags(pitchLags, numSubframes, lagIndex, contourIndex)
				if e.trace != nil && e.trace.LTP != nil {
					tr := e.trace.LTP
					tr.PitchLags = append(tr.PitchLags[:0], pitchLags...)
					tr.SumLogGainQ7In = e.sumLogGainQ7
				}
				ltpCoeffs, ltpIndices, perIndex, predGainQ7 = e.analyzeLTPQuantized(residual, resStart, pitchLags, numSubframes, subframeSamples)
				if e.trace != nil && e.trace.LTP != nil {
					tr := e.trace.LTP
					tr.PERIndex = perIndex
					tr.PredGainQ7 = predGainQ7
					tr.SumLogGainQ7Out = e.sumLogGainQ7
					tr.LTPIndex = append(tr.LTPIndex[:0], ltpIndices[:numSubframes]...)
					tr.BQ14 = tr.BQ14[:0]
					for sf := 0; sf < numSubframes; sf++ {
						for tap := 0; tap < ltpOrderConst; tap++ {
							tr.BQ14 = append(tr.BQ14, int16(ltpCoeffs[sf][tap])<<7)
						}
					}
				}
				ltpScaleIndex = e.computeLTPScaleIndex(predGainQ7, condCoding)
			} else {
				signalType = typeUnvoiced
				e.sumLogGainQ7 = 0
			}
		}
	} else {
		e.ltpCorr = 0
		e.sumLogGainQ7 = 0
	}

	// Step 3: Noise shaping analysis
	noiseParams, gains, quantOffset := e.noiseShapeAnalysis(framePCM, residual, resStart, signalType, speechActivityQ8, e.lastLPCGain, pitchLags, quantOffset, numSubframes, subframeSamples)

	// Step 4: Build LTP residual and compute LPC
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
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	if e.trace != nil && e.trace.NLSF != nil {
		tr := e.trace.NLSF
		tr.Stage1Idx = stage1Idx
		tr.Residuals = append(tr.Residuals[:0], residuals...)
		tr.QuantizedNLSFQ15 = append(tr.QuantizedNLSFQ15[:0], lsfQ15...)
		tr.SignalType = signalType
		tr.SpeechQ8 = speechActivityQ8
		tr.Bandwidth = e.bandwidth
		tr.NLSFSurvivors = e.nlsfSurvivors
		tr.InterpIdx = interpIdx
	}
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

	// Step 7: Encode frame type
	e.encodeFrameType(vadFlag, signalType, quantOffset)

	// SINGLE PASS ENCODING

	// Prepare gains for quantization
	gainsQ16 := make([]int32, numSubframes)
	for i := range gains {
		gainsQ16[i] = int32(gains[i] * 65536.0)
	}

	e.previousGainIndex = int32(e.lbrrPrevLastGainIdx)
	gainIndices := ensureInt8Slice(&e.scratchGainInd, numSubframes)
	newPrevInd := silkGainsQuantInto(gainIndices, gainsQ16, int8(e.previousGainIndex), condCoding == codeConditionally, numSubframes)

	// Encode gain indices to bitstream
	if condCoding == codeIndependently {
		e.encodeAbsoluteGainIndex(int(gainIndices[0]), signalType)
	} else {
		e.rangeEncoder.EncodeICDF(int(gainIndices[0]), silk_delta_gain_iCDF, 8)
	}
	for i := 1; i < numSubframes; i++ {
		e.rangeEncoder.EncodeICDF(int(gainIndices[i]), silk_delta_gain_iCDF, 8)
	}

	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)
	if signalType == typeVoiced {
		e.encodePitchLagsWithParams(pitchParams, condCoding)
		e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
		if condCoding == codeIndependently {
			e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
		}
	}
	seed := e.frameCounter & 3
	e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

	// Prepare LBRR data for the next packet if FEC is enabled.
	if e.lbrrEnabled {
		var frameIndices sideInfoIndices
		for i := 0; i < numSubframes && i < len(gainIndices); i++ {
			frameIndices.GainsIndices[i] = gainIndices[i]
		}
		for i := 0; i < numSubframes && i < len(ltpIndices); i++ {
			frameIndices.LTPIndex[i] = ltpIndices[i]
		}
		frameIndices.signalType = int8(signalType)
		frameIndices.quantOffsetType = int8(quantOffset)
		frameIndices.NLSFInterpCoefQ2 = int8(interpIdx)
		frameIndices.PERIndex = int8(perIndex)
		frameIndices.LTPScaleIndex = int8(ltpScaleIndex)
		frameIndices.Seed = int8(seed)
		frameIndices.lagIndex = int16(pitchParams.lagIdx)
		frameIndices.contourIndex = int8(pitchParams.contourIdx)
		if stage1Idx < 0 {
			frameIndices.NLSFIndices[0] = 0
		} else {
			frameIndices.NLSFIndices[0] = int8(stage1Idx)
		}
		for i := 0; i < len(residuals) && i < maxLPCOrder; i++ {
			frameIndices.NLSFIndices[i+1] = int8(residuals[i])
		}
		e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, seed, numSubframes, subframeSamples, frameSamples, speechActivityQ8)
	}

	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}

	// CRITICAL: Use the quantized gainsQ16 (modified by silkGainsQuantInto) for NSQ!
	allExcitation := e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, seed, numSubframes, subframeSamples, frameSamples, e.nsqState)

	e.encodePulses(allExcitation, signalType, quantOffset)

	e.previousGainIndex = int32(newPrevInd)
	e.previousLogGain = int32(newPrevInd)
	e.frameCounter++
	e.isPreviousFrameVoiced = (signalType == typeVoiced)
	copy(e.prevLSFQ15, lsfQ15)

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

	e.nFramesEncoded++
	e.MarkEncoded()
	e.lastRng = e.rangeEncoder.Range()

	if useSharedEncoder {
		return nil
	}

	// Patch SILK header bits (VAD + LBRR) at start of bitstream.
	flags := uint32(0)
	if vadFlag {
		flags = 1
	}
	flags = (flags << 1) | uint32(e.lbrrFlag&1)
	e.rangeEncoder.PatchInitialBits(flags, 2)

	raw := e.rangeEncoder.Done()

	result := make([]byte, len(raw))
	copy(result, raw)
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, predCoefQ12 []int16, nlsfInterpQ2 int, gainsQ16 []int32, pitchLags []int, ltpCoeffs LTPCoeffsArray, ltpScaleQ14 int, signalType, quantOffset, speechActivityQ8 int, noiseParams *NoiseShapeParams, seed, numSubframes, subframeSamples, frameSamples int, nsqState *NSQState) []int32 {
	inputQ0 := ensureInt16Slice(&e.scratchInputQ0, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		inputQ0[i] = float32ToInt16(pcm[i])
	}
	if len(gainsQ16) < numSubframes {
		tmp := ensureInt32Slice(&e.scratchGainsQ16, numSubframes)
		copy(tmp, gainsQ16)
		for i := len(gainsQ16); i < numSubframes; i++ {
			tmp[i] = 1 << 16
		}
		gainsQ16 = tmp
	}
	pitchL := ensureIntSlice(&e.scratchPitchL, numSubframes)
	for i := range pitchL {
		pitchL[i] = 0
	}
	if pitchLags != nil {
		copy(pitchL, pitchLags)
	}
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
	ltpCoefQ14 := ensureInt16Slice(&e.scratchLtpCoefQ14, numSubframes*ltpOrderConst)
	for i := range ltpCoefQ14 {
		ltpCoefQ14[i] = 0
	}
	if signalType == typeVoiced {
		for sf := 0; sf < numSubframes && sf < len(ltpCoeffs); sf++ {
			for tap := 0; tap < ltpOrderConst; tap++ {
				ltpCoefQ14[sf*ltpOrderConst+tap] = int16(ltpCoeffs[sf][tap]) << 7
			}
		}
	}
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
	if noiseParams == nil {
		if e.noiseShapeState == nil {
			e.noiseShapeState = NewNoiseShapeState()
		}
		fsKHz := e.sampleRate / 1000
		if fsKHz < 8 {
			fsKHz = 8
		}
		inputQualityBandsQ15 := [4]int{-1, -1, -1, -1}
		if e.speechActivitySet {
			inputQualityBandsQ15 = e.inputQualityBandsQ15
		}
		noiseParams = e.noiseShapeState.ComputeNoiseShapeParams(signalType, speechActivityQ8, e.ltpCorr, pitchLags, float64(e.snrDBQ7)/128.0, quantOffset, inputQualityBandsQ15, numSubframes, fsKHz)
	}
	harmShapeGainQ14 := ensureIntSlice(&e.scratchHarmShapeGainQ14, numSubframes)
	tiltQ14 := ensureIntSlice(&e.scratchTiltQ14, numSubframes)
	lfShpQ14 := ensureInt32Slice(&e.scratchLfShpQ14, numSubframes)
	copy(harmShapeGainQ14, noiseParams.HarmShapeGainQ14)
	copy(tiltQ14, noiseParams.TiltQ14)
	copy(lfShpQ14, noiseParams.LFShpQ14)
	lambdaQ10 := noiseParams.LambdaQ10
	if signalType != typeVoiced {
		ltpScaleQ14 = 0
	}
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
	state := nsqState
	if state == nil {
		state = e.nsqState
	}
	pulses, _ := NoiseShapeQuantize(state, inputQ0, params)
	excitation := ensureInt32Slice(&e.scratchExcitation, frameSamples)
	for i := 0; i < len(pulses) && i < frameSamples; i++ {
		excitation[i] = int32(pulses[i])
	}
	return excitation
}

func (e *Encoder) EncodePacketWithFEC(pcm []float32, lookahead []float32, vadFlags []bool) []byte {
	e.ResetPacketState()
	config := GetBandwidthConfig(e.bandwidth)
	frameSamples := config.SampleRate * 20 / 1000
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	nFrames := len(pcm) / frameSamples
	if nFrames < 1 {
		nFrames = 1
	}
	if nFrames > maxFramesPerPacket {
		nFrames = maxFramesPerPacket
	}
	e.nFramesPerPacket = nFrames
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
	e.encodeLBRRData(e.rangeEncoder, 1, true)
	var vadUsed [maxFramesPerPacket]int
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
		}
		if vadFlag {
			vadUsed[i] = 1
		} else {
			vadUsed[i] = 0
		}
		var frameLookahead []float32
		if i < nFrames-1 {
			nextStart := (i + 1) * frameSamples
			if nextStart < len(pcm) {
				frameLookahead = pcm[nextStart:]
				if len(frameLookahead) > frameSamples {
					frameLookahead = frameLookahead[:frameSamples]
				}
			}
		} else {
			frameLookahead = lookahead
		}
		_ = e.EncodeFrame(framePCM, frameLookahead, vadFlag)
	}
	// Patch SILK header bits (VAD + LBRR) at start of bitstream.
	flags := uint32(0)
	for i := 0; i < nFrames; i++ {
		flags <<= 1
		flags |= uint32(vadUsed[i] & 1)
	}
	flags = (flags << 1) | uint32(e.lbrrFlag&1)
	e.rangeEncoder.PatchInitialBits(flags, uint(nFrames+1))
	e.lastRng = e.rangeEncoder.Range()
	result := e.rangeEncoder.Done()
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	typeOffset := 2*signalType + quantOffset
	if vadFlag {
		if typeOffset < 2 {
			typeOffset = 2
		}
		if typeOffset > 5 {
			typeOffset = 5
		}
		e.rangeEncoder.EncodeICDF16(typeOffset-2, ICDFFrameTypeVADActive, 8)
		return
	}
	// VAD inactive uses a dedicated 2-symbol table (typeOffset 0 or 1).
	if typeOffset < 0 {
		typeOffset = 0
	}
	if typeOffset > 1 {
		typeOffset = 1
	}
	e.rangeEncoder.EncodeICDF16(typeOffset, ICDFFrameTypeVADInactive, 8)
}

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
