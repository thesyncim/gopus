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
		e.nFramesEncoded = 0
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

	// Rate control loop variables
	maxBits := e.maxBits
	if maxBits <= 0 {
		maxBits = (e.targetRateBps * 20) / 1000 * 8
		if numSubframes == 2 {
			maxBits = (e.targetRateBps * 10) / 1000 * 8
		}
	}
	if maxBits < 80 {
		maxBits = 80
	}
	bitsMargin := 5
	if !e.useVBR {
		bitsMargin = maxBits / 4
	}

	// Step 1.1: Update noise shaping lookahead buffer and select delayed frame
	framePCM := e.updateShapeBuffer(pcm, frameSamples)

	if !useSharedEncoder {
		e.encodeLBRRData(e.rangeEncoder, 1, true)
	}

	// Step 2: Pitch detection and LTP
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

	// Step 3: Noise shaping analysis
	noiseParams, gains, quantOffset := e.noiseShapeAnalysis(framePCM, residual, resStart, signalType, speechActivityQ8, e.lastLPCGain, pitchLags, quantOffset, numSubframes, subframeSamples, lookahead)

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

	// Rate control loop
	const maxIter = 6
	gainMultQ8 := 256
	foundLower := false
	foundUpper := false
	nBitsLower := 0
	nBitsUpper := 0
	gainMultLower := 0
	gainMultUpper := 0
	var gainsIDLower int32 = -1

	e.rangeEncoder.SaveStateInto(&e.scratchRangeEncoderCopy)
	if e.scratchNSQCopy == nil {
		e.scratchNSQCopy = e.nsqState.Clone()
	} else {
		e.scratchNSQCopy.RestoreFrom(e.nsqState)
	}
	seedCopy := e.frameCounter
	ecPrevLagIndexCopy := e.ecPrevLagIndex

	var finalGainsQ16 []int32
	var finalGainsID int32
	var newPrevInd int8

	unqGainsQ16 := make([]int32, numSubframes)
	for i := 0; i < numSubframes; i++ {
		unqGainsQ16[i] = int32(gains[i] * 65536.0)
	}

	gainLock := make([]bool, numSubframes)
	bestGainMult := make([]int, numSubframes)
	bestSum := make([]int, numSubframes)

	for iter := 0; iter < maxIter; iter++ {
		if iter > 0 {
			e.rangeEncoder.RestoreState(&e.scratchRangeEncoderCopy)
			e.nsqState.RestoreFrom(e.scratchNSQCopy)
			e.frameCounter = seedCopy
			e.ecPrevLagIndex = ecPrevLagIndexCopy
		}

		pGainsQ16 := ensureInt32Slice(&e.scratchGainsQ16Enc, numSubframes)
		for i := 0; i < numSubframes; i++ {
			mult := gainMultQ8
			if gainLock[i] {
				mult = bestGainMult[i]
			}
			pGainsQ16[i] = int32((int64(unqGainsQ16[i]) * int64(mult)) >> 8)
			if pGainsQ16[i] < (1 << 16) {
				pGainsQ16[i] = 1 << 16
			}
		}

		e.previousGainIndex = int32(e.lbrrPrevLastGainIdx)
		gainIndices := ensureInt8Slice(&e.scratchGainInd, numSubframes)
		conditional := condCoding == codeConditionally
		newPrevInd = silkGainsQuantInto(gainIndices, pGainsQ16, int8(e.previousGainIndex), conditional, numSubframes)

		finalGainsQ16 = pGainsQ16
		finalGainsID = silkGainsID(gainIndices, numSubframes)

		if iter > 0 && finalGainsID == gainsIDLower {
			e.rangeEncoder.RestoreState(&e.scratchRangeEncoderCopy2)
			break
		}

		if !conditional {
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
		if iter == 0 {
			frameIndices := sideInfoIndices{
				signalType:       int8(signalType),
				quantOffsetType:  int8(quantOffset),
				NLSFInterpCoefQ2: int8(interpIdx),
				Seed:             int8(seed),
			}
			for i := 0; i < numSubframes; i++ {
				frameIndices.GainsIndices[i] = gainIndices[i]
			}
			frameIndices.NLSFIndices[0] = int8(stage1Idx)
			for i := 0; i < e.lpcOrder && i < len(residuals); i++ {
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
			e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, seed, numSubframes, subframeSamples, frameSamples, speechActivityQ8)
		}

		e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

		ltpScaleQ14 := 0
		if signalType == typeVoiced {
			ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
		}

		allExcitation := e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, finalGainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, seed, numSubframes, subframeSamples, frameSamples, e.nsqState)
		e.encodePulses(allExcitation, signalType, quantOffset)

		nBits := e.rangeEncoder.Tell()

		if !e.useVBR && iter == 0 && nBits <= maxBits {
			break
		}
		if nBits <= maxBits && nBits >= maxBits-bitsMargin {
			break
		}
		if iter == maxIter-1 {
			if foundLower && (finalGainsID == gainsIDLower || nBits > maxBits) {
				e.rangeEncoder.RestoreState(&e.scratchRangeEncoderCopy2)
			}
			break
		}

		if !foundLower && nBits > maxBits {
			for i := 0; i < numSubframes; i++ {
				sum := 0
				for j := i * subframeSamples; j < (i+1)*subframeSamples; j++ {
					val := int(allExcitation[j])
					if val < 0 {
						val = -val
					}
					sum += val
				}
				if iter == 0 || (sum < bestSum[i] && !gainLock[i]) {
					bestSum[i] = sum
					bestGainMult[i] = gainMultQ8
				} else {
					gainLock[i] = true
				}
			}
		}

		if nBits > maxBits {
			if !foundLower && iter >= 2 {
				if noiseParams != nil {
					noiseParams.LambdaQ10 = (noiseParams.LambdaQ10 * 3) / 2
					if noiseParams.LambdaQ10 > 2048 {
						noiseParams.LambdaQ10 = 2048
					}
				}
				quantOffset = 0
				foundUpper = true
				nBitsUpper = nBits
				gainMultUpper = gainMultQ8
			} else {
				foundUpper = true
				nBitsUpper = nBits
				gainMultUpper = gainMultQ8
			}
		} else {
			foundLower = true
			nBitsLower = nBits
			gainMultLower = gainMultQ8
			if finalGainsID != gainsIDLower {
				gainsIDLower = finalGainsID
				e.rangeEncoder.SaveStateInto(&e.scratchRangeEncoderCopy2)
			}
		}

		if !(foundLower && foundUpper) {
			if nBits > maxBits {
				gainMultQ8 = (gainMultQ8 * 3) / 2
				if gainMultQ8 > 1024 {
					gainMultQ8 = 1024
				}
			} else {
				gainMultQ8 = (gainMultQ8 * 4) / 5
				if gainMultQ8 < 64 {
					gainMultQ8 = 64
				}
			}
		} else {
			gainMultQ8 = gainMultLower + ((gainMultUpper-gainMultLower)*(maxBits-nBitsLower))/(nBitsUpper-nBitsLower)
			margin := (gainMultLower - gainMultUpper) / 4
			if gainMultQ8 > gainMultLower-margin {
				gainMultQ8 = gainMultLower - margin
			}
			if gainMultQ8 < gainMultUpper+margin {
				gainMultQ8 = gainMultUpper + margin
			}
		}
	}

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

	if !useSharedEncoder {
		producedBits := e.rangeEncoder.Tell()
		targetBits := (e.targetRateBps * 20) / 1000 * 8
		if numSubframes == 2 {
			targetBits = (e.targetRateBps * 10) / 1000 * 8
		}
		e.nBitsExceeded += producedBits - targetBits
		if e.nBitsExceeded < 0 {
			e.nBitsExceeded = 0
		}
		if e.nBitsExceeded > 10000 {
			e.nBitsExceeded = 10000
		}
	}

	if !useSharedEncoder {
		flags := 0
		if vadFlag {
			flags = 1
		}
		flags = (flags << 1) | e.lbrrFlag
		nBitsHeader := (e.nFramesPerPacket + 1) * 1
		e.rangeEncoder.PatchInitialBits(uint32(flags), uint(nBitsHeader))
	}

	if useSharedEncoder {
		return nil
	}

	result := e.rangeEncoder.Done()
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
	e.nFramesEncoded = 0
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
	e.lastRng = e.rangeEncoder.Range()
	result := e.rangeEncoder.Done()
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	if !vadFlag {
		e.rangeEncoder.EncodeICDF16(0, ICDFFrameTypeVADActive, 8)
		return
	}
	if signalType < 1 {
		signalType = 1
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
