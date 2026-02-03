// Package silk implements LBRR (Low Bitrate Redundancy) encoding for FEC.
// LBRR provides forward error correction by including redundant data
// for the previous frame at a lower quality in the current packet.
//
// Reference: libopus silk/encode_frame_FLP.c silk_LBRR_encode_FLP
package silk

import (
	"github.com/thesyncim/gopus/rangecoding"
)

// lbrrSpeechActivityThresholdQ8 is the minimum speech activity for LBRR.
// Frames with lower activity are not LBRR-encoded.
// Reference: libopus tuning_parameters.h LBRR_SPEECH_ACTIVITY_THRES
const lbrrSpeechActivityThresholdQ8 = 128 // 0.5 in Q8

// lbrrEncode encodes LBRR (low bitrate redundancy) data for the current frame.
// This creates a lower-quality version of the frame that can be used for
// packet loss concealment by the decoder.
//
// LBRR is encoded with:
// - Same LPC/LSF coefficients as the main frame
// - Higher gains (to reduce bits used for excitation)
// - Same pitch parameters for voiced frames
// - Requantized excitation at lower quality
//
// Reference: libopus silk/float/encode_frame_FLP.c silk_LBRR_encode_FLP
func (e *Encoder) lbrrEncode(
	pcm []float32,
	lpcQ12 []int16,
	lsfQ15 []int16,
	gains []float32,
	pitchLags []int,
	signalType, quantOffset int,
	speechActivityQ8 int,
	condCoding int,
) {
	if !e.lbrrEnabled {
		return
	}

	// Only encode LBRR for frames with sufficient speech activity
	if speechActivityQ8 < lbrrSpeechActivityThresholdQ8 {
		e.lbrrFlags[e.nFramesEncoded] = 0
		return
	}

	// Mark this frame for LBRR
	e.lbrrFlags[e.nFramesEncoded] = 1

	// Copy indices from main encoding
	e.lbrrIndices[e.nFramesEncoded] = sideInfoIndices{
		signalType:      int8(signalType),
		quantOffsetType: int8(quantOffset),
	}

	// Copy NLSF indices (LSF quantization already done in main encode)
	// The LBRR uses the same LSF parameters

	// Adjust gains for LBRR - increase gain to reduce excitation bits
	// Reference: libopus silk_LBRR_encode_FLP
	lbrrGainsQ16 := ensureInt32Slice(&e.scratchGainsQ16, len(gains))
	for i := range gains {
		lbrrGainsQ16[i] = int32(gains[i] * 65536.0)
	}

	// Determine if we need independent or conditional coding for LBRR
	lbrrCondCoding := codeIndependently
	if e.nFramesEncoded > 0 && e.lbrrFlags[e.nFramesEncoded-1] != 0 {
		lbrrCondCoding = codeConditionally
	}

	// For first LBRR frame or after non-LBRR, increase first gain
	if e.nFramesEncoded == 0 || e.lbrrFlags[e.nFramesEncoded-1] == 0 {
		// Save gain index for next LBRR frame
		e.lbrrPrevLastGainIdx = e.lbrrIndices[e.nFramesEncoded].GainsIndices[0]

		// Increase gain by LBRR_GainIncreases steps
		gainIdx := int(e.lbrrIndices[e.nFramesEncoded].GainsIndices[0])
		gainIdx += e.lbrrGainIncreases
		if gainIdx > nLevelsQGain-1 {
			gainIdx = nLevelsQGain - 1
		}
		e.lbrrIndices[e.nFramesEncoded].GainsIndices[0] = int8(gainIdx)
	}

	// Dequantize LBRR gains to get actual values for NSQ
	e.decodeLBRRGains(lbrrGainsQ16, lbrrCondCoding)

	// Compute LBRR excitation using NSQ with higher gains
	config := GetBandwidthConfig(e.bandwidth)
	subframeSamples := config.SubframeSamples
	frameSamples := len(pcm)
	if frameSamples <= 0 {
		return
	}
	numSubframes := frameSamples / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
		subframeSamples = frameSamples
	}
	if numSubframes > maxNbSubfr {
		numSubframes = maxNbSubfr
	}
	frameSamples = numSubframes * subframeSamples

	// Prepare pitch lags
	pitchL := ensureIntSlice(&e.scratchPitchL, numSubframes)
	for i := range pitchL {
		pitchL[i] = 0
	}
	if pitchLags != nil {
		copy(pitchL, pitchLags)
	}

	// Use the same LPC coefficients but with increased gains
	e.computeLBRRPulses(pcm, lpcQ12, lbrrGainsQ16, pitchL, signalType, quantOffset, frameSamples, numSubframes, subframeSamples)
}

