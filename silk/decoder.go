package silk

import (
	"github.com/thesyncim/gopus/plc"
	"github.com/thesyncim/gopus/rangecoding"
)

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

	// Track previous bandwidth to detect bandwidth changes.
	// Used to reset sMid state when sample rate changes.
	prevBandwidth    Bandwidth
	hasPrevBandwidth bool

	// Resamplers for each bandwidth (created on demand).
	// Separate resampler state per channel to match libopus.
	resamplers map[Bandwidth]*resamplerPair

	// Per-decoder PLC state (do not share across decoder instances).
	plcState *plc.State

	// Debug flag to track if reset was called (for testing)
	debugResetCalled bool

	// Debug: capture resampler state before/after reset
	debugPreResetSIIR  [6]int32
	debugPostResetSIIR [6]int32

	// Debug: disable resampler reset on bandwidth change
	disableResamplerReset bool

	// Mono output delay buffer to match libopus behavior.
	// libopus delays mono SILK output by (1 + inputDelay) samples:
	// - 1 sample from sMid history prepended before resampler input
	// - inputDelay samples from resampler delay buffer (4 for 8kHz)
	monoDelayBuf     []int16 // Delay buffer (size = fsKHz)
	monoInputDelay   int     // Delay compensation (from delay_matrix_dec)
	monoDelayBufInit bool    // Whether delay buffer has been initialized

	// Pre-allocated scratch buffers for hot-path performance.
	// These eliminate allocations in performance-critical decode loops.
	// Sizes are based on maximum SILK frame parameters:
	// - maxSubFrameLength = 80 (5ms * 16kHz)
	// - maxLPCOrder = 16
	// - maxLtpMemLength = 320 (20ms * 16kHz)
	// - maxFrameLength = 320 (4 subframes * 80 samples)
	// - maxFramesPerPacket = 3
	scratchSLPC     []int32   // Size: maxSubFrameLength + maxLPCOrder = 96
	scratchSLTP     []int16   // Size: maxLtpMemLength = 320
	scratchSLTPQ15  []int32   // Size: maxLtpMemLength + maxFrameLength = 640
	scratchPresQ14  []int32   // Size: maxSubFrameLength = 80
	scratchOutInt16 []int16   // Size: maxFramesPerPacket * maxFrameLength = 960
	scratchPulses   []int16   // Size: roundUpShellFrame(maxFrameLength) = 320
	scratchOutput   []float32 // Size: maxFramesPerPacket * maxFrameLength = 960

	// Scratch buffers for silkDecodeIndices
	scratchEcIx   []int16 // Size: maxLPCOrder = 16
	scratchPredQ8 []uint8 // Size: maxLPCOrder = 16

	// Scratch buffers for silkShellDecoder
	scratchPulses3 []int16 // Size: 2
	scratchPulses2 []int16 // Size: 4
	scratchPulses1 []int16 // Size: 8

	// Scratch buffers for silkDecodePulses
	// iter = frameLength >> 4 + 1 = 320/16 + 1 = 21 max
	scratchSumPulses []int // Size: 21
	scratchNLshifts  []int // Size: 21

	// Scratch buffers for resampler - eliminate allocations in Process()
	resamplerScratchIn     []int16   // Size: max input samples (fsInKHz * 10 = 160)
	resamplerScratchOut    []int16   // Size: max output samples (480 for 48kHz)
	resamplerScratchResult []float32 // Size: max output samples (480)
	resamplerScratchBuf    []int16   // Size: 2*batchSize + resamplerOrderFIR12

	// Scratch buffer for upsampleTo48k
	upsampleScratch []float32 // Size: maxFramesPerPacket * maxFrameLength * 6 = 5760

	// Scratch buffers for applyMonoDelay
	monoResamplerIn []int16 // Size: maxFramesPerPacket * maxFrameLength = 960
	monoOutput      []int16 // Size: maxFramesPerPacket * maxFrameLength = 960

	// Scratch buffer for BuildMonoResamplerInput
	buildMonoInputScratch []float32 // Size: maxFramesPerPacket * maxFrameLength = 960
}

