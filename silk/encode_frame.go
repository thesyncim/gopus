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
	payloadSizeMs := (frameSamples * 1000) / config.SampleRate
	packetPayloadSizeMs := payloadSizeMs
	if e.nFramesPerPacket > 1 {
		packetPayloadSizeMs = payloadSizeMs * e.nFramesPerPacket
	}

	// Match libopus packet control ordering:
	// packet/frame counters are reset before target-rate/SNR control for
	// standalone packets, so bitsBalance only applies to already-encoded
	// frames within the current packet.
	useSharedEncoder := e.rangeEncoder != nil
	// Save nFramesEncoded before the standalone reset.  The libopus "before
	// frame i" snapshot is taken after opus_encode_float(i-1) returns (where
	// nFramesEncoded was incremented to 1) but before opus_encode_float(i)
	// starts (which resets it to 0 inside silk_Encode).  We capture the
	// pre-reset value here so the FramePre trace matches libopus.
	preResetNFramesEncoded := e.nFramesEncoded
	preResetNFramesPerPacket := e.nFramesPerPacket
	if !useSharedEncoder {
		e.ResetPacketState()
		e.nFramesPerPacket = 1
	}
	firstFrameAfterReset := !e.haveEncoded

	// Capture FramePre trace BEFORE the targetRate/SNR computation to match
	// libopus capture timing.  The libopus "before frame i" snapshot reads
	// sCmn.TargetRate_bps and sCmn.SNR_dB_Q7 which were set by the PREVIOUS
	// frame's silk_control_SNR call.  By capturing here (before lines below
	// update lastControlTargetRateBps / snrDBQ7), we get the same values.
	if e.trace != nil && e.trace.FramePre != nil {
		tr := e.trace.FramePre
		tr.SignalType = e.ecPrevSignalType
		tr.LagIndex = e.ecPrevLagIndex
		tr.Contour = 0
		for i := 0; i < maxNbSubfr; i++ {
			tr.GainIndices[i] = 0
			tr.PitchL[i] = 0
		}
		tr.LastGainIndex = e.previousGainIndex
		tr.SumLogGainQ7 = e.sumLogGainQ7
		// Match libopus pre-frame snapshot timing: on the very first frame after
		// reset, silk_mode.bitRate is still at the init default (25000) before
		// the Opus-level control path applies the configured bitrate.
		tr.InputRateBps = e.targetRateBps
		if firstFrameAfterReset {
			tr.InputRateBps = 25000
		}
		tr.TargetRateBps = e.lastControlTargetRateBps
		tr.SNRDBQ7 = e.snrDBQ7
		tr.NBitsExceeded = e.nBitsExceeded
		tr.NFramesPerPacket = preResetNFramesPerPacket
		if firstFrameAfterReset {
			// libopus pre-frame snapshot sees packet state before first control,
			// where nFramesPerPacket is still zero-initialized.
			tr.NFramesPerPacket = 0
		}
		tr.NFramesEncoded = preResetNFramesEncoded
		tr.PrevLag = e.pitchState.prevLag
		tr.PrevSignalType = e.ecPrevSignalType
		tr.LTPCorr = e.ltpCorr
		tr.SpeechActivityQ8 = e.speechActivityQ8
		tr.InputTiltQ15 = e.inputTiltQ15
		tr.PitchEstThresholdQ16 = e.pitchEstimationThresholdQ16
		tr.NStatesDelayedDecision = e.nStatesDelayedDecision
		tr.WarpingQ16 = e.warpingQ16
		tr.FirstFrameAfterReset = firstFrameAfterReset
		tr.ECPrevLagIndex = e.ecPrevLagIndex
		tr.ECPrevSignalType = e.ecPrevSignalType
		if e.nsqState != nil {
			tr.NSQLagPrev = e.nsqState.lagPrev
			tr.NSQSLTPBufIdx = e.nsqState.sLTPBufIdx
			tr.NSQSLTPShpBufIdx = e.nsqState.sLTPShpBufIdx
			tr.NSQPrevGainQ16 = e.nsqState.prevGainQ16
			tr.NSQRandSeed = e.nsqState.randSeed
			tr.NSQRewhiteFlag = e.nsqState.rewhiteFlag
			tr.NSQXQHash = hashInt16Slice(e.nsqState.xq[:])
			tr.NSQSLTPShpHash = hashInt32Slice(e.nsqState.sLTPShpQ14[:])
			tr.NSQSLPCHash = hashInt32Slice(e.nsqState.sLPCQ14[:])
			tr.NSQSAR2Hash = hashInt32Slice(e.nsqState.sAR2Q14[:])
		}
		tr.PitchBufLen, tr.PitchBufHash, tr.PitchWinLen, tr.PitchWinHash = e.tracePitchBufferState(frameSamples, numSubframes)
		if tr.PitchBufLen > 0 {
			tr.PitchBuf = ensureFloat32Slice(&tr.PitchBuf, tr.PitchBufLen)
			scale := float32(silkSampleScale)
			for i := 0; i < tr.PitchBufLen; i++ {
				tr.PitchBuf[i] = e.inputBuffer[i] * scale
			}
		} else {
			tr.PitchBuf = tr.PitchBuf[:0]
		}
	}

	// Match libopus silk/control_codec.c: when fs_kHz changes (including on the
	// very first call, since fs_kHz starts at 0 after memset-init), the encoder
	// resets sShape.LastGainIndex to 10, sNSQ.lagPrev to 100, etc.  In gopus
	// the constructor leaves previousGainIndex at 0 (matching silk_init_encoder's
	// memset), and we apply the control_encoder initialization here so the FramePre
	// trace captures the pre-control_encoder state (0) while the actual encoding
	// uses the post-control_encoder state (10).
	if firstFrameAfterReset {
		e.previousGainIndex = 10
		// control_codec sets sNSQ.prev_gain_Q16 during first-frame reset path.
		if e.nsqState != nil && e.nsqState.prevGainQ16 == 0 {
			e.nsqState.prevGainQ16 = 1 << 16
		}
	}

	// Update target SNR based on configured bitrate and frame size.
	// Matches libopus silk/enc_API.c rate control logic (lines 411-443).
	if e.targetRateBps > 0 {
		// Total target bits for packet
		nBits := (e.targetRateBps * packetPayloadSizeMs) / 1000

		// Subtract bits used for LBRR (exponential moving average).
		// Matches libopus enc_API.c line 425: nBits -= psEnc->nBitsUsedLBRR;
		nBits -= e.nBitsUsedLBRR

		// Divide by number of uncoded frames left in packet
		nBits /= e.nFramesPerPacket

		// Convert to bits/second
		targetRate := 0
		if payloadSizeMs == 10 {
			targetRate = nBits * 100
		} else {
			targetRate = nBits * 50
		}

		// Subtract fraction of bits in excess of target in previous packets.
		// Matches libopus enc_API.c line 436.
		targetRate -= (e.nBitsExceeded * 1000) / 500

		// Compare actual vs target bits so far in this packet (bitsBalance).
		// Matches libopus enc_API.c lines 437-440:
		//   bitsBalance = ec_tell(psRangeEnc) - psEnc->nBitsUsedLBRR - nBits * nFramesEncoded
		if e.nFramesEncoded > 0 && e.rangeEncoder != nil {
			bitsBalance := e.rangeEncoder.Tell() - e.nBitsUsedLBRR - nBits*e.nFramesEncoded
			targetRate -= (bitsBalance * 1000) / 500
		}

		// Never exceed input bitrate, and maintain minimum for quality.
		if targetRate > e.targetRateBps {
			targetRate = e.targetRateBps
		}
		if targetRate < 5000 {
			targetRate = 5000
		}

		e.lastControlTargetRateBps = targetRate
		e.controlSNR(targetRate, numSubframes)
	} else {
		e.lastControlTargetRateBps = 0
	}

	// Quantize input to int16 precision to match libopus float API behavior.
	pcm = e.quantizePCMToInt16(pcm)

	// Apply LP variable cutoff filter for smooth bandwidth transitions.
	// Matches libopus encode_frame_FLP.c line 134: silk_LP_variable_cutoff(&sLP, inputBuf+1, frame_length).
	// The filter operates on int16 data, so we convert float32→int16, filter, then int16→float32.
	// Only do the conversion when the filter is active (Mode != 0); when Mode == 0 it's a no-op.
	if e.lpState.Mode != 0 {
		lpBuf := ensureInt16Slice(&e.scratchLPInt16, frameSamples)
		scale := float32(silkSampleScale)
		for i := 0; i < frameSamples; i++ {
			lpBuf[i] = int16(floatToInt16Round(pcm[i] * scale))
		}
		e.lpState.LPVariableCutoff(lpBuf, frameSamples)
		invScale := float32(1.0 / silkSampleScale)
		for i := 0; i < frameSamples; i++ {
			pcm[i] = float32(lpBuf[i]) * invScale
		}
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
		// Match libopus: search_thres1 = pitchEstimationThreshold_Q16 / 65536.0f — silk_float.
		searchThres1 := float64(float32(e.pitchEstimationThresholdQ16) / 65536.0)
		prevSignalType := 0
		if e.isPreviousFrameVoiced {
			prevSignalType = 2
		}
		// Match libopus find_pitch_lags_FLP: thrhld is silk_float (float32).
		thrhldF32 := float32(0.6)
		thrhldF32 -= float32(0.004) * float32(e.pitchEstimationLPCOrder)
		thrhldF32 -= float32(0.1) * float32(speechActivityQ8) * (1.0 / 256.0)
		thrhldF32 -= float32(0.15) * float32(prevSignalType>>1)
		thrhldF32 -= float32(0.1) * float32(e.inputTiltQ15) * (1.0 / 32768.0)
		thrhld := float64(thrhldF32)
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
			tr.FirstFrameAfterReset = firstFrameAfterReset
		}
		if firstFrameAfterReset {
			// Match libopus: skip pitch analysis on the first frame after reset.
			pitchLags = make([]int, numSubframes)
			lagIndex = 0
			contourIndex = 0
			e.ltpCorr = 0
			e.pitchState.ltpCorr = 0
			e.pitchState.prevLag = 0
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
				tr.LTPCorr = e.pitchState.ltpCorr
			}
			e.ltpCorr = e.pitchState.ltpCorr
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
		e.pitchState.ltpCorr = 0
		e.pitchState.prevLag = 0
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
	minInvGainVal := computeMinInvGain(predGainQ7, codingQuality, firstFrameAfterReset)
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
	quantOffsetBeforeProcess := quantOffset
	var gainsBeforeProcess [maxNbSubfr]float32
	for i := 0; i < numSubframes && i < len(gainsBeforeProcess) && i < len(gains); i++ {
		gainsBeforeProcess[i] = gains[i]
	}
	processedQuantOffset := applyGainProcessing(gains, resNrg, predGainQ7, e.snrDBQ7, signalType, e.inputTiltQ15, subframeSamples)
	if signalType == typeVoiced {
		quantOffset = processedQuantOffset
	}
	if noiseParams != nil {
		noiseParams.LambdaQ10 = computeLambdaQ10(signalType, speechActivityQ8, quantOffset, e.nStatesDelayedDecision, noiseParams.CodingQuality, noiseParams.InputQuality)
	}

	// Step 7: Prepare indices and gains for bitrate control loop.
	seed := e.frameCounter & 3
	maxBits := e.maxBits
	if maxBits <= 0 {
		// Derive from target rate: bits = targetRate * frameDuration_ms / 1000
		// This matches libopus where opus_encoder.c computes maxBits from the
		// packet budget before calling silk_Encode. When the SILK encoder is
		// invoked standalone (not via the Opus-level encoder), maxBits would
		// otherwise default to an astronomically large value that defeats the
		// rate control loop (VBR fast-exit always triggers on iteration 0).
		if e.targetRateBps > 0 && payloadSizeMs > 0 {
			maxBits = e.targetRateBps * payloadSizeMs / 1000
		} else {
			// Fallback: use a generous but reasonable default (1275 bytes = max SILK packet)
			maxBits = 1275 * 8
		}
		if maxBits < 100 {
			maxBits = 100
		}
	}
	var gainTrace *GainLoopTrace
	if e.trace != nil && e.trace.GainLoop != nil {
		gainTrace = e.trace.GainLoop
		gainTrace.Iterations = gainTrace.Iterations[:0]
		gainTrace.SeedIn = seed
		gainTrace.SeedOut = seed
		gainTrace.UsedDelayedDecision = e.nStatesDelayedDecision > 1 || e.warpingQ16 > 0
		gainTrace.WarpingQ16 = e.warpingQ16
		gainTrace.NStatesDelayedDecision = e.nStatesDelayedDecision
		gainTrace.MaxBits = maxBits
		if firstFrameAfterReset {
			// libopus pre-frame snapshot still has silk_mode.maxBits at 0 before
			// the first Opus control pass populates packet budget.
			gainTrace.MaxBits = 0
		}
		gainTrace.UseCBR = e.useCBR
		gainTrace.ConditionalCoding = condCoding == codeConditionally
		gainTrace.NumSubframes = numSubframes
		gainTrace.LastGainIndexPrev = int8(e.previousGainIndex)
		gainTrace.SignalType = signalType
		gainTrace.SpeechActivityQ8 = speechActivityQ8
		gainTrace.InputTiltQ15 = e.inputTiltQ15
		gainTrace.SNRDBQ7 = e.snrDBQ7
		gainTrace.PredGainQ7 = predGainQ7
		gainTrace.SubframeSamples = subframeSamples
		gainTrace.QuantOffsetBefore = quantOffsetBeforeProcess
		gainTrace.QuantOffsetAfter = quantOffset
		for i := range gainTrace.GainsUnqQ16 {
			gainTrace.GainsUnqQ16[i] = 0
			gainTrace.GainsBefore[i] = 0
			gainTrace.ResNrgBefore[i] = 0
			gainTrace.GainsAfter[i] = 0
		}
		for i := 0; i < numSubframes && i < len(gainTrace.GainsBefore); i++ {
			gainTrace.GainsBefore[i] = gainsBeforeProcess[i]
			if i < len(resNrg) {
				gainTrace.ResNrgBefore[i] = float32(resNrg[i])
			}
			if i < len(gains) {
				gainTrace.GainsAfter[i] = gains[i]
			}
		}
	}

	var frameIndices sideInfoIndices
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
	for i := 0; i < numSubframes && i < len(ltpIndices); i++ {
		frameIndices.LTPIndex[i] = ltpIndices[i]
	}

	gainsUnqQ16 := ensureInt32Slice(&e.scratchGainsUnqQ16, numSubframes)
	for i := 0; i < numSubframes && i < len(gains); i++ {
		gainsUnqQ16[i] = int32(gains[i] * 65536.0)
	}
	if gainTrace != nil {
		for i := 0; i < numSubframes && i < len(gainTrace.GainsUnqQ16); i++ {
			gainTrace.GainsUnqQ16[i] = gainsUnqQ16[i]
		}
	}
	gainsQ16 := ensureInt32Slice(&e.scratchGainsQ16, numSubframes)
	copy(gainsQ16, gainsUnqQ16)
	gainIndices := ensureInt8Slice(&e.scratchGainInd, numSubframes)
	lastGainIndexPrev := int8(e.previousGainIndex)
	currentPrevInd := silkGainsQuantInto(gainIndices, gainsQ16, lastGainIndexPrev, condCoding == codeConditionally, numSubframes)
	for i := 0; i < numSubframes; i++ {
		frameIndices.GainsIndices[i] = gainIndices[i]
	}

	// Prepare LBRR data for the next packet if FEC is enabled (before bitrate loop).
	if e.lbrrEnabled {
		e.lbrrEncode(framePCM, frameIndices, lpcQ12, predCoefQ12, interpIdx, pitchLags, ltpCoeffs, ltpScaleIndex, noiseParams, seed, numSubframes, subframeSamples, frameSamples, speechActivityQ8)
	}

	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}

	// Bitrate control: multi-pass NSQ + index encoding.
	bitsMargin := 5
	if !e.useCBR {
		bitsMargin = maxBits / 4
	}

	maxIter := 6
	gainMultQ8 := int16(1 << 8)
	foundLower := false
	foundUpper := false
	gainsID := silkGainsID(gainIndices, numSubframes)
	gainsIDLower := int32(-1)
	gainsIDUpper := int32(-1)

	rangeCopy := *e.rangeEncoder
	nsqCopy0 := *e.nsqState
	seedCopy := frameIndices.Seed
	ecPrevLagIndexCopy := e.ecPrevLagIndex
	ecPrevSignalTypeCopy := e.ecPrevSignalType
	rangeCopy2 := *e.rangeEncoder
	nsqCopy1 := *e.nsqState
	var lastGainIndexCopy2 int8
	ecBufCopy := ensureByteSlice(&e.scratchEcBufCopy, len(e.rangeEncoder.Buffer()))
	var nBits, nBitsLower, nBitsUpper int
	var gainMultLower, gainMultUpper int32
	var gainLock [maxNbSubfr]bool
	var bestGainMult [maxNbSubfr]int16
	var bestSum [maxNbSubfr]int
	var pulses []int8

	for iter := 0; ; iter++ {
		skippedNSQ := false
		bitsBeforeIndices := -1
		bitsAfterIndices := -1
		bitsAfterPulses := -1
		seedInIter := int(frameIndices.Seed)
		seedAfterNSQ := seedInIter

		if gainsID == gainsIDLower {
			nBits = nBitsLower
			skippedNSQ = true
		} else if gainsID == gainsIDUpper {
			nBits = nBitsUpper
			skippedNSQ = true
		} else {
			if iter > 0 {
				*e.rangeEncoder = rangeCopy
				*e.nsqState = nsqCopy0
				frameIndices.Seed = seedCopy
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
			}

			// Noise shaping quantization
			pulses, seedOut := e.computeNSQExcitation(framePCM, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, noiseParams, int(frameIndices.Seed), numSubframes, subframeSamples, frameSamples, e.nsqState)
			frameIndices.Seed = int8(seedOut)
			seedAfterNSQ = seedOut
			frameIndices.quantOffsetType = int8(quantOffset)

			if iter == maxIter && !foundLower {
				rangeCopy2 = *e.rangeEncoder
			}

			// Encode indices
			bitsBeforeIndices = e.rangeEncoder.Tell()
			e.encodeFrameType(vadFlag, signalType, int(frameIndices.quantOffsetType))
			if condCoding == codeIndependently {
				e.encodeAbsoluteGainIndex(int(frameIndices.GainsIndices[0]), signalType)
			} else {
				e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[0]), silk_delta_gain_iCDF, 8)
			}
			for i := 1; i < numSubframes; i++ {
				e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
			}
			e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType, numSubframes)
			if signalType == typeVoiced {
				e.encodePitchLagsWithParams(pitchParams, condCoding)
				e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
				if condCoding == codeIndependently {
					e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
				}
			}
			e.rangeEncoder.EncodeICDF(int(frameIndices.Seed), silk_uniform4_iCDF, 8)
			bitsAfterIndices = e.rangeEncoder.Tell()

			// Encode excitation pulses
			pulseCount := len(pulses)
			pulses32 := ensureInt32Slice(&e.scratchExcitation, pulseCount)
			for i := 0; i < pulseCount; i++ {
				pulses32[i] = int32(pulses[i])
			}
			e.encodePulses(pulses32, signalType, int(frameIndices.quantOffsetType))

			nBits = e.rangeEncoder.Tell()
			bitsAfterPulses = nBits

			// If we still bust after the last iteration, do some damage control.
			if iter == maxIter && !foundLower && nBits > maxBits {
				*e.rangeEncoder = rangeCopy2
				for i := 0; i < numSubframes; i++ {
					frameIndices.GainsIndices[i] = 4
				}
				if condCoding != codeConditionally {
					frameIndices.GainsIndices[0] = lastGainIndexPrev
				}
				e.ecPrevLagIndex = ecPrevLagIndexCopy
				e.ecPrevSignalType = ecPrevSignalTypeCopy
				currentPrevInd = lastGainIndexPrev
				if pulses != nil {
					for i := range pulses {
						pulses[i] = 0
					}
				}

				bitsBeforeIndices = e.rangeEncoder.Tell()
				e.encodeFrameType(vadFlag, signalType, int(frameIndices.quantOffsetType))
				if condCoding == codeIndependently {
					e.encodeAbsoluteGainIndex(int(frameIndices.GainsIndices[0]), signalType)
				} else {
					e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[0]), silk_delta_gain_iCDF, 8)
				}
				for i := 1; i < numSubframes; i++ {
					e.rangeEncoder.EncodeICDF(int(frameIndices.GainsIndices[i]), silk_delta_gain_iCDF, 8)
				}
				e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType, numSubframes)
				if signalType == typeVoiced {
					e.encodePitchLagsWithParams(pitchParams, condCoding)
					e.encodeLTPCoeffs(perIndex, ltpIndices[:], numSubframes)
					if condCoding == codeIndependently {
						e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
					}
				}
				e.rangeEncoder.EncodeICDF(int(frameIndices.Seed), silk_uniform4_iCDF, 8)
				bitsAfterIndices = e.rangeEncoder.Tell()
				if pulses != nil {
					pulses32 := ensureInt32Slice(&e.scratchExcitation, len(pulses))
					for i := range pulses32 {
						pulses32[i] = 0
					}
					e.encodePulses(pulses32, signalType, int(frameIndices.quantOffsetType))
				}
				nBits = e.rangeEncoder.Tell()
				bitsAfterPulses = nBits
			}

			if !e.useCBR && iter == 0 && nBits <= maxBits {
				if gainTrace != nil {
					gainTrace.Iterations = append(gainTrace.Iterations, GainLoopIter{
						Iter:              iter,
						GainMultQ8:        gainMultQ8,
						GainsID:           gainsID,
						QuantOffset:       quantOffset,
						Bits:              nBits,
						BitsBeforeIndices: bitsBeforeIndices,
						BitsAfterIndices:  bitsAfterIndices,
						BitsAfterPulses:   bitsAfterPulses,
						FoundLower:        foundLower,
						FoundUpper:        foundUpper,
						SkippedNSQ:        skippedNSQ,
						SeedIn:            seedInIter,
						SeedAfterNSQ:      seedAfterNSQ,
						SeedOut:           int(frameIndices.Seed),
					})
					gainTrace.SeedOut = int(frameIndices.Seed)
				}
				break
			}
		}

		if gainTrace != nil {
			gainTrace.Iterations = append(gainTrace.Iterations, GainLoopIter{
				Iter:              iter,
				GainMultQ8:        gainMultQ8,
				GainsID:           gainsID,
				QuantOffset:       quantOffset,
				Bits:              nBits,
				BitsBeforeIndices: bitsBeforeIndices,
				BitsAfterIndices:  bitsAfterIndices,
				BitsAfterPulses:   bitsAfterPulses,
				FoundLower:        foundLower,
				FoundUpper:        foundUpper,
				SkippedNSQ:        skippedNSQ,
				SeedIn:            seedInIter,
				SeedAfterNSQ:      seedAfterNSQ,
				SeedOut:           int(frameIndices.Seed),
			})
			gainTrace.SeedOut = int(frameIndices.Seed)
		}

		if iter == maxIter {
			if foundLower && (gainsID == gainsIDLower || nBits > maxBits) {
				*e.rangeEncoder = rangeCopy2
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(e.rangeEncoder.Buffer()[:offs], ecBufCopy[:offs])
				}
				*e.nsqState = nsqCopy1
				currentPrevInd = lastGainIndexCopy2
			}
			break
		}

		if nBits > maxBits {
			if !foundLower && iter >= 2 {
				if noiseParams != nil {
					lambda := noiseParams.LambdaQ10 + noiseParams.LambdaQ10/2
					// Match libopus encode_frame_FLP.c:
					// sEncCtrl.Lambda = silk_max_float(sEncCtrl.Lambda*1.5f, 1.5f)
					// (Q10 => minimum 1.5 * 1024 = 1536).
					if lambda < 1536 {
						lambda = 1536
					}
					noiseParams.LambdaQ10 = lambda
				}
				quantOffset = 0
				foundUpper = false
				gainsIDUpper = -1
			} else {
				foundUpper = true
				nBitsUpper = nBits
				gainMultUpper = int32(gainMultQ8)
				gainsIDUpper = gainsID
			}
		} else if nBits < maxBits-bitsMargin {
			foundLower = true
			nBitsLower = nBits
			gainMultLower = int32(gainMultQ8)
			if gainsID != gainsIDLower {
				gainsIDLower = gainsID
				rangeCopy2 = *e.rangeEncoder
				offs := int(rangeCopy2.Offs())
				if offs <= len(ecBufCopy) {
					copy(ecBufCopy[:offs], e.rangeEncoder.Buffer()[:offs])
				}
				nsqCopy1 = *e.nsqState
				lastGainIndexCopy2 = currentPrevInd
			}
		} else {
			break
		}

		if !foundLower && nBits > maxBits && pulses != nil {
			for i := 0; i < numSubframes; i++ {
				sum := 0
				start := i * subframeSamples
				end := start + subframeSamples
				if end > len(pulses) {
					end = len(pulses)
				}
				for j := start; j < end; j++ {
					v := int(pulses[j])
					if v < 0 {
						v = -v
					}
					sum += v
				}
				if iter == 0 || (sum < bestSum[i] && !gainLock[i]) {
					bestSum[i] = sum
					bestGainMult[i] = gainMultQ8
				} else {
					gainLock[i] = true
				}
			}
		}

		if !(foundLower && foundUpper) {
			if nBits > maxBits {
				next := int(gainMultQ8) * 3 / 2
				if next > 1024 {
					next = 1024
				}
				gainMultQ8 = int16(next)
			} else {
				next := int(gainMultQ8) * 4 / 5
				if next < 64 {
					next = 64
				}
				gainMultQ8 = int16(next)
			}
		} else {
			num := (gainMultUpper - gainMultLower) * int32(maxBits-nBitsLower)
			den := nBitsUpper - nBitsLower
			if den == 0 {
				den = 1
			}
			gainMultQ8 = int16(gainMultLower + num/int32(den))
			upper := gainMultLower + ((gainMultUpper - gainMultLower) >> 2)
			lower := gainMultUpper - ((gainMultUpper - gainMultLower) >> 2)
			if int32(gainMultQ8) > upper {
				gainMultQ8 = int16(upper)
			} else if int32(gainMultQ8) < lower {
				gainMultQ8 = int16(lower)
			}
		}

		for i := 0; i < numSubframes; i++ {
			tmp := gainMultQ8
			if gainLock[i] {
				tmp = bestGainMult[i]
			}
			gainsQ16[i] = silk_LSHIFT_SAT32(silk_SMULWB(gainsUnqQ16[i], int32(tmp)), 8)
		}
		currentPrevInd = silkGainsQuantInto(gainIndices, gainsQ16, lastGainIndexPrev, condCoding == codeConditionally, numSubframes)
		for i := 0; i < numSubframes; i++ {
			frameIndices.GainsIndices[i] = gainIndices[i]
		}
		gainsID = silkGainsID(gainIndices, numSubframes)
	}

	if gainTrace != nil {
		gainTrace.SeedOut = int(frameIndices.Seed)
	}

	e.previousGainIndex = int32(currentPrevInd)
	e.previousLogGain = int32(currentPrevInd)
	e.ecPrevSignalType = signalType
	e.frameCounter++
	e.isPreviousFrameVoiced = (signalType == typeVoiced)
	copy(e.prevLSFQ15, lsfQ15)
	captureFrameTrace := func() {
		if e.trace == nil || e.trace.Frame == nil {
			return
		}
		tr := e.trace.Frame
		tr.SignalType = signalType
		tr.LagIndex = pitchParams.lagIdx
		tr.Contour = pitchParams.contourIdx
		tr.LastGainIndex = e.previousGainIndex
		tr.SumLogGainQ7 = e.sumLogGainQ7
		tr.InputRateBps = e.targetRateBps
		tr.TargetRateBps = e.lastControlTargetRateBps
		tr.SNRDBQ7 = e.snrDBQ7
		tr.NBitsExceeded = e.nBitsExceeded
		tr.NFramesPerPacket = e.nFramesPerPacket
		tr.NFramesEncoded = e.nFramesEncoded
		for i := 0; i < maxNbSubfr; i++ {
			tr.GainIndices[i] = frameIndices.GainsIndices[i]
			tr.PitchL[i] = 0
			if i < len(pitchLags) {
				tr.PitchL[i] = pitchLags[i]
			}
		}
		tr.PrevLag = e.pitchState.prevLag
		tr.PrevSignalType = e.ecPrevSignalType
		tr.LTPCorr = e.ltpCorr
		tr.SpeechActivityQ8 = speechActivityQ8
		tr.InputTiltQ15 = e.inputTiltQ15
		tr.PitchEstThresholdQ16 = e.pitchEstimationThresholdQ16
		tr.NStatesDelayedDecision = e.nStatesDelayedDecision
		tr.WarpingQ16 = e.warpingQ16
		tr.FirstFrameAfterReset = firstFrameAfterReset
		tr.ECPrevLagIndex = e.ecPrevLagIndex
		tr.ECPrevSignalType = e.ecPrevSignalType
		if e.nsqState != nil {
			tr.NSQLagPrev = e.nsqState.lagPrev
			tr.NSQSLTPBufIdx = e.nsqState.sLTPBufIdx
			tr.NSQSLTPShpBufIdx = e.nsqState.sLTPShpBufIdx
			tr.NSQPrevGainQ16 = e.nsqState.prevGainQ16
			tr.NSQRandSeed = e.nsqState.randSeed
			tr.NSQRewhiteFlag = e.nsqState.rewhiteFlag
			tr.NSQXQHash = hashInt16Slice(e.nsqState.xq[:])
			tr.NSQSLTPShpHash = hashInt32Slice(e.nsqState.sLTPShpQ14[:])
			tr.NSQSLPCHash = hashInt32Slice(e.nsqState.sLPCQ14[:])
			tr.NSQSAR2Hash = hashInt32Slice(e.nsqState.sAR2Q14[:])
		}
		tr.PitchBufLen, tr.PitchBufHash, tr.PitchWinLen, tr.PitchWinHash = e.tracePitchBufferState(frameSamples, numSubframes)
		if tr.PitchBufLen > 0 {
			tr.PitchBuf = ensureFloat32Slice(&tr.PitchBuf, tr.PitchBufLen)
			scale := float32(silkSampleScale)
			for i := 0; i < tr.PitchBufLen; i++ {
				tr.PitchBuf[i] = e.inputBuffer[i] * scale
			}
		} else {
			tr.PitchBuf = tr.PitchBuf[:0]
		}
	}

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

	// Shift input buffer at end of frame (matches libopus memmove timing)
	e.shiftInputBuffer(frameSamples)

	e.nFramesEncoded++
	e.MarkEncoded()
	e.lastRng = e.rangeEncoder.Range()

	if useSharedEncoder {
		captureFrameTrace()
		// In libopus, nBitsExceeded is only updated once per packet at the
		// enc_API.c level (lines 555-557), after all frames in the packet are
		// encoded.  The per-frame encode function does NOT update nBitsExceeded.
		// The packet-level caller (EncodePacketWithFEC or hybrid encoder) is
		// responsible for the nBitsExceeded update.
		return nil
	}

	// Patch SILK header bits (VAD + LBRR) at start of bitstream.
	flags := uint32(0)
	if vadFlag {
		flags = 1
	}
	flags = (flags << 1) | uint32(e.lbrrFlag&1)
	e.rangeEncoder.PatchInitialBits(flags, 2)

	// Capture nBytesOut BEFORE ec_enc_done, matching libopus encode_frame_FLP.c:381:
	//   *pnBytesOut = silk_RSHIFT( ec_tell( psRangeEnc ) + 7, 3 );
	nBytesOut := (e.rangeEncoder.Tell() + 7) >> 3

	raw := e.rangeEncoder.Done()

	// Return a slice of the range encoder's buffer directly.
	// The caller must consume the data before the next EncodeFrame call.
	result := raw

	if e.targetRateBps > 0 && payloadSizeMs > 0 {
		// Match libopus enc_API.c nBitsExceeded update exactly:
		//   psEnc->nBitsExceeded += *nBytesOut * 8;
		//   psEnc->nBitsExceeded -= silk_DIV32_16(silk_MUL(bitRate, payloadSize_ms), 1000);
		//   psEnc->nBitsExceeded = silk_LIMIT(psEnc->nBitsExceeded, 0, 10000);
		e.nBitsExceeded += nBytesOut * 8
		e.nBitsExceeded -= (e.targetRateBps * payloadSizeMs) / 1000
		if e.nBitsExceeded < 0 {
			e.nBitsExceeded = 0
		} else if e.nBitsExceeded > 10000 {
			e.nBitsExceeded = 10000
		}
	}
	captureFrameTrace()
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, predCoefQ12 []int16, nlsfInterpQ2 int, gainsQ16 []int32, pitchLags []int, ltpCoeffs LTPCoeffsArray, ltpScaleQ14 int, signalType, quantOffset, speechActivityQ8 int, noiseParams *NoiseShapeParams, seed, numSubframes, subframeSamples, frameSamples int, nsqState *NSQState) ([]int8, int) {
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
		// Match libopus: SNR_dB = (silk_float)psEnc->sCmn.SNR_dB_Q7 * ( 1 / 128.0f ) — float32.
		snrDB := float32(e.snrDBQ7) * (1.0 / 128.0)
		noiseParams = e.noiseShapeState.ComputeNoiseShapeParams(signalType, speechActivityQ8, e.ltpCorr, pitchLags, float64(snrDB), quantOffset, inputQualityBandsQ15, numSubframes, fsKHz, e.nStatesDelayedDecision)
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
		SignalType:             signalType,
		QuantOffsetType:        quantOffset,
		PredCoefQ12:            predCoefQ12,
		NLSFInterpCoefQ2:       nlsfInterpQ2,
		LTPCoefQ14:             ltpCoefQ14,
		ARShpQ13:               arShpQ13,
		HarmShapeGainQ14:       harmShapeGainQ14,
		TiltQ14:                tiltQ14,
		LFShpQ14:               lfShpQ14,
		GainsQ16:               gainsQ16,
		PitchL:                 pitchL,
		LambdaQ10:              lambdaQ10,
		LTPScaleQ14:            int(ltpScaleQ14),
		FrameLength:            frameSamples,
		SubfrLength:            subframeSamples,
		NbSubfr:                numSubframes,
		LTPMemLength:           ltpMemLength,
		PredLPCOrder:           len(lpcQ12),
		ShapeLPCOrder:          shapeLPCOrder,
		WarpingQ16:             e.warpingQ16,
		NStatesDelayedDecision: e.nStatesDelayedDecision,
		Seed:                   seed,
	}
	var nsqTraceXSc []int32
	var nsqTraceSLTPQ15 []int32
	var nsqTraceSLTPRaw []int16
	var nsqTraceDelayedGain []int32
	var traceEnabled bool
	if e.trace != nil && e.trace.NSQ != nil && e.trace.NSQ.CaptureInputs {
		traceEnabled = true
		tr := e.trace.NSQ
		tr.SeedIn = seed
		tr.SignalType = signalType
		tr.QuantOffsetType = quantOffset
		tr.NLSFInterpCoefQ2 = nlsfInterpQ2
		tr.LambdaQ10 = lambdaQ10
		tr.LTPScaleQ14 = int(ltpScaleQ14)
		tr.FrameLength = frameSamples
		tr.SubfrLength = subframeSamples
		tr.NbSubfr = numSubframes
		tr.LTPMemLength = ltpMemLength
		tr.PredLPCOrder = len(lpcQ12)
		tr.ShapeLPCOrder = shapeLPCOrder
		tr.WarpingQ16 = e.warpingQ16
		tr.NStatesDelayedDecision = e.nStatesDelayedDecision
		tr.InputQ0 = append(tr.InputQ0[:0], inputQ0[:frameSamples]...)
		tr.PredCoefQ12 = append(tr.PredCoefQ12[:0], predCoefQ12...)
		tr.LTPCoefQ14 = append(tr.LTPCoefQ14[:0], ltpCoefQ14...)
		tr.ARShpQ13 = append(tr.ARShpQ13[:0], arShpQ13...)
		tr.HarmShapeGainQ14 = append(tr.HarmShapeGainQ14[:0], harmShapeGainQ14...)
		tr.TiltQ14 = append(tr.TiltQ14[:0], tiltQ14...)
		tr.LFShpQ14 = append(tr.LFShpQ14[:0], lfShpQ14...)
		tr.GainsQ16 = append(tr.GainsQ16[:0], gainsQ16...)
		tr.PitchL = append(tr.PitchL[:0], pitchL...)
		tr.PulsesLen = 0
		tr.PulsesHash = 0
		tr.XqHash = 0
		tr.SLTPQ15Hash = 0
		tr.XScSubfrHash = tr.XScSubfrHash[:0]
		tr.XScQ10 = tr.XScQ10[:0]
		tr.SLTPQ15 = tr.SLTPQ15[:0]
		tr.SLTPRaw = tr.SLTPRaw[:0]
		tr.DelayedGainQ10 = tr.DelayedGainQ10[:0]
		tr.NSQPostXQ = tr.NSQPostXQ[:0]
		tr.NSQPostSLTPShpQ14 = tr.NSQPostSLTPShpQ14[:0]
		tr.NSQPostLPCQ14 = tr.NSQPostLPCQ14[:0]
		tr.NSQPostAR2Q14 = tr.NSQPostAR2Q14[:0]
		tr.NSQPostLFARQ14 = 0
		tr.NSQPostDiffQ14 = 0
		tr.NSQPostLagPrev = 0
		tr.NSQPostSLTPBufIdx = 0
		tr.NSQPostSLTPShpBufIdx = 0
		tr.NSQPostRandSeed = 0
		tr.NSQPostPrevGainQ16 = 0
		tr.NSQPostRewhiteFlag = 0
		tr.NSQPostXQHash = 0
		tr.NSQPostSLTPShpHash = 0
		tr.NSQPostSLPCHash = 0
		tr.NSQPostSAR2Hash = 0
		tr.SeedOut = seed

		if numSubframes > 0 && subframeSamples > 0 {
			xscLen := numSubframes * subframeSamples
			nsqTraceXSc = make([]int32, xscLen)
			setNSQDelDecDebugXSc(nsqTraceXSc, subframeSamples)
		}
		if params.LTPMemLength > 0 && frameSamples > 0 {
			nsqTraceSLTPQ15 = make([]int32, params.LTPMemLength+frameSamples)
			setNSQDelDecDebugSLTP(nsqTraceSLTPQ15)
			nsqTraceSLTPRaw = make([]int16, params.LTPMemLength+frameSamples)
			setNSQDelDecDebugSLTPRaw(nsqTraceSLTPRaw)
		}
		nsqTraceDelayedGain = make([]int32, decisionDelay)
		setNSQDelDecDebugDelayedGain(nsqTraceDelayedGain)
	}
	state := nsqState
	if state == nil {
		state = e.nsqState
	}
	if traceEnabled && state != nil {
		tr := e.trace.NSQ
		tr.NSQLFARQ14 = state.sLFARShpQ14
		tr.NSQDiffQ14 = state.sDiffShpQ14
		tr.NSQLagPrev = state.lagPrev
		tr.NSQSLTPBufIdx = state.sLTPBufIdx
		tr.NSQSLTPShpBufIdx = state.sLTPShpBufIdx
		tr.NSQRandSeed = state.randSeed
		tr.NSQPrevGainQ16 = state.prevGainQ16
		tr.NSQRewhiteFlag = state.rewhiteFlag
		tr.NSQXQ = append(tr.NSQXQ[:0], state.xq[:]...)
		tr.NSQSLTPShpQ14 = append(tr.NSQSLTPShpQ14[:0], state.sLTPShpQ14[:]...)
		tr.NSQLPCQ14 = append(tr.NSQLPCQ14[:0], state.sLPCQ14[:]...)
		tr.NSQAR2Q14 = append(tr.NSQAR2Q14[:0], state.sAR2Q14[:]...)
	}
	seedOut := seed
	var pulses []int8
	if params.NStatesDelayedDecision > 1 || params.WarpingQ16 > 0 {
		var outXQ []int16
		pulses, outXQ, seedOut = NoiseShapeQuantizeDelDec(state, inputQ0, params)
		if traceEnabled {
			tr := e.trace.NSQ
			tr.PulsesLen = len(pulses)
			tr.PulsesHash = hashInt8Slice(pulses)
			tr.XqHash = hashInt16Slice(outXQ)
			tr.SeedOut = seedOut
			if len(nsqTraceSLTPQ15) > 0 {
				tr.SLTPQ15Hash = hashInt32Slice(nsqTraceSLTPQ15)
				tr.SLTPQ15 = append(tr.SLTPQ15[:0], nsqTraceSLTPQ15...)
			}
			if len(nsqTraceSLTPRaw) > 0 {
				tr.SLTPRaw = append(tr.SLTPRaw[:0], nsqTraceSLTPRaw...)
			}
			if len(nsqTraceDelayedGain) > 0 {
				tr.DelayedGainQ10 = append(tr.DelayedGainQ10[:0], nsqTraceDelayedGain...)
			}
			if len(nsqTraceXSc) > 0 && numSubframes > 0 && subframeSamples > 0 {
				tr.XScSubfrHash = ensureUint64Slice(&tr.XScSubfrHash, numSubframes)
				for sf := 0; sf < numSubframes; sf++ {
					start := sf * subframeSamples
					end := start + subframeSamples
					if end > len(nsqTraceXSc) {
						end = len(nsqTraceXSc)
					}
					if start < end {
						tr.XScSubfrHash[sf] = hashInt32Slice(nsqTraceXSc[start:end])
					}
				}
				tr.XScQ10 = append(tr.XScQ10[:0], nsqTraceXSc...)
			}
		}
	} else {
		pulses, _ = NoiseShapeQuantize(state, inputQ0, params)
		if traceEnabled {
			tr := e.trace.NSQ
			tr.PulsesLen = len(pulses)
			tr.PulsesHash = hashInt8Slice(pulses)
			tr.SeedOut = seedOut
		}
	}
	if traceEnabled && state != nil {
		tr := e.trace.NSQ
		tr.NSQPostLFARQ14 = state.sLFARShpQ14
		tr.NSQPostDiffQ14 = state.sDiffShpQ14
		tr.NSQPostLagPrev = state.lagPrev
		tr.NSQPostSLTPBufIdx = state.sLTPBufIdx
		tr.NSQPostSLTPShpBufIdx = state.sLTPShpBufIdx
		tr.NSQPostRandSeed = state.randSeed
		tr.NSQPostPrevGainQ16 = state.prevGainQ16
		tr.NSQPostRewhiteFlag = state.rewhiteFlag
		tr.NSQPostXQ = append(tr.NSQPostXQ[:0], state.xq[:]...)
		tr.NSQPostSLTPShpQ14 = append(tr.NSQPostSLTPShpQ14[:0], state.sLTPShpQ14[:]...)
		tr.NSQPostLPCQ14 = append(tr.NSQPostLPCQ14[:0], state.sLPCQ14[:]...)
		tr.NSQPostAR2Q14 = append(tr.NSQPostAR2Q14[:0], state.sAR2Q14[:]...)
		tr.NSQPostXQHash = hashInt16Slice(tr.NSQPostXQ)
		tr.NSQPostSLTPShpHash = hashInt32Slice(tr.NSQPostSLTPShpQ14)
		tr.NSQPostSLPCHash = hashInt32Slice(tr.NSQPostLPCQ14)
		tr.NSQPostSAR2Hash = hashInt32Slice(tr.NSQPostAR2Q14)
	}
	if traceEnabled {
		setNSQDelDecDebugSLTP(nil)
		setNSQDelDecDebugSLTPRaw(nil)
		setNSQDelDecDebugXSc(nil, 0)
		setNSQDelDecDebugDelayedGain(nil)
	}
	return pulses, seedOut
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
	// Capture nBytesOut BEFORE ec_enc_done, matching libopus encode_frame_FLP.c:381.
	nBytesOut := (e.rangeEncoder.Tell() + 7) >> 3
	result := e.rangeEncoder.Done()
	if e.targetRateBps > 0 {
		payloadSizeMs := (nFrames * frameSamples * 1000) / config.SampleRate
		if payloadSizeMs > 0 {
			e.nBitsExceeded += nBytesOut * 8
			e.nBitsExceeded -= (e.targetRateBps * payloadSizeMs) / 1000
			if e.nBitsExceeded < 0 {
				e.nBitsExceeded = 0
			} else if e.nBitsExceeded > 10000 {
				e.nBitsExceeded = 10000
			}
		}
	}
	e.rangeEncoder = nil
	return result
}

