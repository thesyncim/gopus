package silk

import "errors"

// ErrInvalidPacket indicates the packet data is malformed.
var ErrInvalidPacket = errors.New("silk: invalid packet")

// Encode encodes mono PCM audio to SILK frame.
// pcm: Input samples at encoder's configured sample rate
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes
//
// Note: This function allocates a new encoder per call.
// For zero-allocation encoding, use EncodeWithEncoder.
func Encode(pcm []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	enc := NewEncoder(bandwidth)
	return enc.EncodeFrame(pcm, nil, vadFlag), nil
}

// EncodeWithEncoder encodes mono PCM audio using a pre-existing encoder.
// This is the zero-allocation version of Encode.
// enc: Pre-allocated encoder (use silk.NewEncoder to create)
// pcm: Input samples at encoder's configured sample rate
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes
func EncodeWithEncoder(enc *Encoder, pcm []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	return enc.EncodeFrame(pcm, nil, vadFlag), nil
}

// EncodeStereo encodes stereo PCM audio to SILK frame.
// left, right: Input samples for each channel
// bandwidth: Target bandwidth
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes for combined mid/side channels
//
// Note: This function allocates new encoders per call.
// For zero-allocation encoding, use EncodeStereoWithEncoder.
func EncodeStereo(left, right []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	enc := NewEncoder(bandwidth)
	sideEnc := NewEncoder(bandwidth)
	return EncodeStereoWithEncoder(enc, sideEnc, left, right, bandwidth, vadFlag)
}