// decodeLBRRGains dequantizes LBRR gain indices to Q16 gains.
// This ensures gains are in sync with what the decoder will compute.
func (e *Encoder) decodeLBRRGains(gainsQ16 []int32, condCoding int) {
	indices := &e.lbrrIndices[e.nFramesEncoded]

	// Use gains_dequant logic
	// Reference: libopus silk/gain_quant.c silk_gains_dequant

	var prevGainIdx int
	if condCoding == codeConditionally {
		prevGainIdx = int(e.lbrrPrevLastGainIdx)
	} else {
		prevGainIdx = int(indices.GainsIndices[0])
	}

	for i := 0; i < len(gainsQ16); i++ {
		var gainIdx int
		if i == 0 && condCoding != codeConditionally {
			gainIdx = int(indices.GainsIndices[i])
		} else {
			gainIdx = prevGainIdx + int(indices.GainsIndices[i]) - 4
		}

		// Clamp to valid range
		if gainIdx < 0 {
			gainIdx = 0
		}
		if gainIdx >= nLevelsQGain {
			gainIdx = nLevelsQGain - 1
		}

		// Convert gain index to Q16 gain
		// log_gain = min_gain_dB + gain_idx * (max_gain_dB - min_gain_dB) / (N_LEVELS_QGAIN - 1)
		// gain = 10^(log_gain/20) in Q16
		logGainQ7 := int32(minQGainDb*128) + int32(gainIdx)*(int32(maxQGainDb-minQGainDb)*128)/(nLevelsQGain-1)
		gainsQ16[i] = silkLog2Lin(logGainQ7 - 16*128) // Adjust for Q7 to linear conversion

		prevGainIdx = gainIdx
	}

	e.lbrrPrevLastGainIdx = int8(prevGainIdx)
}

