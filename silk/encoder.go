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
	previousLogGain       int32 // Last subframe gain (for delta coding) - legacy
	previousGainIndex     int32 // Previous gain quantization index [0, 63] (libopus matching)
	isPreviousFrameVoiced bool  // Was previous frame voiced
	frameCounter          int   // Frame counter for seed generation (seed = frameCounter & 3)

	// LPC state
	lpcOrder   int     // Current LPC order (10 for NB/MB, 16 for WB)
	prevLSFQ15 []int16 // Previous frame LSF (Q15) for interpolation

	// Stereo state
	prevStereoWeights [2]int16       // Previous w0, w1 stereo weights (Q13)
	stereo            stereoEncState // Full stereo encoder state for LP filtering

	// Pitch analysis state
	pitchState PitchAnalysisState // State for pitch estimation across frames

	// NSQ (Noise Shaping Quantization) state
	nsqState        *NSQState        // Noise shaping quantizer state for proper libopus-matching
	noiseShapeState *NoiseShapeState // Noise shaping analysis state for adaptive parameters

	// Encoder control parameters (persists across frames)
	snrDBQ7  int // Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
	ltpCorr  float32 // LTP correlation from pitch analysis [0, 1]

	// Analysis buffers (encoder-specific)
	inputBuffer []float32 // Buffered input samples
	lpcState    []float32 // LPC filter state for residual computation

	// Bandwidth configuration
	bandwidth  Bandwidth
	sampleRate int

	// FEC/LBRR (Low Bitrate Redundancy) state
	// LBRR provides forward error correction by encoding redundant data
	// for the previous frame at a lower quality in the current packet.
	// Reference: libopus silk/structs.h silk_encoder_state
	useFEC             bool                                // Enable in-band FEC (LBRR)
	lbrrEnabled        bool                                // LBRR currently active (depends on bitrate/loss)
	lbrrGainIncreases  int                                 // Gain increase for LBRR encoding
	lbrrPrevLastGainIdx int8                               // Previous frame's last gain index for LBRR
	lbrrFlags          [maxFramesPerPacket]int             // LBRR flags per frame in packet
	lbrrIndices        [maxFramesPerPacket]sideInfoIndices // LBRR indices per frame
	lbrrPulses         [maxFramesPerPacket][]int8          // LBRR pulses per frame
	packetLossPercent  int                                 // Expected packet loss (0-100)
	nFramesEncoded     int                                 // Number of frames encoded in current packet
	nFramesPerPacket   int                                 // Number of frames per packet

	// Scratch buffers for zero-allocation encoding
	scratchPaddedPulses []int8   // encodePulses: padded pulses
	scratchAbsPulses    []int    // encodePulses: absolute value pulses
	scratchSumPulses    []int    // encodePulses: sum per shell block
	scratchNRshifts     []int    // encodePulses: right shifts per shell block
	scratchLPC          []float64 // lpcToLSF: LPC coefficients as float64
	scratchP            []float64 // lpcToLSF: P polynomial
	scratchQ            []float64 // lpcToLSF: Q polynomial
	scratchLSFFloat     []float64 // lpcToLSF: LSF in float64
	scratchLSFQ15       []int16   // lpcToLSF: LSF result in Q15

	// Pitch detection scratch buffers
	scratchFrame8kHz  []float32  // detectPitch: downsampled to 8kHz
	scratchFrame4kHz  []float32  // detectPitch: downsampled to 4kHz
	scratchPitchC     []float64  // detectPitch: autocorrelation
	scratchDSrch      []int      // detectPitch: candidate lags
	scratchDSrchCorr  []float64  // detectPitch: candidate correlations
	scratchDComp      []int16    // detectPitch: expanded search
	scratchC8kHz      []float64  // detectPitch: 8kHz correlations (flat array for 4 subframes)
	scratchPitchLags  []int      // detectPitch: output pitch lags

	// Shell encoder scratch buffers (fixed sizes)
	scratchShellPulses1 [8]int // shellEncoder: level 1
	scratchShellPulses2 [4]int // shellEncoder: level 2
	scratchShellPulses3 [2]int // shellEncoder: level 3
	scratchShellPulses4 [1]int // shellEncoder: level 4

	// Pitch contour scratch buffer
	scratchPitchContour [][4]int8 // encodePitchLags: contour table

	// NSQ (computeNSQExcitation) scratch buffers
	scratchInputQ0         []int16 // PCM converted to int16
	scratchGainsQ16        []int32 // gains in Q16 format
	scratchPitchL          []int   // pitch lags for NSQ
	scratchArShpQ13        []int16 // AR shaping coefficients
	scratchLtpCoefQ14      []int16 // LTP coefficients
	scratchPredCoefQ12     []int16 // prediction coefficients
	scratchHarmShapeGainQ14 []int  // harmonic shaping gain
	scratchTiltQ14         []int   // tilt values
	scratchLfShpQ14        []int32 // low-frequency shaping
	scratchExcitation      []int32 // excitation output

	// LPC/Burg scratch buffers
	scratchLpcBurg       []float64 // LPC coefficients from Burg
	scratchBurgC         []float64 // Burg C buffer
	scratchBurgBf        []float64 // Burg forward buffer
	scratchBurgBb        []float64 // Burg backward buffer
	scratchWindowed      []float32 // computeLPCFromFrame: windowed PCM
	scratchLpcQ12        []int16   // burgLPCZeroAlloc: output LPC Q12
	scratchBurgAf        []float64 // burgModifiedFLPZeroAlloc: Af buffer
	scratchBurgCFirstRow []float64 // burgModifiedFLPZeroAlloc: CFirstRow
	scratchBurgCLastRow  []float64 // burgModifiedFLPZeroAlloc: CLastRow
	scratchBurgCAf       []float64 // burgModifiedFLPZeroAlloc: CAf
	scratchBurgCAb       []float64 // burgModifiedFLPZeroAlloc: CAb
	scratchBurgResult    []float64 // burgModifiedFLPZeroAlloc: result

	// LTP analysis scratch buffers
	scratchLtpCoeffs [4][]float64 // per-subframe LTP coefficients (4 subframes max)

	// LSF quantization scratch buffers
	scratchLsfResiduals []int   // computeStage2ResidualsLibopus: residuals
	scratchEcIx         []int16 // computeStage2ResidualsLibopus / NLSF decode: ecIx
	scratchPredQ8       []uint8 // computeStage2ResidualsLibopus / NLSF decode: predQ8
	scratchResQ10       []int16 // computeStage2ResidualsLibopus / NLSF decode: resQ10
	scratchNLSFIndices  []int8  // NLSF decode indices (stage1 + residuals)
	scratchNLSFWeights  []int16 // NLSF VQ weights (Laroia)
	scratchNLSFWeightsTmp []int16 // NLSF weights for interpolated vector
	scratchNLSFTempQ15  []int16 // Interpolated NLSF scratch

	// Gain encoding scratch buffers
	scratchGains        []float32 // computeSubframeGains: output gains
	scratchGainsQ16Enc  []int32   // encodeSubframeGains: gains in Q16
	scratchGainInd      []int8    // silkGainsQuant: gain indices

	// Output buffer scratch (standalone SILK mode)
	scratchOutput       []byte              // EncodeFrame: range encoder output
	scratchRangeEncoder rangecoding.Encoder // EncodeFrame: reusable range encoder
}

