package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

// Decoder decodes SILK frames from an Opus packet.
// It maintains state across frames for proper speech continuity.
//
// SILK is the speech layer of Opus, using Linear Predictive Coding (LPC)
// for efficient speech compression. The decoder reconstructs audio by:
// 1. Parsing frame headers (VAD, signal type, quantization offset)
// 2. Decoding parameters (gains, LSF/LPC coefficients, pitch lags)
// 3. Reconstructing excitation signal
// 4. Applying LTP (voiced) and LPC synthesis filters
//
// Reference: RFC 6716 Section 4.2
type Decoder struct {
	// Range decoder reference (set per frame)
	rangeDecoder *rangecoding.Decoder

	// Frame state (persists across frames)
	haveDecoded           bool  // True after first frame decoded
	previousLogGain       int32 // Last subframe gain (for delta coding)
	isPreviousFrameVoiced bool  // Was previous frame voiced (for LTP)

	// LPC state (persists across frames)
	lpcOrder      int       // Current LPC order (10 for NB/MB, 16 for WB)
	prevLPCValues []float32 // d_LPC output history for filter continuity

	// LSF state (persists for interpolation)
	prevLSFQ15 []int16 // Previous frame LSF coefficients (Q15)

	// Excitation/output history (for LTP lookback)
	// Needs at least max_pitch_lag + LTP_taps/2 + margin samples
	outputHistory []float32 // Ring buffer for pitch prediction
	historyIndex  int       // Current write position in ring buffer

	// Stereo state (for stereo unmixing)
	prevStereoWeights [2]int16 // Previous w0, w1 stereo weights (Q13)
}

// NewDecoder creates a new SILK decoder with proper initial state.
// The decoder is ready to process SILK frames after creation.
func NewDecoder() *Decoder {
	return &Decoder{
		prevLPCValues: make([]float32, 16),  // Max for WB (d_LPC = 16)
		prevLSFQ15:    make([]int16, 16),    // Max for WB (d_LPC = 16)
		outputHistory: make([]float32, 322), // Max pitch lag (288) + LTP taps (5) + margin
	}
}

// Reset clears decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *Decoder) Reset() {
	d.haveDecoded = false
	d.previousLogGain = 0
	d.isPreviousFrameVoiced = false
	d.lpcOrder = 0

	// Clear LPC history
	for i := range d.prevLPCValues {
		d.prevLPCValues[i] = 0
	}

	// Clear LSF history
	for i := range d.prevLSFQ15 {
		d.prevLSFQ15[i] = 0
	}

	// Clear output history
	for i := range d.outputHistory {
		d.outputHistory[i] = 0
	}
	d.historyIndex = 0

	// Clear stereo state
	d.prevStereoWeights = [2]int16{0, 0}
}

// SetRangeDecoder sets the range decoder for the current frame.
// This must be called before decoding each frame.
func (d *Decoder) SetRangeDecoder(rd *rangecoding.Decoder) {
	d.rangeDecoder = rd
}

// HaveDecoded returns whether at least one frame has been decoded.
// Used to determine if delta coding should be applied for gains.
func (d *Decoder) HaveDecoded() bool {
	return d.haveDecoded
}

// PreviousLogGain returns the previous frame's log gain value.
// Used for delta gain decoding.
func (d *Decoder) PreviousLogGain() int32 {
	return d.previousLogGain
}

// SetPreviousLogGain sets the log gain for delta coding.
func (d *Decoder) SetPreviousLogGain(gain int32) {
	d.previousLogGain = gain
}

// IsPreviousFrameVoiced returns whether the previous frame was voiced.
// Used for LTP filter application.
func (d *Decoder) IsPreviousFrameVoiced() bool {
	return d.isPreviousFrameVoiced
}

// SetPreviousFrameVoiced sets the voiced state for the previous frame.
func (d *Decoder) SetPreviousFrameVoiced(voiced bool) {
	d.isPreviousFrameVoiced = voiced
}

// MarkDecoded marks that a frame has been successfully decoded.
// This enables delta coding for subsequent frames.
func (d *Decoder) MarkDecoded() {
	d.haveDecoded = true
}

// LPCOrder returns the current LPC order (10 for NB/MB, 16 for WB).
func (d *Decoder) LPCOrder() int {
	return d.lpcOrder
}

// SetLPCOrder sets the LPC order based on bandwidth.
func (d *Decoder) SetLPCOrder(order int) {
	d.lpcOrder = order
}

// PrevLPCValues returns the LPC filter state for continuity.
func (d *Decoder) PrevLPCValues() []float32 {
	return d.prevLPCValues
}

// PrevLSFQ15 returns the previous frame's LSF coefficients.
func (d *Decoder) PrevLSFQ15() []int16 {
	return d.prevLSFQ15
}

// SetPrevLSFQ15 copies LSF coefficients for interpolation with next frame.
func (d *Decoder) SetPrevLSFQ15(lsf []int16) {
	copy(d.prevLSFQ15, lsf)
}

// OutputHistory returns the output buffer for LTP lookback.
func (d *Decoder) OutputHistory() []float32 {
	return d.outputHistory
}

// HistoryIndex returns the current write position in the history buffer.
func (d *Decoder) HistoryIndex() int {
	return d.historyIndex
}

// SetHistoryIndex sets the write position in the history buffer.
func (d *Decoder) SetHistoryIndex(idx int) {
	d.historyIndex = idx
}

// PrevStereoWeights returns the previous stereo weights.
func (d *Decoder) PrevStereoWeights() [2]int16 {
	return d.prevStereoWeights
}

// SetPrevStereoWeights sets the stereo weights for the next frame.
func (d *Decoder) SetPrevStereoWeights(weights [2]int16) {
	d.prevStereoWeights = weights
}