// computeLBRRPulses computes the LBRR excitation pulses using NSQ.
func (e *Encoder) computeLBRRPulses(
	pcm []float32,
	lpcQ12 []int16,
	gainsQ16 []int32,
	pitchLags []int,
	signalType, quantOffset int,
	frameSamples, numSubframes, subframeSamples int,
) {
	// Convert PCM to int16 for NSQ
	inputQ0 := ensureInt16Slice(&e.scratchInputQ0, frameSamples)
	for i := 0; i < frameSamples && i < len(pcm); i++ {
		val := pcm[i] * 32767.0
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		inputQ0[i] = int16(val)
	}

	// Create a copy of NSQ state for LBRR (don't modify main state)
	lbrrNSQ := *e.nsqState

	// Compute noise shaping AR coefficients from LPC
	shapeLPCOrder := len(lpcQ12)
	if shapeLPCOrder > maxShapeLpcOrder {
		shapeLPCOrder = maxShapeLpcOrder
	}

	arShpQ13 := ensureInt16Slice(&e.scratchArShpQ13, numSubframes*maxShapeLpcOrder)
	for i := range arShpQ13 {
		arShpQ13[i] = 0
	}
	for sf := 0; sf < numSubframes; sf++ {
		for i := 0; i < shapeLPCOrder && i < len(lpcQ12); i++ {
			// Bandwidth expansion for LBRR (more aggressive)
			arShpQ13[sf*maxShapeLpcOrder+i] = int16(int32(lpcQ12[i]) * 2 * 90 / 100)
		}
	}

	// LTP coefficients (simplified)
	ltpCoefQ14 := ensureInt16Slice(&e.scratchLtpCoefQ14, numSubframes*ltpOrderConst)
	for i := range ltpCoefQ14 {
		ltpCoefQ14[i] = 0
	}
	if signalType == typeVoiced {
		for sf := 0; sf < numSubframes; sf++ {
			ltpCoefQ14[sf*ltpOrderConst+2] = 8192 // Center tap = 0.5 in Q14
		}
	}

	// Prediction coefficients
	predCoefQ12 := ensureInt16Slice(&e.scratchPredCoefQ12, 2*maxLPCOrder)
	for i := range predCoefQ12 {
		predCoefQ12[i] = 0
	}
	for i := 0; i < len(lpcQ12) && i < maxLPCOrder; i++ {
		predCoefQ12[i] = lpcQ12[i]
		predCoefQ12[maxLPCOrder+i] = lpcQ12[i]
	}

	// Shaping parameters (more aggressive for LBRR)
	harmShapeGainQ14 := ensureIntSlice(&e.scratchHarmShapeGainQ14, numSubframes)
	tiltQ14 := ensureIntSlice(&e.scratchTiltQ14, numSubframes)
	lfShpQ14 := ensureInt32Slice(&e.scratchLfShpQ14, numSubframes)
	for i := 0; i < numSubframes; i++ {
		harmShapeGainQ14[i] = 0
		tiltQ14[i] = 0
		lfShpQ14[i] = 0
	}
	for sf := 0; sf < numSubframes; sf++ {
		if signalType == typeVoiced {
			harmShapeGainQ14[sf] = 3072 // Lower than main (more noise tolerance)
			tiltQ14[sf] = -1536
		} else {
			harmShapeGainQ14[sf] = 0
			tiltQ14[sf] = -3072
		}
		lfShpQ14[sf] = 384
	}

	// Lambda (higher = more aggressive quantization for LBRR)
	lambdaQ10 := 768

	// LTP scale
	ltpScaleQ14 := silk_LTPScales_table_Q14[1]

	// Set up NSQ parameters
	params := &NSQParams{
		SignalType:       signalType,
		QuantOffsetType:  quantOffset,
		PredCoefQ12:      predCoefQ12,
		NLSFInterpCoefQ2: 4,
		LTPCoefQ14:       ltpCoefQ14,
		ARShpQ13:         arShpQ13,
		HarmShapeGainQ14: harmShapeGainQ14,
		TiltQ14:          tiltQ14,
		LFShpQ14:         lfShpQ14,
		GainsQ16:         gainsQ16,
		PitchL:           pitchLags,
		LambdaQ10:        lambdaQ10,
		LTPScaleQ14:      int(ltpScaleQ14),
		FrameLength:      frameSamples,
		SubfrLength:      subframeSamples,
		NbSubfr:          numSubframes,
		LTPMemLength:     ltpMemLength,
		PredLPCOrder:     len(lpcQ12),
		ShapeLPCOrder:    shapeLPCOrder,
		Seed:             e.frameCounter & 3,
	}

	// Run NSQ for LBRR
	pulses, _ := NoiseShapeQuantize(&lbrrNSQ, inputQ0, params)

	// Store LBRR pulses
	for i := 0; i < len(pulses) && i < len(e.lbrrPulses[e.nFramesEncoded]); i++ {
		e.lbrrPulses[e.nFramesEncoded][i] = pulses[i]
	}
}

// encodeLBRRData encodes the LBRR flags and data at the start of the packet.
// This should be called at the beginning of the first frame encoding.
//
// Reference: libopus silk/enc_API.c lines 355-405
func (e *Encoder) encodeLBRRData(re *rangecoding.Encoder, nChannels int, includeHeader bool) {
	if e.nFramesEncoded != 0 {
		// LBRR is only encoded at the start of the packet
		return
	}

	if includeHeader {
		// Create space at start of payload for VAD and FEC flags
		// This is done by encoding a placeholder that will be patched later
		iCDF := []uint16{
			uint16(256 - (256 >> ((e.nFramesPerPacket + 1) * nChannels))),
			0,
		}
		re.EncodeICDF16(0, iCDF, 8)
	}

	// Encode LBRR flags
	lbrrSymbol := 0
	for i := 0; i < e.nFramesPerPacket; i++ {
		lbrrSymbol |= e.lbrrFlags[i] << i
	}

	// Set the overall LBRR flag
	lbrrFlag := 0
	if lbrrSymbol > 0 {
		lbrrFlag = 1
	}
	e.lbrrFlag = lbrrFlag

	// If LBRR is present and there are multiple frames, encode the flags
	if lbrrFlag != 0 && e.nFramesPerPacket > 1 {
		// Use silk_LBRR_flags_iCDF_ptr
		re.EncodeICDF(lbrrSymbol-1, silk_LBRR_flags_iCDF_ptr[e.nFramesPerPacket-2], 8)
	}

	// Encode LBRR indices and pulses for each frame
	for i := 0; i < e.nFramesPerPacket; i++ {
		if e.lbrrFlags[i] == 0 {
			continue
		}

		condCoding := codeIndependently
		if i > 0 && e.lbrrFlags[i-1] != 0 {
			condCoding = codeConditionally
		}

		// Encode LBRR indices
		e.encodeLBRRIndices(re, i, condCoding)

		// Encode LBRR pulses
		e.encodeLBRRPulses(re, i)
	}

	// Clear LBRR flags after encoding (they apply to the previous packet)
	for i := range e.lbrrFlags {
		e.lbrrFlags[i] = 0
	}
}

