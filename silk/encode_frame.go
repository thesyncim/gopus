package silk

import "github.com/thesyncim/gopus/rangecoding"

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
	numSubframes := 4 // 20ms frame = 4 subframes

	// Check if we have a pre-set range encoder (hybrid mode)
	// Note: rangeEncoder is set externally via SetRangeEncoder() for hybrid mode.
	// In standalone mode, rangeEncoder should be nil at the start of each frame.
	useSharedEncoder := e.rangeEncoder != nil

	if !useSharedEncoder {
		// Standalone SILK mode: create our own range encoder
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
		output := make([]byte, bufSize)
		e.rangeEncoder = &rangecoding.Encoder{}
		e.rangeEncoder.Init(output)
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

	// Step 2: Encode frame type using ICDFFrameTypeVADActive
	e.encodeFrameType(vadFlag, signalType, quantOffset)

	// Step 3: Compute and encode gains
	gains := e.computeSubframeGains(pcm, numSubframes)
	e.encodeSubframeGains(gains, signalType, numSubframes)

	// Step 4: Compute LPC coefficients
	lpcQ12 := e.computeLPCFromFrame(pcm)

	// Step 5: Convert to LSF and quantize
	lsfQ15 := lpcToLSFEncode(lpcQ12)
	stage1Idx, residuals, interpIdx := e.quantizeLSF(lsfQ15, e.bandwidth, signalType)
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)

	// Step 6: Pitch detection and LTP (voiced only)
	var pitchLags []int
	if signalType == 2 {
		pitchLags = e.detectPitch(pcm, numSubframes)
		e.encodePitchLags(pitchLags, numSubframes)

		ltpCoeffs := e.analyzeLTP(pcm, pitchLags, numSubframes)
		periodicity := e.determinePeriodicity(pcm, pitchLags)
		e.encodeLTPCoeffs(ltpCoeffs, periodicity, numSubframes)
	}

	// Step 6.5: LBRR Encoding (FEC)
	// Per libopus: LBRR is encoded AFTER VAD but BEFORE main frame encoding
	// Reference: silk/float/encode_frame_FLP.c silk_LBRR_encode_FLP call
	// Determine conditional coding mode
	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}
	e.lbrrEncode(pcm, lpcQ12, lsfQ15, gains, pitchLags, signalType, quantOffset, speechActivityQ8, condCoding)

	// Step 7: Encode seed (LAST in indices, BEFORE pulses)
	// Per libopus: seed = frameCounter++ & 3
	seed := e.frameCounter & 3
	e.frameCounter++
	e.rangeEncoder.EncodeICDF16(seed, ICDFLCGSeed, 8)

	// Step 8: Compute excitation using Noise Shaping Quantization (NSQ)
	// Per libopus silk_encode_pulses(), pulses are encoded for full frame_length
	subframeSamples := config.SubframeSamples
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	// Use NSQ for proper noise-shaped quantization
	allExcitation := e.computeNSQExcitation(pcm, lpcQ12, gains, pitchLags, signalType, quantOffset, seed, numSubframes, subframeSamples, frameSamples)

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
func (e *Encoder) computeNSQExcitation(pcm []float32, lpcQ12 []int16, gains []float32, pitchLags []int, signalType, quantOffset, seed, numSubframes, subframeSamples, frameSamples int) []int32 {
	// Convert PCM to int16 for NSQ
	inputQ0 := make([]int16, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		// Scale float to int16 range
		val := pcm[i] * 32767.0
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		inputQ0[i] = int16(val)
	}

	// Convert gains to Q16 format
	gainsQ16 := make([]int32, numSubframes)
	for i := 0; i < numSubframes && i < len(gains); i++ {
		gainsQ16[i] = int32(gains[i] * 65536.0)
		if gainsQ16[i] < 1 {
			gainsQ16[i] = 1 // Minimum gain
		}
	}

	// Prepare pitch lags (default to 0 for unvoiced)
	pitchL := make([]int, numSubframes)
	if pitchLags != nil {
		copy(pitchL, pitchLags)
	}

	// Compute noise shaping AR coefficients from LPC
	// For simplicity, use the LPC coefficients with bandwidth expansion
	shapeLPCOrder := len(lpcQ12)
	if shapeLPCOrder > maxShapeLpcOrder {
		shapeLPCOrder = maxShapeLpcOrder
	}

	// Create shaping coefficients (Q13) from LPC (Q12)
	arShpQ13 := make([]int16, numSubframes*maxShapeLpcOrder)
	for sf := 0; sf < numSubframes; sf++ {
		for i := 0; i < shapeLPCOrder && i < len(lpcQ12); i++ {
			// Convert Q12 to Q13 with bandwidth expansion (0.94^(i+1))
			// This shapes the quantization noise spectrum
			arShpQ13[sf*maxShapeLpcOrder+i] = int16(int32(lpcQ12[i]) * 2 * 94 / 100)
		}
	}

	// LTP coefficients (Q14) - simplified, use default for unvoiced
	ltpCoefQ14 := make([]int16, numSubframes*ltpOrderConst)
	if signalType == typeVoiced {
		// Default LTP coefficients for voiced (center tap dominant)
		for sf := 0; sf < numSubframes; sf++ {
			ltpCoefQ14[sf*ltpOrderConst+2] = 8192 // Center tap = 0.5 in Q14
		}
	}

	// Prediction coefficients - replicate for both subframe groups
	predCoefQ12 := make([]int16, 2*maxLPCOrder)
	for i := 0; i < len(lpcQ12) && i < maxLPCOrder; i++ {
		predCoefQ12[i] = lpcQ12[i]
		predCoefQ12[maxLPCOrder+i] = lpcQ12[i]
	}

	// Harmonic shaping gain (Q14) - based on voicing
	harmShapeGainQ14 := make([]int, numSubframes)
	tiltQ14 := make([]int, numSubframes)
	lfShpQ14 := make([]int32, numSubframes)
	for sf := 0; sf < numSubframes; sf++ {
		if signalType == typeVoiced {
			harmShapeGainQ14[sf] = 4096 // Moderate harmonic shaping
			tiltQ14[sf] = -2048         // Slight high-frequency emphasis
		} else {
			harmShapeGainQ14[sf] = 0
			tiltQ14[sf] = -4096 // More tilt for unvoiced
		}
		lfShpQ14[sf] = 512 // Low-frequency shaping
	}

	// Lambda (rate-distortion tradeoff) - higher = more aggressive quantization
	lambdaQ10 := 512 // Moderate R-D tradeoff

	// LTP scale for first subframe
	ltpScaleQ14 := silk_LTPScales_table_Q14[1] // Middle value

	// Set up NSQ parameters
	params := &NSQParams{
		SignalType:       signalType,
		QuantOffsetType:  quantOffset,
		PredCoefQ12:      predCoefQ12,
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

	// Convert pulses to int32 for encoding
	excitation := make([]int32, frameSamples)
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
	// Determine frames per packet based on input size
	config := GetBandwidthConfig(e.bandwidth)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame
	nFrames := len(pcm) / frameSamples
	if nFrames < 1 {
		nFrames = 1
	}
	if nFrames > maxFramesPerPacket {
		nFrames = maxFramesPerPacket
	}
	e.nFramesPerPacket = nFrames
	e.nFramesEncoded = 0

	// Create range encoder
	bufSize := len(pcm)/2 + 100
	if bufSize < 150 {
		bufSize = 150
	}
	if bufSize > 400 {
		bufSize = 400
	}
	output := make([]byte, bufSize)
	e.rangeEncoder = &rangecoding.Encoder{}
	e.rangeEncoder.Init(output)

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
	e.encodeLBRRData(e.rangeEncoder, 1) // nChannels = 1 for mono

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
	numSubframes := 4 // 20ms frame = 4 subframes

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

	// Step 3: Compute and encode gains
	gains := e.computeSubframeGains(pcm, numSubframes)
	e.encodeSubframeGains(gains, signalType, numSubframes)

	// Step 4: Compute LPC coefficients
	lpcQ12 := e.computeLPCFromFrame(pcm)

	// Step 5: Convert to LSF and quantize
	lsfQ15 := lpcToLSFEncode(lpcQ12)
	stage1Idx, residuals, interpIdx := e.quantizeLSF(lsfQ15, e.bandwidth, signalType)
	e.encodeLSF(stage1Idx, residuals, interpIdx, e.bandwidth, signalType)

	// Step 6: Pitch detection and LTP (voiced only)
	var pitchLags []int
	if signalType == 2 {
		pitchLags = e.detectPitch(pcm, numSubframes)
		e.encodePitchLags(pitchLags, numSubframes)

		ltpCoeffs := e.analyzeLTP(pcm, pitchLags, numSubframes)
		periodicity := e.determinePeriodicity(pcm, pitchLags)
		e.encodeLTPCoeffs(ltpCoeffs, periodicity, numSubframes)
	}

	// Step 6.5: LBRR Encoding (FEC) for this frame
	condCoding := codeIndependently
	if e.nFramesEncoded > 0 {
		condCoding = codeConditionally
	}
	e.lbrrEncode(pcm, lpcQ12, lsfQ15, gains, pitchLags, signalType, quantOffset, speechActivityQ8, condCoding)

	// Step 7: Encode seed
	seed := e.frameCounter & 3
	e.frameCounter++
	e.rangeEncoder.EncodeICDF16(seed, ICDFLCGSeed, 8)

	// Step 8: Compute and encode excitation
	subframeSamples := config.SubframeSamples
	frameSamples := numSubframes * subframeSamples
	if frameSamples > len(pcm) {
		frameSamples = len(pcm)
	}

	allExcitation := e.computeNSQExcitation(pcm, lpcQ12, gains, pitchLags, signalType, quantOffset, seed, numSubframes, subframeSamples, frameSamples)
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
