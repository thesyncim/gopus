package silk

import "github.com/thesyncim/gopus/rangecoding"

// Encoder encodes PCM audio to SILK frames.
// It maintains state across frames that mirrors the decoder for proper
// synchronized prediction of gains, LSF, and stereo weights.
//
// Reference: RFC 6716 Section 5.2, draft-vos-silk-01
type Encoder struct {
	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// lastRng holds the final range coder state after encoding.
	// This is captured before calling Done() which clears the state.
	lastRng uint32

	// Frame state (persists across frames, mirrors decoder)
	haveEncoded           bool  // True after first frame encoded
	previousLogGain       int32 // Last subframe gain (for delta coding)
	isPreviousFrameVoiced bool  // Was previous frame voiced
	frameCounter          int   // Frame counter for seed generation (seed = frameCounter & 3)

	// LPC state
	lpcOrder   int     // Current LPC order (10 for NB/MB, 16 for WB)
	prevLSFQ15 []int16 // Previous frame LSF (Q15) for interpolation

	// Stereo state
	prevStereoWeights [2]int16 // Previous w0, w1 stereo weights (Q13)
	stereo            stereoEncState // Full stereo encoder state for LP filtering

	// Pitch analysis state
	pitchState PitchAnalysisState // State for pitch estimation across frames

	// NSQ (Noise Shaping Quantization) state
	nsqState *NSQState // Noise shaping quantizer state for proper libopus-matching

	// Analysis buffers (encoder-specific)
	inputBuffer []float32 // Buffered input samples
	lpcState    []float32 // LPC filter state for residual computation

	// Bandwidth configuration
	bandwidth  Bandwidth
	sampleRate int
}

// NewEncoder creates a new SILK encoder with proper initial state.
func NewEncoder(bandwidth Bandwidth) *Encoder {
	config := GetBandwidthConfig(bandwidth)
	// Frame samples = sampleRate * 20ms / 1000
	frameSamples := config.SampleRate * 20 / 1000
	return &Encoder{
		prevLSFQ15:  make([]int16, config.LPCOrder),
		inputBuffer: make([]float32, frameSamples*2), // Look-ahead buffer
		lpcState:    make([]float32, config.LPCOrder),
		nsqState:    NewNSQState(), // Initialize NSQ state
		bandwidth:   bandwidth,
		sampleRate:  config.SampleRate,
		lpcOrder:    config.LPCOrder,
	}
}

// Reset clears encoder state for a new stream.
func (e *Encoder) Reset() {
	e.haveEncoded = false
	e.previousLogGain = 0
	e.isPreviousFrameVoiced = false

	for i := range e.prevLSFQ15 {
		e.prevLSFQ15[i] = 0
	}
	for i := range e.lpcState {
		e.lpcState[i] = 0
	}
	for i := range e.inputBuffer {
		e.inputBuffer[i] = 0
	}
	e.prevStereoWeights = [2]int16{0, 0}
	e.stereo = stereoEncState{} // Reset LP filter state
	e.pitchState = PitchAnalysisState{} // Reset pitch state
	if e.nsqState != nil {
		e.nsqState.Reset() // Reset NSQ state
	}
}

// SetRangeEncoder sets the range encoder for the current frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// HaveEncoded returns whether at least one frame has been encoded.
func (e *Encoder) HaveEncoded() bool {
	return e.haveEncoded
}

// MarkEncoded marks that a frame has been successfully encoded.
func (e *Encoder) MarkEncoded() {
	e.haveEncoded = true
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.bandwidth
}

// LPCOrder returns the LPC order for current bandwidth.
func (e *Encoder) LPCOrder() int {
	return e.lpcOrder
}

// SampleRate returns the sample rate for current bandwidth.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// PreviousLogGain returns the previous frame's log gain value.
func (e *Encoder) PreviousLogGain() int32 {
	return e.previousLogGain
}

// SetPreviousLogGain sets the log gain for delta coding.
func (e *Encoder) SetPreviousLogGain(gain int32) {
	e.previousLogGain = gain
}

// IsPreviousFrameVoiced returns whether the previous frame was voiced.
func (e *Encoder) IsPreviousFrameVoiced() bool {
	return e.isPreviousFrameVoiced
}

// SetPreviousFrameVoiced sets the voiced state for the previous frame.
func (e *Encoder) SetPreviousFrameVoiced(voiced bool) {
	e.isPreviousFrameVoiced = voiced
}

// PrevLSFQ15 returns the previous frame's LSF coefficients.
func (e *Encoder) PrevLSFQ15() []int16 {
	return e.prevLSFQ15
}

// SetPrevLSFQ15 copies LSF coefficients for interpolation with next frame.
func (e *Encoder) SetPrevLSFQ15(lsf []int16) {
	copy(e.prevLSFQ15, lsf)
}

// PrevStereoWeights returns the previous stereo weights.
func (e *Encoder) PrevStereoWeights() [2]int16 {
	return e.prevStereoWeights
}

// SetPrevStereoWeights sets the stereo weights for the next frame.
func (e *Encoder) SetPrevStereoWeights(weights [2]int16) {
	e.prevStereoWeights = weights
}

// InputBuffer returns the input sample buffer.
func (e *Encoder) InputBuffer() []float32 {
	return e.inputBuffer
}

// LPCState returns the LPC filter state.
func (e *Encoder) LPCState() []float32 {
	return e.lpcState
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after EncodeFrame() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	return e.lastRng
}

// NSQState returns the noise shaping quantizer state.
func (e *Encoder) NSQState() *NSQState {
	return e.nsqState
}
