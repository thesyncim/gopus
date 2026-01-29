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

	// libopus-aligned decoder state
	state                [2]decoderState
	stereo               stereoDecState
	prevDecodeOnlyMiddle int

	// Resamplers for each bandwidth (created on demand).
	// Separate resampler state per channel to match libopus.
	resamplers map[Bandwidth]*resamplerPair
}

// NewDecoder creates a new SILK decoder with proper initial state.
// The decoder is ready to process SILK frames after creation.
func NewDecoder() *Decoder {
	d := &Decoder{
		prevLPCValues: make([]float32, 16),  // Max for WB (d_LPC = 16)
		prevLSFQ15:    make([]int16, 16),    // Max for WB (d_LPC = 16)
		outputHistory: make([]float32, 322), // Max pitch lag (288) + LTP taps (5) + margin
	}
	resetDecoderState(&d.state[0])
	resetDecoderState(&d.state[1])
	return d
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

	resetDecoderState(&d.state[0])
	resetDecoderState(&d.state[1])
	d.stereo = stereoDecState{}
	d.prevDecodeOnlyMiddle = 0

	// Reset resampler state for a clean stream start
	for _, pair := range d.resamplers {
		if pair == nil {
			continue
		}
		if pair.left != nil {
			pair.left.Reset()
		}
		if pair.right != nil {
			pair.right.Reset()
		}
	}
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

// GetLastSignalType returns the signal type from the last decoded frame.
// Returns: 0=inactive, 1=unvoiced, 2=voiced
func (d *Decoder) GetLastSignalType() int {
	return int(d.state[0].indices.signalType)
}

// DebugFrameParams contains decoded frame parameters for debugging.
type DebugFrameParams struct {
	NLSFInterpCoefQ2 int
	LTPScaleIndex    int
	LagPrev          int
	GainIndices      []int
}

// GetLastFrameParams returns the parameters from the last decoded frame.
func (d *Decoder) GetLastFrameParams() DebugFrameParams {
	st := &d.state[0]
	gains := make([]int, st.nbSubfr)
	for i := 0; i < st.nbSubfr; i++ {
		gains[i] = int(st.indices.GainsIndices[i])
	}
	return DebugFrameParams{
		NLSFInterpCoefQ2: int(st.indices.NLSFInterpCoefQ2),
		LTPScaleIndex:    int(st.indices.LTPScaleIndex),
		LagPrev:          st.lagPrev,
		GainIndices:      gains,
	}
}

type resamplerPair struct {
	left  *LibopusResampler
	right *LibopusResampler
}

// GetResampler returns the libopus-compatible resampler for the given bandwidth.
// This returns the left/mono resampler.
func (d *Decoder) GetResampler(bandwidth Bandwidth) *LibopusResampler {
	return d.GetResamplerForChannel(bandwidth, 0)
}

// GetResamplerRightChannel returns the right channel resampler for the given bandwidth.
func (d *Decoder) GetResamplerRightChannel(bandwidth Bandwidth) *LibopusResampler {
	return d.GetResamplerForChannel(bandwidth, 1)
}

// GetResamplerForChannel returns the resampler for the specified channel and bandwidth.
func (d *Decoder) GetResamplerForChannel(bandwidth Bandwidth, channel int) *LibopusResampler {
	if d.resamplers == nil {
		d.resamplers = make(map[Bandwidth]*resamplerPair)
	}

	pair, ok := d.resamplers[bandwidth]
	if !ok {
		pair = &resamplerPair{}
		d.resamplers[bandwidth] = pair
	}

	config := GetBandwidthConfig(bandwidth)
	if channel == 1 {
		if pair.right == nil {
			pair.right = NewLibopusResampler(config.SampleRate, 48000)
		}
		return pair.right
	}

	if pair.left == nil {
		pair.left = NewLibopusResampler(config.SampleRate, 48000)
	}
	return pair.left
}

// BuildMonoResamplerInput prepares the mono resampler input using libopus-style sMid buffering.
// It updates the internal sMid state based on the current samples.
func (d *Decoder) BuildMonoResamplerInput(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	resamplerInput := make([]float32, len(samples))
	resamplerInput[0] = float32(d.stereo.sMid[1]) / 32768.0

	if len(samples) > 1 {
		copy(resamplerInput[1:], samples[:len(samples)-1])
		d.stereo.sMid[0] = float32ToInt16(samples[len(samples)-2])
		d.stereo.sMid[1] = float32ToInt16(samples[len(samples)-1])
	} else {
		d.stereo.sMid[0] = d.stereo.sMid[1]
		d.stereo.sMid[1] = float32ToInt16(samples[0])
	}

	return resamplerInput
}

// ResetSideChannel resets the side-channel decoder state and its resampler history.
// This matches libopus behavior when switching from mono to stereo.
func (d *Decoder) ResetSideChannel() {
	resetDecoderState(&d.state[1])
	if d.resamplers == nil {
		return
	}
	for _, pair := range d.resamplers {
		if pair == nil || pair.right == nil {
			continue
		}
		pair.right.Reset()
	}
}

// TraceInfo contains information about a subframe during decoding.
// Used for debugging to trace LTP parameters.
type TraceInfo struct {
	SignalType   int // 0=inactive, 1=unvoiced, 2=voiced
	PitchLag     int // Pitch lag for this subframe (voiced only)
	LtpMemLength int // LTP memory length
	LpcOrder     int // LPC order

	// Detailed values for debugging (only populated at k=0 or k=2 with interp)
	InvGainQ31    int32    // Inverse gain used for sLTP_Q15 population
	GainQ10       int32    // Gain for output scaling
	LTPCoefQ14    [5]int16 // LTP coefficients for this subframe
	FirstSLTPQ15  int32    // First sLTP_Q15 value used for LTP prediction
	FirstPresQ14  int32    // First presQ14 value (excitation + LTP prediction)
	FirstOutputQ0 int16    // First output sample value (after LPC synthesis)

	// Additional values for detailed debugging
	FirstLpcPredQ10 int32     // First lpcPredQ10 value
	FirstSLPC       int32     // First sLPC value (before output scaling)
	SLPCHistory     [16]int32 // sLPC history at start of subframe
	A_Q12           [16]int16 // LPC coefficients used for this subframe
	FirstExcQ14     int32     // First excitation value

	// LTP prediction trace values
	SLTPQ15Used     [5]int32 // sLTP_Q15 values used for first LTP prediction (indices: predLagPtr+0, -1, -2, -3, -4)
	FirstLTPPredQ13 int32    // First ltpPredQ13 value (before shifting to Q14)
}

// TraceCallback is called for each subframe during tracing.
type TraceCallback func(frame, k int, info TraceInfo)

// DecoderStateSnapshot holds a snapshot of the decoder state for debugging.
type DecoderStateSnapshot struct {
	PrevNLSFQ15 []int16 // Previous NLSF values (used for interpolation)
	LPCOrder    int     // Current LPC order
	FsKHz       int     // Sample rate in kHz
	NbSubfr     int     // Number of subframes
}

// GetDecoderState returns a snapshot of the internal decoder state for debugging.
func (d *Decoder) GetDecoderState() *DecoderStateSnapshot {
	st := &d.state[0]
	snapshot := &DecoderStateSnapshot{
		PrevNLSFQ15: make([]int16, st.lpcOrder),
		LPCOrder:    st.lpcOrder,
		FsKHz:       st.fsKHz,
		NbSubfr:     st.nbSubfr,
	}
	copy(snapshot.PrevNLSFQ15, st.prevNLSFQ15[:st.lpcOrder])
	return snapshot
}

// ExportedState holds internal decoder state for debugging/comparison.
type ExportedState struct {
	PrevGainQ16 int32
	SLPCQ14Buf  [16]int32
	OutBuf      []int16
	LtpMemLen   int
	LpcOrder    int
	FsKHz       int
}

// ExportState returns internal decoder state for debugging/comparison.
func (d *Decoder) ExportState() ExportedState {
	st := &d.state[0]
	state := ExportedState{
		PrevGainQ16: st.prevGainQ16,
		LtpMemLen:   st.ltpMemLength,
		LpcOrder:    st.lpcOrder,
		FsKHz:       st.fsKHz,
	}
	copy(state.SLPCQ14Buf[:], st.sLPCQ14Buf[:])
	if st.ltpMemLength > 0 {
		state.OutBuf = make([]int16, len(st.outBuf))
		copy(state.OutBuf, st.outBuf[:])
	}
	return state
}
