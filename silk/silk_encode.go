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

// EncodeStereoWithEncoder encodes stereo PCM audio using pre-existing encoders.
// This is the zero-allocation version of EncodeStereo.
// enc: Pre-allocated encoder for mid channel
// sideEnc: Pre-allocated encoder for side channel (can be nil, will be created)
// left, right: Input samples for each channel
// bandwidth: Target bandwidth
// vadFlag: True if frame contains voice activity
// Returns: Encoded SILK frame bytes for combined mid/side channels
func EncodeStereoWithEncoder(enc, sideEnc *Encoder, left, right []float32, bandwidth Bandwidth, vadFlag bool) ([]byte, error) {
	// Get frame length and sample rate for LP/HP filtering
	config := GetBandwidthConfig(bandwidth)
	frameLength := len(left)
	fsKHz := config.SampleRate / 1000

	// Convert to mid-side with LP/HP filtering and compute stereo weights
	// This matches libopus stereo_LR_to_MS.c by:
	// 1. Computing LP and HP filtered versions of mid/side
	// 2. Computing separate predictors for LP and HP bands
	// 3. Providing proper predictor values for the decoder
	midWithHistory, sideWithHistory, weights := enc.EncodeStereoLRToMS(left, right, frameLength, fsKHz)

	// Extract frame data (skip 1 history sample offset due to LP filter alignment)
	// The output has frameLength+2 samples with history at the start
	var mid, side []float32
	if len(midWithHistory) >= frameLength+1 {
		mid = midWithHistory[1 : frameLength+1]
	} else {
		mid = midWithHistory
	}
	if len(sideWithHistory) >= frameLength+1 {
		side = sideWithHistory[1 : frameLength+1]
	} else {
		side = sideWithHistory
	}

	// Encode mid channel (primary)
	midBytes := enc.EncodeFrame(mid, nil, vadFlag)

	// Encode side channel (secondary, typically lower bitrate)
	if sideEnc == nil {
		sideEnc = NewEncoder(bandwidth)
	}
	sideBytes := sideEnc.EncodeFrame(side, nil, vadFlag)

	// Combine mid and side into single output
	// Format: [weights:4][mid_len:2][mid_bytes][side_len:2][side_bytes]
	// Weights first so decoder can apply prediction during unmixing
	result := make([]byte, 4+2+len(midBytes)+2+len(sideBytes))

	// Write stereo weights (Q13 format) at start
	result[0] = byte(weights[0] >> 8)
	result[1] = byte(weights[0])
	result[2] = byte(weights[1] >> 8)
	result[3] = byte(weights[1])

	// Write mid length and data
	result[4] = byte(len(midBytes) >> 8)
	result[5] = byte(len(midBytes))
	copy(result[6:], midBytes)

	// Write side length and data
	offset := 6 + len(midBytes)
	result[offset] = byte(len(sideBytes) >> 8)
	result[offset+1] = byte(len(sideBytes))
	copy(result[offset+2:], sideBytes)

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