// NewDecoder creates a new SILK decoder with proper initial state.
// The decoder is ready to process SILK frames after creation.
func NewDecoder() *Decoder {
	// Pre-calculate max buffer sizes based on SILK constants:
	// - maxSubFrameLength = 80 (5ms * 16kHz)
	// - maxLPCOrder = 16
	// - maxLtpMemLength = 320 (20ms * 16kHz)
	// - maxFrameLength = 320 (4 subframes * 80)
	// - maxFramesPerPacket = 3
	const (
		maxSLPCSize     = 80 + 16   // maxSubFrameLength + maxLPCOrder
		maxSLTPSize     = 320       // maxLtpMemLength
		maxSLTPQ15Size  = 320 + 320 // maxLtpMemLength + maxFrameLength
		maxPresQ14Size  = 80        // maxSubFrameLength
		maxOutInt16Size = 3 * 320   // maxFramesPerPacket * maxFrameLength
		maxPulsesSize   = 320       // roundUpShellFrame(maxFrameLength)
		maxOutputSize   = 3 * 320   // maxFramesPerPacket * maxFrameLength

		// Additional scratch buffer sizes
		maxIterSize     = 21   // (maxFrameLength >> 4) + 1
		maxResamplerIn  = 160  // 16kHz * 10ms = 160 samples
		maxResamplerOut = 480  // 48kHz * 10ms = 480 samples
		maxResamplerBuf = 328  // 2 * 160 + 8 = 328 (2*batchSize + resamplerOrderFIR12)
		maxUpsampleSize = 5760 // 3 * 320 * 6 = 5760 (maxFramesPerPacket * maxFrameLength * 6x upsample)
	)

	d := &Decoder{
		prevLPCValues: make([]float32, 16),  // Max for WB (d_LPC = 16)
		prevLSFQ15:    make([]int16, 16),    // Max for WB (d_LPC = 16)
		outputHistory: make([]float32, 322), // Max pitch lag (288) + LTP taps (5) + margin

		// Pre-allocated scratch buffers for hot-path performance
		scratchSLPC:     make([]int32, maxSLPCSize),
		scratchSLTP:     make([]int16, maxSLTPSize),
		scratchSLTPQ15:  make([]int32, maxSLTPQ15Size),
		scratchPresQ14:  make([]int32, maxPresQ14Size),
		scratchOutInt16: make([]int16, maxOutInt16Size),
		scratchPulses:   make([]int16, maxPulsesSize),
		scratchOutput:   make([]float32, maxOutputSize),

		// Additional scratch buffers for zero-allocation decoding
		scratchEcIx:            make([]int16, maxLPCOrder),
		scratchPredQ8:          make([]uint8, maxLPCOrder),
		scratchPulses3:         make([]int16, 2),
		scratchPulses2:         make([]int16, 4),
		scratchPulses1:         make([]int16, 8),
		scratchSumPulses:       make([]int, maxIterSize),
		scratchNLshifts:        make([]int, maxIterSize),
		resamplerScratchIn:     make([]int16, maxResamplerIn),
		resamplerScratchOut:    make([]int16, maxResamplerOut),
		resamplerScratchResult: make([]float32, maxResamplerOut),
		resamplerScratchBuf:    make([]int16, maxResamplerBuf),
		upsampleScratch:        make([]float32, maxUpsampleSize),
		monoResamplerIn:        make([]int16, maxOutInt16Size),
		monoOutput:             make([]int16, maxOutInt16Size),
		buildMonoInputScratch:  make([]float32, maxOutInt16Size),
		plcState:               plc.NewState(),
	}
	resetDecoderState(&d.state[0])
	resetDecoderState(&d.state[1])

	// Wire up scratch buffers to decoderState for hot-path optimization
	d.setupScratchBuffers()

	return d
}

