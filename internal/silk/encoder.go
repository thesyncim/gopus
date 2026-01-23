package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// Encoder encodes PCM audio to SILK frames.
// It maintains state across frames that mirrors the decoder for proper
// synchronized prediction of gains, LSF, and stereo weights.
//
// Reference: RFC 6716 Section 5.2, draft-vos-silk-01
type Encoder struct {
	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// Frame state (persists across frames, mirrors decoder)
	haveEncoded           bool  // True after first frame encoded
	previousLogGain       int32 // Last subframe gain (for delta coding)
	isPreviousFrameVoiced bool  // Was previous frame voiced

	// LPC state
	lpcOrder   int     // Current LPC order (10 for NB/MB, 16 for WB)
	prevLSFQ15 []int16 // Previous frame LSF (Q15) for interpolation

	// Stereo state
	prevStereoWeights [2]int16 // Previous w0, w1 stereo weights (Q13)

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
