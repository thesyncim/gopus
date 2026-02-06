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
	midOut, sideOut, ix, midOnly, _, _, _ := enc.StereoLRToMSWithRates(
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

// DecodeStereoEncoded decodes a range-coded SILK stereo packet back to
// separate left/right channel float32 slices at 48 kHz.
//
// The input must be a complete SILK stereo bitstream produced by
// EncodeStereoWithEncoder (or the equivalent libopus stereo encoder).
// The packet contains range-coded VAD/LBRR header bits, stereo prediction
// indices, mid and side channel frame data.
//
// Returns left and right channels (each 48 kHz, length = frameSizeSamples).
func DecodeStereoEncoded(encoded []byte, bandwidth Bandwidth) (left, right []float32, err error) {
	if len(encoded) < 2 {
		return nil, nil, ErrInvalidPacket
	}

	// Compute expected 48 kHz frame size from bandwidth (20 ms frame).
	config := GetBandwidthConfig(bandwidth)
	frameSamples := config.SampleRate * 20 / 1000
	frameSizeSamples48 := frameSamples * 48000 / config.SampleRate

	// Use the proper stereo decoder which handles range-coded SILK stereo
	// packets (VAD/LBRR header, stereo prediction, mid/side frames, MS-to-LR).
	decoder := NewDecoder()
	interleaved, err := decoder.DecodeStereo(encoded, bandwidth, frameSizeSamples48, true)
	if err != nil {
		return nil, nil, err
	}

	// De-interleave [L0, R0, L1, R1, ...] into separate left/right slices.
	n := len(interleaved) / 2
	left = make([]float32, n)
	right = make([]float32, n)
	for i := 0; i < n; i++ {
		left[i] = interleaved[i*2]
		right[i] = interleaved[i*2+1]
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