// ensureInt8Slice ensures the slice has at least n elements.
func ensureInt8Slice(buf *[]int8, n int) []int8 {
	if cap(*buf) < n {
		*buf = make([]int8, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureIntSlice ensures the slice has at least n elements.
func ensureIntSlice(buf *[]int, n int) []int {
	if cap(*buf) < n {
		*buf = make([]int, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureFloat64Slice ensures the slice has at least n elements.
func ensureFloat64Slice(buf *[]float64, n int) []float64 {
	if cap(*buf) < n {
		*buf = make([]float64, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureInt16Slice ensures the slice has at least n elements.
func ensureInt16Slice(buf *[]int16, n int) []int16 {
	if cap(*buf) < n {
		*buf = make([]int16, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureFloat32Slice ensures the slice has at least n elements.
func ensureFloat32Slice(buf *[]float32, n int) []float32 {
	if cap(*buf) < n {
		*buf = make([]float32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensure2DInt8Slice ensures the slice has at least n elements, each with 4 elements.
func ensure2DInt8Slice(buf *[][4]int8, n int) [][4]int8 {
	if cap(*buf) < n {
		*buf = make([][4]int8, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureInt32Slice ensures the slice has at least n elements.
func ensureInt32Slice(buf *[]int32, n int) []int32 {
	if cap(*buf) < n {
		*buf = make([]int32, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureUint8Slice ensures the slice has at least n elements.
func ensureUint8Slice(buf *[]uint8, n int) []uint8 {
	if cap(*buf) < n {
		*buf = make([]uint8, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// ensureByteSlice ensures the slice has at least n elements.
func ensureByteSlice(buf *[]byte, n int) []byte {
	if cap(*buf) < n {
		*buf = make([]byte, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

// NewEncoder creates a new SILK encoder with proper initial state.
func NewEncoder(bandwidth Bandwidth) *Encoder {
	config := GetBandwidthConfig(bandwidth)
	// Frame samples = sampleRate * 20ms / 1000
	frameSamples := config.SampleRate * 20 / 1000

	// Pre-allocate LBRR pulse buffers
	lbrrPulses := [maxFramesPerPacket][]int8{}
	for i := range lbrrPulses {
		lbrrPulses[i] = make([]int8, maxFrameLength)
	}

	return &Encoder{
		prevLSFQ15:        make([]int16, config.LPCOrder),
		inputBuffer:       make([]float32, frameSamples*2), // Look-ahead buffer
		lpcState:          make([]float32, config.LPCOrder),
		nsqState:          NewNSQState(),        // Initialize NSQ state
		noiseShapeState:   NewNoiseShapeState(), // Initialize noise shaping state
		bandwidth:         bandwidth,
		sampleRate:        config.SampleRate,
		lpcOrder:          config.LPCOrder,
		snrDBQ7:           25 * 128,             // Default: 25 dB SNR target
		nFramesPerPacket:  1,                    // Default: 1 frame per packet (20ms)
		lbrrPulses:        lbrrPulses,
		lbrrGainIncreases: 7,                    // Default gain increase for LBRR
	}
}

// Reset clears encoder state for a new stream.
func (e *Encoder) Reset() {
	e.haveEncoded = false
	e.previousLogGain = 0
	e.previousGainIndex = 0
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
	e.stereo = stereoEncState{}         // Reset LP filter state
	e.pitchState = PitchAnalysisState{} // Reset pitch state
	if e.nsqState != nil {
		e.nsqState.Reset() // Reset NSQ state
	}
	if e.noiseShapeState != nil {
		e.noiseShapeState = NewNoiseShapeState() // Reset noise shaping state
	}
	e.ltpCorr = 0

	// Reset FEC/LBRR state
	e.lbrrEnabled = false
	e.lbrrPrevLastGainIdx = 10 // Default gain index (same as decoder reset)
	e.nFramesEncoded = 0
	for i := range e.lbrrFlags {
		e.lbrrFlags[i] = 0
	}
	for i := range e.lbrrIndices {
		e.lbrrIndices[i] = sideInfoIndices{}
	}
	for i := range e.lbrrPulses {
		for j := range e.lbrrPulses[i] {
			e.lbrrPulses[i][j] = 0
		}
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

// SetBitrate sets the target bitrate for encoding.
// This is a no-op for SILK in hybrid mode (bitrate is controlled by Opus-level allocator).
func (e *Encoder) SetBitrate(bitrate int) {
	// SILK bitrate control in hybrid mode is handled by the Opus-level encoder.
	// This method exists for API compatibility.
}

// SetFEC enables or disables in-band Forward Error Correction (LBRR).
// When enabled, each packet includes redundant data for the previous frame
// at a lower quality, allowing the decoder to recover from packet loss.
//
// Reference: libopus silk/control_codec.c silk_setup_LBRR
func (e *Encoder) SetFEC(enabled bool) {
	e.useFEC = enabled
	e.updateLBRREnabled()
}

// FECEnabled returns whether FEC (LBRR) is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.useFEC
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
// This affects the LBRR gain increase - higher loss means more aggressive
// redundancy encoding with higher gains.
//
// Reference: libopus silk/control_codec.c silk_setup_LBRR
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLossPercent = lossPercent
	e.updateLBRREnabled()
}

// PacketLoss returns the configured packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return e.packetLossPercent
}

// updateLBRREnabled updates the LBRR enabled flag based on FEC setting and packet loss.
// Matches libopus silk_setup_LBRR logic.
func (e *Encoder) updateLBRREnabled() {
	lbrrInPreviousPacket := e.lbrrEnabled
	e.lbrrEnabled = e.useFEC

	if e.lbrrEnabled {
		// Set gain increase for coding LBRR excitation
		// Reference: libopus silk/control_codec.c
		if !lbrrInPreviousPacket {
			// Previous packet did not have LBRR, was coded at higher bitrate
			e.lbrrGainIncreases = 7
		} else {
			// LBRR_GainIncreases = max(7 - PacketLoss_perc * 0.2, 3)
			// Using fixed-point: 0.2 in Q16 = 13107
			gainDecrease := (e.packetLossPercent * 13107) >> 16
			e.lbrrGainIncreases = 7 - gainDecrease
			if e.lbrrGainIncreases < 3 {
				e.lbrrGainIncreases = 3
			}
		}
	}
}

// LBRREnabled returns whether LBRR is currently active.
// LBRR may be disabled even when FEC is enabled if bitrate is too low.
func (e *Encoder) LBRREnabled() bool {
	return e.lbrrEnabled
}