func (e *Encoder) encodeFrameType(vadFlag bool, signalType, quantOffset int) {
	typeOffset := 2*signalType + quantOffset
	if vadFlag {
		sym := typeOffset - 2
		if sym < 0 {
			sym = 0
		}
		if sym >= len(silk_type_offset_VAD_iCDF) {
			sym = len(silk_type_offset_VAD_iCDF) - 1
		}
		e.rangeEncoder.EncodeICDF(sym, silk_type_offset_VAD_iCDF, 8)
		return
	}
	// VAD inactive uses a dedicated 2-symbol table (typeOffset 0 or 1).
	sym := typeOffset
	if sym < 0 {
		sym = 0
	}
	if sym >= len(silk_type_offset_no_VAD_iCDF) {
		sym = len(silk_type_offset_no_VAD_iCDF) - 1
	}
	e.rangeEncoder.EncodeICDF(sym, silk_type_offset_no_VAD_iCDF, 8)
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

	// Per libopus silk/float/encode_frame_FLP.c:
	// 1. Copy new PCM to buffer FIRST (before shift)
	// 2. Run pitch analysis
	// 3. Shift buffer at frame END (after encoding)
	//
	// This ensures LTP memory contains the correct history at pitch analysis time.
	// The shift for frame N happens AFTER frame N encoding (in shiftInputBuffer).

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
	// Match libopus encode_frame_FLP: add a tiny anti-denormal signal to
	// eight evenly spaced positions in the freshly copied frame.
	// This is applied to x_buf after short->float conversion.
	step := frameSamples >> 3
	if step > 0 {
		// Our x_buf is normalized to [-1, 1], while libopus x_buf is int16-scaled.
		// Scale the tiny anti-denormal term to match libopus magnitude.
		antiDenormal := float32(1e-6 / silkSampleScale)
		for i := 0; i < 8; i++ {
			idx := i * step
			if idx >= 0 && idx < frameSamples {
				dither := antiDenormal
				if (i & 2) != 0 {
					dither = -dither
				}
				insert[idx] += dither
			}
		}
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

func (e *Encoder) tracePitchBufferState(frameSamples, numSubframes int) (bufLen int, bufHash uint64, winLen int, winHash uint64) {
	// Match libopus capture timing: before the first encode, the SILK
	// encoder state fields (frame_length, la_pitch, ltp_mem_length) have
	// not been set by silk_control_encoder yet, so buf_len computes as 0.
	// Our trace is captured inside EncodeFrame (after the encoder already
	// knows its configuration), so we explicitly return 0 for the first
	// frame to match the libopus "before frame 0" snapshot.
	const fnvOffsetBasis = 1469598103934665603
	if !e.haveEncoded {
		return 0, fnvOffsetBasis, 0, fnvOffsetBasis
	}
	fsKHz := e.sampleRate / 1000
	if fsKHz <= 0 {
		return 0, fnvOffsetBasis, 0, fnvOffsetBasis
	}
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laPitchSamples := laPitchMs * fsKHz
	bufLen = ltpMemSamples + frameSamples + laPitchSamples
	if bufLen < 0 {
		bufLen = 0
	}
	if bufLen > len(e.inputBuffer) {
		bufLen = len(e.inputBuffer)
	}
	if bufLen == 0 {
		return 0, 0, 0, 0
	}
	scale := float32(silkSampleScale)
	bufHash = hashScaledFloat32Slice(e.inputBuffer[:bufLen], scale)

	if numSubframes == 2 {
		winLen = findPitchLpcWinMs2SF * fsKHz
	} else {
		winLen = findPitchLpcWinMs * fsKHz
	}
	if winLen < 0 {
		winLen = 0
	}
	if winLen > bufLen {
		winLen = bufLen
	}
	if winLen == 0 {
		return bufLen, bufHash, 0, 0
	}
	winHash = hashScaledFloat32Slice(e.inputBuffer[bufLen-winLen:bufLen], scale)
	return bufLen, bufHash, winLen, winHash
}

// shiftInputBuffer shifts the input buffer left by frameSamples after frame encoding.
// This matches libopus silk/float/encode_frame_FLP.c silk_memmove at end of frame.
// Must be called AFTER encoding is complete for the current frame.
func (e *Encoder) shiftInputBuffer(frameSamples int) {
	if frameSamples <= 0 {
		return
	}
	fsKHz := e.sampleRate / 1000
	if fsKHz < 1 {
		fsKHz = 1
	}
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	keep := ltpMemSamples + laShapeSamples
	shapeBuf := e.inputBuffer
	if len(shapeBuf) < keep+frameSamples {
		return
	}
	// Per libopus: memmove(x_buf, x_buf + frame_length, ltp_mem + la_shape)
	// This shifts buffer left by frame_length, keeping ltp_mem + la_shape samples.
	copy(shapeBuf[:keep], shapeBuf[frameSamples:frameSamples+keep])
}