// EncodeStereoWithEncoder encodes stereo PCM audio using pre-existing encoders
// into a single SILK stereo bitstream that matches the libopus format.
//
// The output is a valid SILK stereo packet containing:
//   - VAD/LBRR header bits for both channels (patched at end)
//   - LBRR data for both channels
//   - Stereo prediction indices (range coded)
//   - Mid-only flag (when side VAD is inactive)
//   - Mid channel frame data (range coded)
//   - Side channel frame data (range coded, if not mid-only)
//
// This matches libopus enc_API.c silk_Encode() for nChannelsInternal == 2.
//
// enc: Pre-allocated encoder for mid channel
// sideEnc: Pre-allocated encoder for side channel
// left, right: Input samples for each channel at SILK sample rate
// bandwidth: Target bandwidth
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK stereo frame bytes
func EncodeStereoWithEncoder(enc, sideEnc *Encoder, left, right []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	if sideEnc == nil {
		sideEnc = NewEncoder(bandwidth)
	}

	config := GetBandwidthConfig(bandwidth)
	frameLength := len(left)
	fsKHz := config.SampleRate / 1000

	// Reset packet state for both encoders
	enc.ResetPacketState()
	sideEnc.ResetPacketState()
	enc.nFramesPerPacket = 1
	sideEnc.nFramesPerPacket = 1

	// Use the mid channel's speech activity for stereo decision.
	speechActQ8 := enc.speechActivityQ8
	if !enc.speechActivitySet {
		speechActQ8 = 200
	}

	// Compute total bitrate for stereo rate allocation.
	totalRate := enc.targetRateBps
	if totalRate <= 0 {
		totalRate = 20000
	}

	// Create shared range encoder for the stereo packet.
	bufSize := frameLength + 200
	if bufSize < maxSilkPacketBytes {
		bufSize = maxSilkPacketBytes
	}
	output := ensureByteSlice(&enc.scratchOutput, bufSize)
	enc.scratchRangeEncoder.Init(output)
	re := &enc.scratchRangeEncoder

	// Reserve header bits for 2 channels: (nFramesPerPacket + 1) * 2 = 4 bits
	// This reserves space for VAD flags + LBRR flags for both channels.
	nChannels := 2
	nFrames := 1
	headerBits := (nFrames + 1) * nChannels
	iCDF := []uint16{
		uint16(256 - (256 >> headerBits)),
		0,
	}
	re.EncodeICDF16(0, iCDF, 8)

	// Encode LBRR data for mid channel.
	// For now, just encode empty LBRR flags (no FEC data).
	midLBRRSymbol := 0
	for i := 0; i < nFrames; i++ {
		midLBRRSymbol |= enc.lbrrFlags[i] << i
	}
	midLBRRFlag := 0
	if midLBRRSymbol > 0 {
		midLBRRFlag = 1
	}
	enc.lbrrFlag = midLBRRFlag

	sideLBRRSymbol := 0
	for i := 0; i < nFrames; i++ {
		sideLBRRSymbol |= sideEnc.lbrrFlags[i] << i
	}
	sideLBRRFlag := 0
	if sideLBRRSymbol > 0 {
		sideLBRRFlag = 1
	}
	sideEnc.lbrrFlag = sideLBRRFlag

	// Encode LBRR flags and data for mid
	if midLBRRFlag != 0 && nFrames > 1 {
		re.EncodeICDF(midLBRRSymbol-1, silk_LBRR_flags_iCDF_ptr[nFrames-2], 8)
	}
	// Encode LBRR flags and data for side
	if sideLBRRFlag != 0 && nFrames > 1 {
		re.EncodeICDF(sideLBRRSymbol-1, silk_LBRR_flags_iCDF_ptr[nFrames-2], 8)
	}

	// Clear LBRR flags after encoding
	for i := range enc.lbrrFlags {
		enc.lbrrFlags[i] = 0
	}
	for i := range sideEnc.lbrrFlags {
		sideEnc.lbrrFlags[i] = 0
	}

	// Ensure we have lookahead samples (need frameLength + 2 for LP filter).
	if len(left) < frameLength+2 {
		pad := make([]float32, frameLength+2)
		copy(pad, left)
		left = pad
	}
	if len(right) < frameLength+2 {
		pad := make([]float32, frameLength+2)
		copy(pad, right)
		right = pad
	}

	// Convert L/R to M/S with stereo prediction, rate allocation, and width decision.
	// This matches libopus silk_stereo_LR_to_MS.
	midOut, sideOut, ix, midOnly, midRate, sideRate, _ := enc.StereoLRToMSWithRates(
		left, right, frameLength, fsKHz,
		totalRate, speechActQ8, false,
	)

	// Encode stereo prediction indices into the shared range encoder.
	EncodeStereoIndices(re, ix)

	// Encode mid-only flag if side VAD is inactive.
	// In libopus, mid-only is encoded when side VAD flag is 0.
	// For simplicity, if we decided mid-only, encode it. Otherwise the side is coded.
	sideVAD := vadFlag && !midOnly
	if !sideVAD {
		midOnlyVal := 0
		if midOnly {
			midOnlyVal = 1
		}
		EncodeStereoMidOnly(re, midOnlyVal)
	}

	// Set per-channel bitrates from the stereo rate allocation.
	// In libopus enc_API.c line 502-513, each channel gets its own channelRate_bps
	// from MStargetRates_bps[], and silk_control_SNR is called with that rate.
	if midOnly {
		enc.SetBitrate(totalRate)
	} else {
		if midRate > 0 {
			enc.SetBitrate(midRate)
		}
		if sideRate > 0 {
			sideEnc.SetBitrate(sideRate)
		}
	}

	// In libopus, when side is active, the mid channel is forced non-CBR
	// and maxBits is reduced to leave room for side. Matches enc_API.c line 503-507:
	//   useCBR = 0; maxBits -= encControl->maxBits / (tot_blocks * 2);
	if !midOnly && sideRate > 0 && enc.maxBits > 0 {
		enc.maxBits -= enc.maxBits / 2
	}

	// Set up shared range encoder for mid channel encoding.
	enc.SetRangeEncoder(re)
	_ = enc.EncodeFrame(midOut, nil, vadFlag)

	// Encode side channel if not mid-only.
	if !midOnly {
		sideEnc.SetRangeEncoder(re)
		_ = sideEnc.EncodeFrame(sideOut, nil, vadFlag)
	} else {
		// When mid-only, the side VAD flag should be 0.
		// We still need to track state properly.
		sideVAD = false
	}

	// Patch header bits: VAD flags + LBRR flags for both channels.
	// Format: [mid_VAD | mid_LBRR | side_VAD | side_LBRR]
	// For 1 frame per packet: (1+1) * 2 = 4 bits
	flags := uint32(0)
	// Mid channel: VAD flag + LBRR flag
	if vadFlag {
		flags = 1
	}
	flags = (flags << 1) | uint32(midLBRRFlag&1)
	// Side channel: VAD flag + LBRR flag
	flags <<= 1
	if sideVAD {
		flags |= 1
	}
	flags = (flags << 1) | uint32(sideLBRRFlag&1)
	re.PatchInitialBits(flags, uint(headerBits))

	// Capture final range state.
	enc.lastRng = re.Range()

	// Finalize the range encoder.
	raw := re.Done()
	result := make([]byte, len(raw))
	copy(result, raw)

	// Clean up shared encoder references.
	enc.rangeEncoder = nil
	sideEnc.rangeEncoder = nil

	return result, nil
}

