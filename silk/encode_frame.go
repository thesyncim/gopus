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
func (e *Encoder) EncodeFrame(pcm []float32, vadFlag bool) []byte {
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

	// Update target SNR based on configured bitrate and frame size.
	if e.targetRateBps > 0 {
		e.controlSNR(e.targetRateBps, numSubframes)
	}

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
		e.haveEncoded = false    // Reset so gain encoding uses absolute mode
		e.previousGainIndex = 10 // Default gain index (matches decoder's reset)

		// Create our own range encoder using scratch buffer
		// Allocate extra space for potential LBRR data
		bufSize := len(pcm) / 3
		if bufSize < 80 {
			bufSize = 80
		}
		if bufSize > 250 {
			bufSize = 250
		}
		// Add extra for LBRR if enabled
		if e.lbrrEnabled {
			bufSize += 50
		}
		output := ensureByteSlice(&e.scratchOutput, bufSize)
		e.scratchRangeEncoder.Init(output)
		e.rangeEncoder = &e.scratchRangeEncoder
	}

	// Step 0.5: Update pitch analysis buffer
	// The buffer holds LTP memory (20ms history) + current frame (20ms).
	// Shift old samples left and append new samples.
	// This provides proper history for pitch detection correlation.
	pitchBufFrameLen := len(pcm)
	if len(e.pitchAnalysisBuf) >= pitchBufFrameLen*2 {
		// Shift old samples left (keep second half as LTP memory)
		copy(e.pitchAnalysisBuf[:pitchBufFrameLen], e.pitchAnalysisBuf[pitchBufFrameLen:])
		// Append new frame
		copy(e.pitchAnalysisBuf[pitchBufFrameLen:], pcm)
	} else if len(e.pitchAnalysisBuf) > 0 {
		// Buffer might be smaller, just copy what fits
		copy(e.pitchAnalysisBuf, pcm)
	}

	// Step 1: Classify frame (VAD)
	var signalType, quantOffset int
	var speechActivityQ8 int
	if vadFlag {
		signalType, quantOffset = e.classifyFrame(pcm)
		speechActivityQ8 = 200 // Active speech activity (simplified)
	} else {
		signalType, quantOffset = 0, 0
		speechActivityQ8 = 50 // Low activity
	}

	// Step 1.5: Encode VAD and LBRR flags (standalone SILK only)
	// Per RFC 6716, the SILK layer header contains:
	// 1. VAD flag for each frame (1 bit per frame)
	// 2. LBRR flag (1 bit) - whether LBRR data follows
	// 3. LBRR data if LBRR flag is set
	// In hybrid mode, this header is handled by the Opus layer.
	if !useSharedEncoder {
		// Encode VAD flag (1 bit) - this is for single-frame packets
		vadBit := 0
		if vadFlag {
			vadBit = 1
		}
		e.rangeEncoder.EncodeBit(vadBit, 1)

		// Encode LBRR flag (1 bit) - whether LBRR data follows
		lbrrFlagBit := 0
		if e.lbrrEnabled && e.nFramesEncoded > 0 {
			// LBRR is only available after the first frame
			lbrrFlagBit = 1
		}
		e.rangeEncoder.EncodeBit(lbrrFlagBit, 1)
	}

	// Step 2: Encode frame type using ICDFFrameTypeVADActive
	e.encodeFrameType(vadFlag, signalType, quantOffset)

	// Step 3: Compute LPC coefficients FIRST (needed for residual-based gain computation)
	// This sets e.lastTotalEnergy, e.lastInvGain, e.lastNumSamples
	lpcQ12 := e.computeLPCFromFrame(pcm)

	// Step 4: Compute and encode gains from LPC residual energy
	// Per libopus: gains are sqrt(residual_energy) from Burg/Schur analysis,
	// NOT sqrt(raw_signal_energy). The residual is much smaller than raw signal
	// because LPC prediction removes the predictable component.
	// NSQ scales input by inverse gain, quantizes, then decoder scales output by gain.
	gains := e.computeSubframeGainsFromResidual(pcm, numSubframes)
	gainsQ16 := e.encodeSubframeGains(gains, signalType, numSubframes)

	// Step 5: Convert LPC to NLSF using silkA2NLSF (with bandwidth expansion retry)
	// lpcToLSFEncodeInto fails for sparse LPC coefficients (e.g., from sine waves
	// where Burg's method produces only 2 non-zero coefficients). silkA2NLSF
	// handles this by applying bandwidth expansion and retrying root-finding.
	order := len(lpcQ12)
	lsfQ15 := ensureInt16Slice(&e.scratchLSFQ15, order)
	lpcQ16 := ensureInt32Slice(&e.scratchLPCQ16, order)
	for i := 0; i < order; i++ {
		// Convert Q12 to Q16: multiply by 2^(16-12) = 16
		lpcQ16[i] = int32(lpcQ12[i]) << 4
	}
	silkA2NLSF(lsfQ15, lpcQ16, order)
	stage1Idx, residuals, interpIdx := e.quantizeLSF(lsfQ15, e.bandwidth, signalType, speechActivityQ8, numSubframes)
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)
	// Reconstruct quantized NLSF and build predictor coefficients for NSQ.
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	interpIdx = e.buildPredCoefQ12(predCoefQ12, lsfQ15, interpIdx)

	// Step 6: Pitch detection and LTP (voiced only)
	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}
	var pitchLags []int
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	periodicity := 0
	if signalType == 2 {
		// Use pitch analysis buffer (LTP memory + current frame) for better pitch detection
		pitchLags = e.detectPitch(e.pitchAnalysisBuf, numSubframes)
		e.encodePitchLags(pitchLags, numSubframes)

		// Update LTP correlation for noise shaping (from pitch detection)
		e.ltpCorr = float32(e.pitchState.ltpCorr)
		if e.ltpCorr > 1.0 {
			e.ltpCorr = 1.0
		}

		periodicity = e.determinePeriodicity(pcm, pitchLags)
		ltpCoeffs = e.analyzeLTP(pcm, pitchLags, numSubframes, periodicity)
		e.encodeLTPCoeffs(ltpCoeffs, periodicity, numSubframes)

		ltpPredGainQ7 := computeLTPPredGainQ7(pcm, pitchLags, ltpCoeffs, numSubframes, subframeSamples)
		ltpScaleIndex = e.computeLTPScaleIndex(ltpPredGainQ7, condCoding)
		// Encode LTP scale index (required for voiced frames).
		if condCoding == codeIndependently {
			e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
		}
	} else {
		// Reset LTP correlation for unvoiced frames
		e.ltpCorr = 0
	}

	// Step 6.5: LBRR Encoding (FEC)
	// Per libopus: LBRR is encoded AFTER VAD but BEFORE main frame encoding
	// Reference: silk/float/encode_frame_FLP.c silk_LBRR_encode_FLP call
	e.lbrrEncode(pcm, lpcQ12, lsfQ15, gains, pitchLags, signalType, quantOffset, speechActivityQ8, condCoding)

	// Step 7: Encode seed (LAST in indices, BEFORE pulses)
	// Per libopus: seed = frameCounter++ & 3
	seed := e.frameCounter & 3
	e.frameCounter++
	e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

	// Step 8: Compute excitation using Noise Shaping Quantization (NSQ)
	// Per libopus silk_encode_pulses(), pulses are encoded for full frame_length
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	// Use NSQ for proper noise-shaped quantization with adaptive parameters
	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}
	allExcitation := e.computeNSQExcitation(pcm, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, seed, numSubframes, subframeSamples, frameSamples)

	// Encode ALL pulses for the entire frame at once
	e.encodePulses(allExcitation, signalType, quantOffset)

	// Update state for next frame
	e.isPreviousFrameVoiced = (signalType == 2)
	copy(e.prevLSFQ15, lsfQ15)
	e.nFramesEncoded++
	e.MarkEncoded()

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
func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, predCoefQ12 []int16, nlsfInterpQ2 int, gainsQ16 []int32, pitchLags []int, ltpCoeffs LTPCoeffsArray, ltpScaleQ14 int, signalType, quantOffset, speechActivityQ8, seed, numSubframes, subframeSamples, frameSamples int) []int32 {
	// Convert PCM to int16 for NSQ using scratch buffer
	inputQ0 := ensureInt16Slice(&e.scratchInputQ0, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		// Scale float to int16 range (symmetric: -1.0→-32768, +1.0→+32768)
		// Use 32768.0 for symmetric scaling, matching libopus and resample_libopus.go
		val := pcm[i] * 32768.0
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		inputQ0[i] = int16(val)
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

	// Compute noise shaping AR coefficients from LPC
	// For simplicity, use the LPC coefficients with bandwidth expansion
	shapeLPCOrder := len(lpcQ12)
	if shapeLPCOrder > maxShapeLpcOrder {
		shapeLPCOrder = maxShapeLpcOrder
	}

	// Create shaping coefficients (Q13) from LPC (Q12) using scratch buffer
	arShpQ13 := ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
	for i := range arShpQ13 {
		arShpQ13[i] = 0 // Clear
	}
	for sf := 0; sf < numSubframes; sf++ {
		for i := 0; i < shapeLPCOrder && i < len(lpcQ12); i++ {
			// Convert Q12 to Q13 with bandwidth expansion (0.94^(i+1))
			// This shapes the quantization noise spectrum
			arShpQ13[sf*maxShapeLpcOrder+i] = int16(int32(lpcQ12[i]) * 2 * 94 / 100)
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
	if e.noiseShapeState == nil {
		e.noiseShapeState = NewNoiseShapeState()
	}

	// Get sample rate in kHz for noise shaping computation
	fsKHz := e.sampleRate / 1000
	if fsKHz < 8 {
		fsKHz = 8
	}

	// Compute adaptive noise shaping parameters
	noiseParams := e.noiseShapeState.ComputeNoiseShapeParams(
		signalType,
		speechActivityQ8,
		e.ltpCorr,
		pitchL,
		e.snrDBQ7,
		quantOffset,
		numSubframes,
		fsKHz,
	)

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
	pulses, _ := NoiseShapeQuantize(e.nsqState, inputQ0, params)

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
func (e *Encoder) EncodePacketWithFEC(pcm []float32, vadFlags []bool) []byte {
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
	if bufSize > 400 {
		bufSize = 400
	}
	output := ensureByteSlice(&e.scratchOutput, bufSize)
	e.scratchRangeEncoder.Init(output)
	e.rangeEncoder = &e.scratchRangeEncoder

	// Step 1: Encode VAD and LBRR header
	// First, create space for VAD+FEC flags at start of payload
	nBitsHeader := (nFrames + 1) * 1 // nFrames VAD + 1 LBRR flag
	iCDFVal := 256 - (256 >> uint(nBitsHeader))
	if iCDFVal > 255 {
		iCDFVal = 255
	}
	iCDF := []uint8{uint8(iCDFVal), 0}
	e.rangeEncoder.EncodeICDF8(0, iCDF, 8)

	// Step 2: Encode any LBRR data from previous packet
	e.encodeLBRRData(e.rangeEncoder, 1, true) // nChannels = 1 for mono

	// Step 3: Encode each frame
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

		// Encode the frame (this also computes LBRR for current frame)
		e.encodeFrameInternal(framePCM, vadFlag)
	}

	// Step 4: Patch initial bits with VAD and FEC flags
	// Build the flags value: [VAD_0, VAD_1, ..., VAD_n-1, LBRR_flag]
	flags := 0
	for i := 0; i < nFrames; i++ {
		flags <<= 1
		if vadFlags == nil || (i < len(vadFlags) && vadFlags[i]) {
			flags |= 1
		}
	}
	flags <<= 1
	if e.hasLBRRData() {
		flags |= 1
	}

	// Use ec_enc_patch_initial_bits equivalent
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
func (e *Encoder) encodeFrameInternal(pcm []float32, vadFlag bool) {
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

	// Step 0.5: Update pitch analysis buffer
	// The buffer holds LTP memory (20ms history) + current frame (20ms).
	pitchBufFrameLen := len(pcm)
	if len(e.pitchAnalysisBuf) >= pitchBufFrameLen*2 {
		copy(e.pitchAnalysisBuf[:pitchBufFrameLen], e.pitchAnalysisBuf[pitchBufFrameLen:])
		copy(e.pitchAnalysisBuf[pitchBufFrameLen:], pcm)
	} else if len(e.pitchAnalysisBuf) > 0 {
		copy(e.pitchAnalysisBuf, pcm)
	}

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

	// Step 2: Encode frame type
	e.encodeFrameType(vadFlag, signalType, quantOffset)

	// Step 3: Compute LPC coefficients FIRST (needed for residual-based gain computation)
	lpcQ12 := e.computeLPCFromFrame(pcm)

	// Step 4: Compute and encode gains from signal energy
	gains := e.computeSubframeGains(pcm, numSubframes)
	gainsQ16 := e.encodeSubframeGains(gains, signalType, numSubframes)

	// Step 5: Convert LPC to NLSF using silkA2NLSF (with bandwidth expansion retry)
	// lpcToLSFEncodeInto fails for sparse LPC coefficients (e.g., from sine waves
	// where Burg's method produces only 2 non-zero coefficients). silkA2NLSF
	// handles this by applying bandwidth expansion and retrying root-finding.
	order := len(lpcQ12)
	lsfQ15 := ensureInt16Slice(&e.scratchLSFQ15, order)
	lpcQ16 := ensureInt32Slice(&e.scratchLPCQ16, order)
	for i := 0; i < order; i++ {
		// Convert Q12 to Q16: multiply by 2^(16-12) = 16
		lpcQ16[i] = int32(lpcQ12[i]) << 4
	}
	silkA2NLSF(lsfQ15, lpcQ16, order)
	stage1Idx, residuals, interpIdx := e.quantizeLSF(lsfQ15, e.bandwidth, signalType, speechActivityQ8, numSubframes)
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)
	// Reconstruct quantized NLSF and build predictor coefficients for NSQ.
	lsfQ15 = e.decodeQuantizedNLSF(stage1Idx, residuals, e.bandwidth)
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	interpIdx = e.buildPredCoefQ12(predCoefQ12, lsfQ15, interpIdx)

	// Step 6: Pitch detection and LTP (voiced only)
	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}
	var pitchLags []int
	var ltpCoeffs LTPCoeffsArray
	ltpScaleIndex := 0
	periodicity := 0
	if signalType == 2 {
		// Use pitch analysis buffer (LTP memory + current frame) for better pitch detection
		pitchLags = e.detectPitch(e.pitchAnalysisBuf, numSubframes)
		e.encodePitchLags(pitchLags, numSubframes)

		// Update LTP correlation for noise shaping (from pitch detection)
		e.ltpCorr = float32(e.pitchState.ltpCorr)
		if e.ltpCorr > 1.0 {
			e.ltpCorr = 1.0
		}

		periodicity = e.determinePeriodicity(pcm, pitchLags)
		ltpCoeffs = e.analyzeLTP(pcm, pitchLags, numSubframes, periodicity)
		e.encodeLTPCoeffs(ltpCoeffs, periodicity, numSubframes)

		ltpPredGainQ7 := computeLTPPredGainQ7(pcm, pitchLags, ltpCoeffs, numSubframes, subframeSamples)
		ltpScaleIndex = e.computeLTPScaleIndex(ltpPredGainQ7, condCoding)
		// Encode LTP scale index (required for voiced frames).
		if condCoding == codeIndependently {
			e.rangeEncoder.EncodeICDF(ltpScaleIndex, silk_LTPscale_iCDF, 8)
		}
	} else {
		// Reset LTP correlation for unvoiced frames
		e.ltpCorr = 0
	}

	// Step 6.5: LBRR Encoding (FEC) for this frame
	e.lbrrEncode(pcm, lpcQ12, lsfQ15, gains, pitchLags, signalType, quantOffset, speechActivityQ8, condCoding)

	// Step 7: Encode seed
	seed := e.frameCounter & 3
	e.frameCounter++
	e.rangeEncoder.EncodeICDF(seed, silk_uniform4_iCDF, 8)

	// Step 8: Compute and encode excitation
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	ltpScaleQ14 := 0
	if signalType == typeVoiced {
		ltpScaleQ14 = int(silk_LTPScales_table_Q14[ltpScaleIndex])
	}
	allExcitation := e.computeNSQExcitation(pcm, lpcQ12, predCoefQ12, interpIdx, gainsQ16, pitchLags, ltpCoeffs, ltpScaleQ14, signalType, quantOffset, speechActivityQ8, seed, numSubframes, subframeSamples, frameSamples)
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