// setupScratchBuffers wires the pre-allocated scratch buffers to both decoderState instances.
// This enables silkDecodeCore and related functions to use pre-allocated memory.
func (d *Decoder) setupScratchBuffers() {
	d.state[0].scratchSLPC = d.scratchSLPC
	d.state[0].scratchSLTP = d.scratchSLTP
	d.state[0].scratchSLTPQ15 = d.scratchSLTPQ15
	d.state[0].scratchPresQ14 = d.scratchPresQ14
	d.state[0].scratchEcIx = d.scratchEcIx
	d.state[0].scratchPredQ8 = d.scratchPredQ8
	d.state[0].scratchPulses3 = d.scratchPulses3
	d.state[0].scratchPulses2 = d.scratchPulses2
	d.state[0].scratchPulses1 = d.scratchPulses1
	d.state[0].scratchSumPulses = d.scratchSumPulses
	d.state[0].scratchNLshifts = d.scratchNLshifts

	d.state[1].scratchSLPC = d.scratchSLPC
	d.state[1].scratchSLTP = d.scratchSLTP
	d.state[1].scratchSLTPQ15 = d.scratchSLTPQ15
	d.state[1].scratchPresQ14 = d.scratchPresQ14
	d.state[1].scratchEcIx = d.scratchEcIx
	d.state[1].scratchPredQ8 = d.scratchPredQ8
	d.state[1].scratchPulses3 = d.scratchPulses3
	d.state[1].scratchPulses2 = d.scratchPulses2
	d.state[1].scratchPulses1 = d.scratchPulses1
	d.state[1].scratchSumPulses = d.scratchSumPulses
	d.state[1].scratchNLshifts = d.scratchNLshifts
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

	// Reset mono delay buffer state
	d.monoDelayBuf = nil
	d.monoInputDelay = 0
	d.monoDelayBufInit = false

	// Clear scratch buffers (zero them for clean state).
	// Note: We don't reallocate - the buffers remain allocated
	// and are reused across stream resets for performance.
	for i := range d.scratchSLPC {
		d.scratchSLPC[i] = 0
	}
	for i := range d.scratchSLTP {
		d.scratchSLTP[i] = 0
	}
	for i := range d.scratchSLTPQ15 {
		d.scratchSLTPQ15[i] = 0
	}
	for i := range d.scratchPresQ14 {
		d.scratchPresQ14[i] = 0
	}
	for i := range d.scratchOutInt16 {
		d.scratchOutInt16[i] = 0
	}
	for i := range d.scratchPulses {
		d.scratchPulses[i] = 0
	}
	for i := range d.scratchOutput {
		d.scratchOutput[i] = 0
	}
	for i := range d.scratchEcIx {
		d.scratchEcIx[i] = 0
	}
	for i := range d.scratchPredQ8 {
		d.scratchPredQ8[i] = 0
	}
	for i := range d.scratchPulses3 {
		d.scratchPulses3[i] = 0
	}
	for i := range d.scratchPulses2 {
		d.scratchPulses2[i] = 0
	}
	for i := range d.scratchPulses1 {
		d.scratchPulses1[i] = 0
	}
	for i := range d.scratchSumPulses {
		d.scratchSumPulses[i] = 0
	}
	for i := range d.scratchNLshifts {
		d.scratchNLshifts[i] = 0
	}
	for i := range d.resamplerScratchIn {
		d.resamplerScratchIn[i] = 0
	}
	for i := range d.resamplerScratchOut {
		d.resamplerScratchOut[i] = 0
	}
	for i := range d.resamplerScratchResult {
		d.resamplerScratchResult[i] = 0
	}
	for i := range d.resamplerScratchBuf {
		d.resamplerScratchBuf[i] = 0
	}
	for i := range d.upsampleScratch {
		d.upsampleScratch[i] = 0
	}
	for i := range d.monoResamplerIn {
		d.monoResamplerIn[i] = 0
	}
	for i := range d.monoOutput {
		d.monoOutput[i] = 0
	}
	for i := range d.buildMonoInputScratch {
		d.buildMonoInputScratch[i] = 0
	}

	// Re-wire scratch buffers after resetDecoderState cleared them
	d.setupScratchBuffers()

	if d.plcState == nil {
		d.plcState = plc.NewState()
	}
	d.plcState.Reset()
}