// DecodeStereoEncoded decodes stereo SILK frame back to left/right channels.
// Uses Phase 2 decoder for mid/side, then reconstructs stereo.
func DecodeStereoEncoded(encoded []byte, bandwidth Bandwidth) (left, right []float32, err error) {
	if len(encoded) < 8 {
		return nil, nil, ErrInvalidPacket
	}

	// Parse stereo weights (Q13 format) at start
	weights := [2]int16{
		int16(encoded[0])<<8 | int16(encoded[1]),
		int16(encoded[2])<<8 | int16(encoded[3]),
	}

	// Parse mid length and data
	midLen := int(encoded[4])<<8 | int(encoded[5])
	if 6+midLen > len(encoded) {
		return nil, nil, ErrInvalidPacket
	}
	midBytes := encoded[6 : 6+midLen]

	// Parse side length and data
	offset := 6 + midLen
	if offset+2 > len(encoded) {
		return nil, nil, ErrInvalidPacket
	}
	sideLen := int(encoded[offset])<<8 | int(encoded[offset+1])
	if offset+2+sideLen > len(encoded) {
		return nil, nil, ErrInvalidPacket
	}
	sideBytes := encoded[offset+2 : offset+2+sideLen]

	// Decode mid and side channels using Phase 2 decoder
	config := GetBandwidthConfig(bandwidth)
	frameSamples := config.SampleRate * 20 / 1000 // 20ms frame

	decoder := NewDecoder()
	mid, err := decoder.Decode(midBytes, bandwidth, frameSamples*48000/config.SampleRate, true)
	if err != nil {
		return nil, nil, err
	}

	sideDecoder := NewDecoder()
	side, err := sideDecoder.Decode(sideBytes, bandwidth, frameSamples*48000/config.SampleRate, true)
	if err != nil {
		return nil, nil, err
	}

	// Reconstruct left/right from mid/side
	n := len(mid)
	if len(side) < n {
		n = len(side)
	}

	left = make([]float32, n)
	right = make([]float32, n)

	// Apply stereo weights to reconstruct
	// Prediction: side_pred = w0*mid + w1*mid_prev
	// Residual side is encoded separately
	w0 := float32(weights[0]) / 8192.0
	w1 := float32(weights[1]) / 8192.0

	for i := 0; i < n; i++ {
		var midPrev float32
		if i > 0 {
			midPrev = mid[i-1]
		}
		sidePred := w0*mid[i] + w1*midPrev
		sideActual := side[i] + sidePred

		// Convert back to left/right
		left[i] = mid[i] + sideActual
		right[i] = mid[i] - sideActual
	}

	return left, right, nil
}

// EncoderState holds encoder state for streaming encoding.
type EncoderState struct {
	enc *Encoder
}

// NewEncoderState creates a new streaming encoder.
func NewEncoderState(bandwidth Bandwidth) *EncoderState {
	return &EncoderState{
		enc: NewEncoder(bandwidth),
	}
}

// EncodeFrame encodes a frame maintaining state across calls.
func (es *EncoderState) EncodeFrame(pcm []float32, vadFlag bool) ([]byte, error) {
	return es.enc.EncodeFrame(pcm, nil, vadFlag), nil
}

// Reset resets encoder state for a new stream.
func (es *EncoderState) Reset() {
	es.enc.Reset()
}