// EncodeLBRRData encodes LBRR data with optional header placeholder.
// If includeHeader is false, the caller is responsible for reserving header bits.
func (e *Encoder) EncodeLBRRData(re *rangecoding.Encoder, nChannels int, includeHeader bool) {
	e.encodeLBRRData(re, nChannels, includeHeader)
}

// encodeLBRRIndices encodes the LBRR indices for a single frame.
func (e *Encoder) encodeLBRRIndices(re *rangecoding.Encoder, frameIdx, condCoding int) {
	indices := &e.lbrrIndices[frameIdx]

	// Encode gains
	// For LBRR, we use the same structure as regular indices encoding
	signalType := int(indices.signalType)

	// Encode gain indices
	if condCoding == codeConditionally {
		// Delta coding for gains
		for i := 0; i < 4; i++ {
			re.EncodeICDF16(int(indices.GainsIndices[i]), ICDFDeltaGain, 8)
		}
	} else {
		// Absolute gain for first subframe
		var gainICDF []uint16
		switch signalType {
		case typeNoVoiceActivity:
			gainICDF = ICDFGainMSBInactive
		case typeUnvoiced:
			gainICDF = ICDFGainMSBUnvoiced
		default:
			gainICDF = ICDFGainMSBVoiced
		}
		// Encode MSB
		msb := int(indices.GainsIndices[0]) >> 3
		re.EncodeICDF16(msb, gainICDF, 8)
		// Encode LSB
		lsb := int(indices.GainsIndices[0]) & 7
		re.EncodeICDF16(lsb, ICDFGainLSB, 8)

		// Delta for remaining subframes
		for i := 1; i < 4; i++ {
			re.EncodeICDF16(int(indices.GainsIndices[i]), ICDFDeltaGain, 8)
		}
	}

	// For LBRR, we skip most other indices as they're inherited from main encoding
	// The decoder will use the same LSF/pitch parameters from the main frame
}

// encodeLBRRPulses encodes the LBRR pulses for a single frame.
func (e *Encoder) encodeLBRRPulses(re *rangecoding.Encoder, frameIdx int) {
	pulses := e.lbrrPulses[frameIdx]
	signalType := int(e.lbrrIndices[frameIdx].signalType)
	quantOffset := int(e.lbrrIndices[frameIdx].quantOffsetType)

	// Convert int8 pulses to int32 for encoding
	pulsesInt32 := ensureInt32Slice(&e.scratchPulses32, len(pulses))
	for i, p := range pulses {
		pulsesInt32[i] = int32(p)
	}

	// Use the standard pulse encoding
	e.encodePulses(pulsesInt32, signalType, quantOffset)
}

// hasLBRRData returns true if there is LBRR data to encode.
func (e *Encoder) hasLBRRData() bool {
	for i := 0; i < e.nFramesPerPacket; i++ {
		if e.lbrrFlags[i] != 0 {
			return true
		}
	}
	return false
}

// HasLBRRData reports whether there is pending LBRR data to encode.
func (e *Encoder) HasLBRRData() bool {
	return e.hasLBRRData()
}

// Note: silk_LBRR_flags_iCDF_ptr is defined in libopus_tables.go
// Note: silkLog2Lin is defined in libopus_log.go