// applyMonoDelay applies libopus-compatible delay compensation for mono SILK output.
// This simulates the delay introduced by libopus's sMid buffering and resampler.
//
// The total delay is (1 + inputDelay) samples:
// - 1 sample from sMid[1] prepended before resampler input
// - inputDelay samples from resampler delay buffer (from delay_matrix_dec)
//
// For the same-rate "copy" resampler case, the delay values are:
// - 8kHz: inputDelay=4, total delay=5 samples
// - 12kHz: inputDelay=9, total delay=10 samples (but 8kHz->12kHz uses different path)
// - 16kHz: inputDelay=12, total delay=13 samples
//
// For native rate output (API rate = internal rate), we only need the
// delay from delay_matrix_dec[rateID(in)][rateID(in)].
func (d *Decoder) applyMonoDelay(decoded []int16, fsKHz int) []int16 {
	// Get inputDelay from delay_matrix_dec for native rate output
	// libopus uses rateID to convert Hz to index: 8->0, 12->1, 16->2
	var inputDelay int
	switch fsKHz {
	case 8:
		inputDelay = 4 // delay_matrix_dec[0][0] for 8kHz->8kHz API
	case 12:
		inputDelay = 9 // delay_matrix_dec[1][1] for 12kHz->12kHz API
	case 16:
		inputDelay = 12 // delay_matrix_dec[2][2] for 16kHz->16kHz API
	default:
		// Unknown rate, return as-is
		return decoded
	}

	// Initialize delay buffer if needed
	if !d.monoDelayBufInit || d.monoInputDelay != inputDelay {
		d.monoDelayBuf = make([]int16, fsKHz) // Delay buffer size = fsKHz samples (1ms)
		d.monoInputDelay = inputDelay
		d.monoDelayBufInit = true
	}

	// Build the resampler input: [sMid[1], decoded[0:n-1]]
	// libopus calls resampler with &samplesOut1_tmp[n][1] and nSamplesOutDec
	// This means the input has sMid[1] at position 0, then decoded[0:nSamplesOutDec-1]
	// The last decoded sample is NOT in the resampler input.
	nSamplesOutDec := len(decoded)

	// Use scratch buffer for resamplerIn if available
	var resamplerIn []int16
	if d.monoResamplerIn != nil && len(d.monoResamplerIn) >= nSamplesOutDec {
		resamplerIn = d.monoResamplerIn[:nSamplesOutDec]
	} else {
		resamplerIn = make([]int16, nSamplesOutDec)
	}
	resamplerIn[0] = d.stereo.sMid[1] // sMid[1] from previous frame
	copy(resamplerIn[1:], decoded[:nSamplesOutDec-1])

	inLen := len(resamplerIn)

	// Output buffer same size as decoded - use scratch buffer if available
	var output []int16
	if d.monoOutput != nil && len(d.monoOutput) >= len(decoded) {
		output = d.monoOutput[:len(decoded)]
	} else {
		output = make([]int16, len(decoded))
	}

	// Apply the libopus copy-resampler logic exactly:
	// silk_resampler() for USE_silk_resampler_copy case:
	//
	// nSamples = Fs_in_kHz - inputDelay;  // 8 - 4 = 4 for 8kHz
	// silk_memcpy(&delayBuf[inputDelay], in, nSamples * sizeof(opus_int16));
	// silk_memcpy(out, delayBuf, Fs_in_kHz * sizeof(opus_int16));
	// silk_memcpy(&out[Fs_out_kHz], &in[nSamples], (inLen - Fs_in_kHz) * sizeof(opus_int16));
	// silk_memcpy(delayBuf, &in[inLen - inputDelay], inputDelay * sizeof(opus_int16));

	nSamples := fsKHz - inputDelay

	// Step 1: Copy first nSamples of input to end of delay buffer
	copy(d.monoDelayBuf[inputDelay:], resamplerIn[:nSamples])

	// Step 2: Copy delay buffer to first fsKHz samples of output
	copy(output[:fsKHz], d.monoDelayBuf[:])

	// Step 3: Copy remaining input to rest of output
	// output[fsKHz:] = resamplerIn[nSamples:nSamples+(inLen-fsKHz)]
	if inLen > fsKHz {
		copy(output[fsKHz:], resamplerIn[nSamples:nSamples+(inLen-fsKHz)])
	}

	// Step 4: Save last inputDelay samples of input to start of delay buffer
	copy(d.monoDelayBuf[:inputDelay], resamplerIn[inLen-inputDelay:])

	// Update sMid with last 2 samples of decoded output (before delay processing)
	// libopus does: silk_memcpy(psDec->sStereo.sMid, &samplesOut1_tmp[0][nSamplesOutDec], 2)
	// where samplesOut1_tmp[0][2:] contains decoded samples
	// So sMid gets the last 2 samples of the decoded (not delayed) output
	if len(decoded) >= 2 {
		d.stereo.sMid[0] = decoded[len(decoded)-2]
		d.stereo.sMid[1] = decoded[len(decoded)-1]
	}

	return output
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
	QuantOffset      int
	GainIndices      []int
	PERIndex         int
	LTPIndices       []int
	LagIndex         int
	ContourIndex     int
	Seed             int
}

// GetLastFrameParams returns the parameters from the last decoded frame.
func (d *Decoder) GetLastFrameParams() DebugFrameParams {
	st := &d.state[0]
	gains := make([]int, st.nbSubfr)
	for i := 0; i < st.nbSubfr; i++ {
		gains[i] = int(st.indices.GainsIndices[i])
	}
	ltpIdx := make([]int, st.nbSubfr)
	for i := 0; i < st.nbSubfr; i++ {
		ltpIdx[i] = int(st.indices.LTPIndex[i])
	}
	return DebugFrameParams{
		NLSFInterpCoefQ2: int(st.indices.NLSFInterpCoefQ2),
		LTPScaleIndex:    int(st.indices.LTPScaleIndex),
		LagPrev:          st.lagPrev,
		QuantOffset:      int(st.indices.quantOffsetType),
		GainIndices:      gains,
		PERIndex:         int(st.indices.PERIndex),
		LTPIndices:       ltpIdx,
		LagIndex:         int(st.indices.lagIndex),
		ContourIndex:     int(st.indices.contourIndex),
		Seed:             int(st.indices.Seed),
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

// HandleBandwidthChange checks if bandwidth has changed.
// This must be called before BuildMonoResamplerInput when bandwidth may have changed.
// Returns true if bandwidth changed.
//
// Note: libopus does NOT reset sMid on bandwidth change. Only the resampler internal
// state is zeroed. sMid values from the previous bandwidth are preserved for continuity.
func (d *Decoder) HandleBandwidthChange(bandwidth Bandwidth) bool {
	if !d.hasPrevBandwidth {
		d.prevBandwidth = bandwidth
		d.hasPrevBandwidth = true
		return false
	}
	if d.prevBandwidth != bandwidth {
		// Bandwidth changed - do NOT reset sMid to match libopus behavior.
		// The resampler internal state is reset in handleBandwidthChange().
		d.prevBandwidth = bandwidth
		return true
	}
	return false
}

// BuildMonoResamplerInput prepares the mono resampler input using libopus-style sMid buffering.
// It updates the internal sMid state based on the current samples.
func (d *Decoder) BuildMonoResamplerInput(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Use pre-allocated scratch buffer if available
	var resamplerInput []float32
	if d.buildMonoInputScratch != nil && len(d.buildMonoInputScratch) >= len(samples) {
		resamplerInput = d.buildMonoInputScratch[:len(samples)]
	} else {
		resamplerInput = make([]float32, len(samples))
	}
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

// BuildMonoResamplerInputInt16 prepares mono resampler input with libopus-style sMid buffering.
// This int16 variant is used by decoder hot paths to avoid float32->int16 reconversion.
func (d *Decoder) BuildMonoResamplerInputInt16(samples []int16) []int16 {
	if len(samples) == 0 {
		return nil
	}

	var resamplerInput []int16
	if d.monoResamplerIn != nil && len(d.monoResamplerIn) >= len(samples) {
		resamplerInput = d.monoResamplerIn[:len(samples)]
	} else {
		resamplerInput = make([]int16, len(samples))
	}
	resamplerInput[0] = d.stereo.sMid[1]

	if len(samples) > 1 {
		copy(resamplerInput[1:], samples[:len(samples)-1])
		d.stereo.sMid[0] = samples[len(samples)-2]
		d.stereo.sMid[1] = samples[len(samples)-1]
	} else {
		d.stereo.sMid[0] = d.stereo.sMid[1]
		d.stereo.sMid[1] = samples[0]
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

// handleBandwidthChange detects sample rate changes and resets the appropriate resampler.
// In libopus, when the internal sample rate changes (NB 8kHz <-> MB 12kHz <-> WB 16kHz),
// the resampler for the NEW bandwidth needs to be reset to avoid using stale state.
//
// IMPORTANT: libopus does NOT reset sMid on bandwidth change - it keeps the previous
// sample values. Only the resampler internal state (sIIR, sFIR, delayBuf) is zeroed via
// silk_resampler_init(). The sMid values from the previous bandwidth are preserved and
// used as the first input sample to the new resampler, which causes a small transient
// but maintains signal continuity at bandwidth transitions.
//
// NOTE: This is also called by the Hybrid decoder via NotifyBandwidthChange to ensure
// proper resampler state management when mixing SILK-only and Hybrid packets.
func (d *Decoder) handleBandwidthChange(bandwidth Bandwidth) {
	if d.hasPrevBandwidth && d.prevBandwidth != bandwidth {
		// Sample rate changed - reset the resampler for the NEW bandwidth
		// but keep sMid values to match libopus behavior.
		if !d.disableResamplerReset {
			if pair, ok := d.resamplers[bandwidth]; ok && pair != nil {
				if pair.left != nil {
					pair.left.Reset()
					d.debugResetCalled = true
				}
				if pair.right != nil {
					pair.right.Reset()
				}
			}
		}
	}
	d.prevBandwidth = bandwidth
	d.hasPrevBandwidth = true
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

// GetSMid returns the current sMid state for debugging.
func (d *Decoder) GetSMid() [2]int16 {
	return d.stereo.sMid
}

// NotifyBandwidthChange updates bandwidth tracking and resets the resampler if needed.
// This should be called by the Hybrid decoder before using SILK to ensure proper
// resampler state when transitioning between SILK-only and Hybrid modes.
//
// When Hybrid mode uses SILK at BandwidthWideband, calling this method ensures that:
// 1. The prevBandwidth is updated to WB
// 2. If transitioning TO WB, the WB resampler is reset
// 3. When later transitioning back to SILK NB/MB, the correct resampler will be reset
func (d *Decoder) NotifyBandwidthChange(bandwidth Bandwidth) {
	d.handleBandwidthChange(bandwidth)
}

// DebugResetCalled returns true if the resampler reset was called since last clear.
func (d *Decoder) DebugResetCalled() bool {
	return d.debugResetCalled
}

// DebugClearResetFlag clears the debug reset flag.
func (d *Decoder) DebugClearResetFlag() {
	d.debugResetCalled = false
}

// DebugGetResetStates returns the pre and post reset sIIR states.
func (d *Decoder) DebugGetResetStates() (pre, post [6]int32) {
	return d.debugPreResetSIIR, d.debugPostResetSIIR
}

// GetResamplerScratch returns a pre-allocated buffer for resampler output (left channel).
// This is used by the Hybrid decoder for zero-allocation SILK upsampling.
func (d *Decoder) GetResamplerScratch(frameSize int) []float32 {
	// Max output is frameSize samples (already at 48kHz after resampling)
	if cap(d.resamplerScratchResult) < frameSize {
		d.resamplerScratchResult = make([]float32, frameSize)
	} else {
		d.resamplerScratchResult = d.resamplerScratchResult[:frameSize]
	}
	return d.resamplerScratchResult
}

// GetResamplerScratchR returns a pre-allocated buffer for right channel resampler output.
// This is used by the Hybrid decoder for zero-allocation stereo SILK upsampling.
func (d *Decoder) GetResamplerScratchR(frameSize int) []float32 {
	// Max output is frameSize samples (already at 48kHz after resampling)
	if cap(d.upsampleScratch) < frameSize {
		d.upsampleScratch = make([]float32, frameSize)
	} else {
		d.upsampleScratch = d.upsampleScratch[:frameSize]
	}
	return d.upsampleScratch
}

// SetDisableResamplerReset controls whether the resampler is reset on bandwidth change.
// This is for testing purposes to compare behavior with/without reset.
func (d *Decoder) SetDisableResamplerReset(disable bool) {
	d.disableResamplerReset = disable
}

// FinalRange returns the final range coder state after decoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after decoding a frame to get a meaningful value.
func (d *Decoder) FinalRange() uint32 {
	if d.rangeDecoder != nil {
		return d.rangeDecoder.Range()
	}
	return 0
}
