package silk

import "github.com/thesyncim/gopus/rangecoding"

// VADFrameState carries per-frame SILK VAD-derived controls.
// It mirrors the state produced by silk_encode_do_VAD_Fxx in libopus.
type VADFrameState struct {
	SpeechActivityQ8     int
	InputTiltQ15         int
	InputQualityBandsQ15 [4]int
	Valid                bool
}

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
	ecPrevLagIndex        int   // Previous lag index for conditional pitch coding
	ecPrevSignalType      int   // Previous signal type for conditional pitch coding
	lastQuantOffsetType   int   // Last frame's quantization offset type (for hybrid silk_info)
	frameCounter          int   // Frame counter for seed generation (seed = frameCounter & 3)

	// LPC state
	lpcOrder   int     // Current LPC order (10 for NB/MB, 16 for WB)
	prevLSFQ15 []int16 // Previous frame LSF (Q15) for interpolation

	// Stereo state
	prevStereoWeights [2]int16       // Previous w0, w1 stereo weights (Q13)
	stereo            stereoEncState // Full stereo encoder state for LP filtering

	// LP variable cutoff filter state (for smooth bandwidth transitions)
	lpState LPState

	// Pitch analysis state
	pitchState       PitchAnalysisState // State for pitch estimation across frames
	pitchAnalysisBuf []float32          // History buffer for pitch analysis (LTP memory + frame)

	// Optional trace hook for parity debugging (set in tests).
	trace *EncoderTrace

	// VAD-derived state (optional, provided by Opus-level encoder)
	speechActivityQ8     int    // Speech activity in Q8 (0-255)
	inputTiltQ15         int    // Spectral tilt in Q15 from VAD
	inputQualityBandsQ15 [4]int // Quality in each VAD band (Q15)
	speechActivitySet    bool   // Whether VAD-derived state was explicitly set

	// NSQ (Noise Shaping Quantization) state
	nsqState        *NSQState        // Noise shaping quantizer state for proper libopus-matching
	noiseShapeState *NoiseShapeState // Noise shaping analysis state for adaptive parameters

	// Encoder control parameters (persists across frames)
	snrDBQ7                  int  // Target SNR in dB (Q7 format, e.g., 25 dB = 25 * 128)
	targetRateBps            int  // Target bitrate (per channel) for SNR control
	lastControlTargetRateBps int  // Last per-frame target rate used for SNR control
	useCBR                   bool // Constant Bitrate mode
	// reducedDependency mirrors libopus encControl->reducedDependency.
	// When enabled, the first frame of each packet is coded as
	// first_frame_after_reset without resetting the full encoder state.
	reducedDependency bool
	// forceFirstFrameAfterReset is latched at packet start and consumed by the
	// first encoded frame in that packet.
	forceFirstFrameAfterReset bool
	ltpCorr                   float32 // LTP correlation from pitch analysis [0, 1]
	sumLogGainQ7              int32   // Sum log gain for LTP quantization
	complexity                int     // Encoder complexity (0-10)
	nStatesDelayedDecision    int     // Delayed decision states (libopus control_codec)

	// Pitch estimation tuning (mirrors libopus control_codec.c)
	pitchEstimationComplexity   int
	pitchEstimationThresholdQ16 int32
	pitchEstimationLPCOrder     int

	// Noise shaping analysis tuning (mirrors libopus control_codec.c)
	shapingLPCOrder int
	laShape         int
	shapeWinLength  int
	warpingQ16      int
	nlsfSurvivors   int

	// LPC analysis results (for gain computation from prediction residual)
	lastTotalEnergy float64 // C0 from Burg analysis
	lastInvGain     float64 // Inverse prediction gain from Burg analysis
	lastLPCGain     float64 // Initial prediction gain from pitch analysis
	lastNumSamples  int     // Number of samples analyzed

	// Analysis buffers (encoder-specific)
	inputBuffer     []float32 // Noise shaping lookahead buffer (x_buf in libopus)
	lpcState        []float32 // LPC filter state for residual computation
	scratchPCMQuant []float32 // Quantized PCM (int16 round-trip) for SILK entry

	// Bandwidth configuration
	bandwidth  Bandwidth
	sampleRate int

	// FEC/LBRR (Low Bitrate Redundancy) state
	// LBRR provides forward error correction by encoding redundant data
	// for the previous frame at a lower quality in the current packet.
	// Reference: libopus silk/structs.h silk_encoder_state
	useFEC              bool                                // Enable in-band FEC (LBRR)
	lbrrEnabled         bool                                // LBRR currently active (depends on bitrate/loss)
	lbrrGainIncreases   int                                 // Gain increase for LBRR encoding
	lbrrPrevLastGainIdx int8                                // Previous frame's last gain index for LBRR
	lbrrFlags           [maxFramesPerPacket]int             // LBRR flags per frame in packet
	lbrrFlag            int                                 // LBRR flag for current packet header
	lbrrIndices         [maxFramesPerPacket]sideInfoIndices // LBRR indices per frame
	lbrrPulses          [maxFramesPerPacket][]int8          // LBRR pulses per frame
	lbrrFrameLength     [maxFramesPerPacket]int             // LBRR frame length per frame
	lbrrNbSubfr         [maxFramesPerPacket]int             // LBRR subframe count per frame
	packetLossPercent   int                                 // Expected packet loss (0-100)
	nFramesEncoded      int                                 // Number of frames encoded in current packet
	nFramesPerPacket    int                                 // Number of frames per packet

	// Scratch buffers for zero-allocation encoding
	scratchPaddedPulses []int8  // encodePulses: padded pulses
	scratchAbsPulses    []int   // encodePulses: absolute value pulses
	scratchSumPulses    []int   // encodePulses: sum per shell block
	scratchNRshifts     []int   // encodePulses: right shifts per shell block
	scratchLSFQ15       []int16 // lpcToLSF: LSF result in Q15
	scratchLPCQ16       []int32 // silkA2NLSF: LPC coefficients in Q16

	// Pitch detection scratch buffers
	scratchFrame8kHz      []float32 // detectPitch: downsampled to 8kHz
	scratchFrame4kHz      []float32 // detectPitch: downsampled to 4kHz
	scratchFrame16Fix     []int16   // detectPitch: input in int16 scale
	scratchFrame8Fix      []int16   // detectPitch: 8kHz int16 samples
	scratchFrame4Fix      []int16   // detectPitch: 4kHz int16 samples
	scratchResampler      []int32   // detectPitch: resampler buffer (int32)
	scratchPitchC         []float64 // detectPitch: autocorrelation
	scratchDSrch          []int     // detectPitch: candidate lags
	scratchDSrchCorr      []float64 // detectPitch: candidate correlations
	scratchDComp          []int16   // detectPitch: expanded search
	scratchC8kHz          []float64 // detectPitch: 8kHz correlations (flat array for 4 subframes)
	scratchPitchLags      []int     // detectPitch: output pitch lags
	scratchPitchCorrSt3   []float64 // detectPitch: stage3 correlations
	scratchPitchEnergySt3 []float64 // detectPitch: stage3 energies
	scratchPitchXcorr     []float32 // detectPitch: celt_pitch_xcorr scratch

	// Shell encoder scratch buffers (fixed sizes)
	scratchShellPulses1 [8]int // shellEncoder: level 1
	scratchShellPulses2 [4]int // shellEncoder: level 2
	scratchShellPulses3 [2]int // shellEncoder: level 3
	scratchShellPulses4 [1]int // shellEncoder: level 4

	// NSQ (computeNSQExcitation) scratch buffers
	scratchInputQ0          []int16 // PCM converted to int16
	scratchGainsUnqQ16      []int32 // unquantized gains in Q16 format
	scratchGainsQ16         []int32 // gains in Q16 format
	scratchPitchL           []int   // pitch lags for NSQ
	scratchArShpQ13         []int16 // AR shaping coefficients
	scratchLtpCoefQ14       []int16 // LTP coefficients
	scratchPredCoefQ12      []int16 // prediction coefficients
	scratchHarmShapeGainQ14 []int   // harmonic shaping gain
	scratchTiltQ14          []int   // tilt values
	scratchLfShpQ14         []int32 // low-frequency shaping
	scratchExcitation       []int32 // excitation output
	scratchPulses32         []int32 // LBRR pulse conversion
	scratchEcBufCopy        []byte  // range encoder buffer snapshot

	// LPC/Burg scratch buffers
	scratchLpcBurg       []float64 // LPC coefficients from Burg
	scratchWindowed      []float32 // computeLPCFromFrame: windowed PCM
	scratchLpcQ12        []int16   // burgLPCZeroAlloc: output LPC Q12
	scratchBurgAf        []float64 // burgModifiedFLPZeroAlloc: Af buffer
	scratchBurgCFirstRow []float64 // burgModifiedFLPZeroAlloc: CFirstRow
	scratchBurgCLastRow  []float64 // burgModifiedFLPZeroAlloc: CLastRow
	scratchBurgCAf       []float64 // burgModifiedFLPZeroAlloc: CAf
	scratchBurgCAb       []float64 // burgModifiedFLPZeroAlloc: CAb
	scratchBurgResult    []float64 // burgModifiedFLPZeroAlloc: result

	// LTP analysis scratch buffers
	scratchLtpInput     []float64 // LTP analysis: pitch analysis buffer as float64
	scratchLtpRes       []float64 // LTP analysis: LPC residual
	scratchLtpResF64    []float64 // Residual energy: float64 scratch
	scratchLpcResF64    []float64 // Residual energy: LPC residual scratch
	scratchPitchWsig    []float64 // Pitch analysis: windowed signal
	scratchPitchAuto    []float64 // Pitch analysis: autocorrelation
	scratchPitchRefl    []float64 // Pitch analysis: reflection coefficients
	scratchPitchA       []float64 // Pitch analysis: LPC coefficients
	scratchPitchRes32   []float32 // Pitch analysis: residual as float32
	scratchPitchInput32 []float32 // Pitch analysis: input buffer (float32)
	scratchPitchWsig32  []float32 // Pitch analysis: windowed signal (float32)
	scratchPitchAuto32  []float32 // Pitch analysis: autocorrelation (float32)
	scratchPitchRefl32  []float32 // Pitch analysis: reflection coefficients (float32)
	scratchPitchA32     []float32 // Pitch analysis: LPC coefficients (float32)

	// Noise shaping analysis scratch buffers
	scratchShapeInput    []float64 // noise shape analysis: input with lookahead
	scratchShapeWindow   []float64 // noise shape analysis: windowed buffer
	scratchShapeAutoCorr []float64 // noise shape analysis: autocorrelation
	scratchShapeRc       []float64 // noise shape analysis: reflection coeffs
	scratchShapeAr       []float64 // noise shape analysis: AR coeffs

	// LTP residual and residual energy scratch
	scratchLtpResF32    []float32 // LTP analysis: LTP residual with pre-length
	scratchLpcResF32    []float32 // Residual energy: LPC residual scratch (float32)
	scratchResNrg       []float64 // Gain processing: residual energies
	scratchPredCoefF32A []float32 // Gain processing: LPC coeffs (first half, float32)
	scratchPredCoefF32B []float32 // Gain processing: LPC coeffs (second half, float32)
	scratchPredCoefF64A []float64 // Gain processing: LPC coeffs (first half)
	scratchPredCoefF64B []float64 // Gain processing: LPC coeffs (second half)

	// A2NLSF scratch buffers (used by a2nlsfFLPInto / silkA2NLSFInto)
	scratchA2nlsfP    [9]int32  // silkA2NLSFInto: P polynomial (dd+1, max dd=8)
	scratchA2nlsfQ    [9]int32  // silkA2NLSFInto: Q polynomial
	scratchA2nlsfAQ16 [16]int32 // a2nlsfFLPInto: LPC Q16 conversion
	scratchA2nlsfNLSF [16]int16 // a2nlsfFLPInto: NLSF result

	// FindLPC interpolation scratch buffers
	scratchLpcXF64     []float64   // float32->float64 conversion
	scratchNlsf0Q15    [16]int16   // interpolated NLSF
	scratchLpcATmp     [16]float64 // LPC from NLSF
	scratchLpcResidual []float64   // LPC residual for energy
	scratchNlsfCos     [16]float64 // nlsfToLPCFloat: cosine values
	scratchNlsfP       [10]float64 // nlsfToLPCFloat: P polynomial (halfOrder+2, max 10)
	scratchNlsfQ       [10]float64 // nlsfToLPCFloat: Q polynomial (halfOrder+2, max 10)

	// LSF quantization scratch buffers
	scratchLsfResiduals   []int   // computeStage2ResidualsLibopus: residuals
	scratchEcIx           []int16 // computeStage2ResidualsLibopus / NLSF decode: ecIx
	scratchPredQ8         []uint8 // computeStage2ResidualsLibopus / NLSF decode: predQ8
	scratchResQ10         []int16 // computeStage2ResidualsLibopus / NLSF decode: resQ10
	scratchNLSFIndices    []int8  // NLSF decode indices (stage1 + residuals)
	scratchNLSFWeights    []int16 // NLSF VQ weights (Laroia)
	scratchNLSFWeightsTmp []int16 // NLSF weights for interpolated vector
	scratchNLSFTempQ15    []int16 // Interpolated NLSF scratch

	// Gain encoding scratch buffers
	scratchGains   []float32 // computeSubframeGains: output gains
	scratchGainInd []int8    // silkGainsQuant: gain indices

	// Rate control loop scratch buffers
	// Bit reservoir and rate control state (libopus parity)
	nBitsExceeded int // Bits produced in excess of target
	nBitsUsedLBRR int // Exponential moving average of LBRR overhead bits
	maxBits       int // Maximum bits allowed for current frame
	useVBR        bool

	// LP variable cutoff filter scratch buffer
	scratchLPInt16 []int16 // LP filter: int16 conversion for biquad filter

	// Output buffer scratch (standalone SILK mode)
	scratchOutput       []byte              // EncodeFrame: range encoder output
	scratchRangeEncoder rangecoding.Encoder // EncodeFrame: reusable range encoder

	// Stereo scratch buffers for zero-allocation stereo encoding
	scratchStereoMid      []float32 // mid channel (frameLength+2)
	scratchStereoSide     []float32 // side channel (frameLength+2)
	scratchStereoMidOut   []float32 // mid output (frameLength)
	scratchStereoSideOut  []float32 // side output (frameLength)
	scratchStereoMidQ0    []int16   // mid channel in Q0 (frameLength+2)
	scratchStereoSideQ0   []int16   // side channel in Q0 (frameLength+2)
	scratchStereoLPMidQ0  []int16   // LP filtered mid in Q0
	scratchStereoHPMidQ0  []int16   // HP filtered mid in Q0
	scratchStereoLPSideQ0 []int16   // LP filtered side in Q0
	scratchStereoHPSideQ0 []int16   // HP filtered side in Q0
	scratchStereoLPMid    []float32 // LP filtered mid
	scratchStereoHPMid    []float32 // HP filtered mid
	scratchStereoLPSide   []float32 // LP filtered side
	scratchStereoHPSide   []float32 // HP filtered side
	scratchStereoPadLeft  []float32 // padding buffer for left
	scratchStereoPadRight []float32 // padding buffer for right
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

// ensureUint64Slice ensures the slice has at least n elements.
func ensureUint64Slice(buf *[]uint64, n int) []uint64 {
	if cap(*buf) < n {
		*buf = make([]uint64, n)
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

	// Pitch analysis buffer: LTP memory + max frame (20ms).
	// Lookahead is zero-padded during residual computation.
	frameMs := 20
	fsKHz := config.SampleRate / 1000
	pitchBufSamples := (ltpMemLengthMs + frameMs) * fsKHz
	shapeBufSamples := (ltpMemLengthMs+laShapeMs)*fsKHz + frameSamples
	pitchResSamples := pitchBufSamples + laPitchMs*fsKHz
	maxPitchWinSamples := findPitchLpcWinMs * fsKHz
	if maxPitchWinSamples < 1 {
		maxPitchWinSamples = 1
	}

	enc := &Encoder{
		prevLSFQ15:        make([]int16, config.LPCOrder),
		inputBuffer:       make([]float32, shapeBufSamples), // Noise shaping buffer with lookahead
		lpcState:          make([]float32, config.LPCOrder),
		nsqState:          NewNSQState(),                    // Initialize NSQ state
		noiseShapeState:   NewNoiseShapeState(),             // Initialize noise shaping state
		pitchAnalysisBuf:  make([]float32, pitchBufSamples), // Pitch analysis buffer
		scratchLtpInput:   make([]float64, pitchResSamples),
		scratchLtpRes:     make([]float64, pitchResSamples),
		scratchPitchRes32: make([]float32, pitchResSamples),
		scratchPitchWsig:  make([]float64, maxPitchWinSamples),
		scratchPitchAuto:  make([]float64, maxFindPitchLpcOrder+1),
		scratchPitchRefl:  make([]float64, maxFindPitchLpcOrder),
		scratchPitchA:     make([]float64, maxFindPitchLpcOrder),
		bandwidth:         bandwidth,
		sampleRate:        config.SampleRate,
		lpcOrder:          config.LPCOrder,
		// Keep reset parity with libopus.
		pitchState:               PitchAnalysisState{prevLag: 0},
		snrDBQ7:                  0, // Match libopus zero-initialization (silk_init_encoder memset 0)
		lastControlTargetRateBps: 0,
		nFramesPerPacket:         1, // Default: 1 frame per packet (20ms)
		lbrrPulses:               lbrrPulses,
		lbrrGainIncreases:        7, // Default gain increase for LBRR
		previousGainIndex:        0,
	}
	enc.SetComplexity(10)
	enc.stereo.smthWidthQ14 = 16384
	// Match Opus-level init timing: before first control pass, libopus keeps
	// sNSQ zero-initialized. control_codec sets prev_gain_Q16 on first frame.
	if enc.nsqState != nil {
		enc.nsqState.prevGainQ16 = 0
	}
	// Set default bitrate matching libopus (opus_encoder.c: silk_mode.bitRate = 25000).
	// This ensures controlSNR is called on the first frame, matching libopus behavior
	// where silk_control_SNR is always invoked before encoding.
	enc.targetRateBps = 25000
	return enc
}

// Reset clears encoder state for a new stream.
func (e *Encoder) Reset() {
	e.haveEncoded = false
	e.previousLogGain = 0
	e.previousGainIndex = 0
	e.isPreviousFrameVoiced = false
	e.ecPrevLagIndex = 0
	e.ecPrevSignalType = 0
	e.targetRateBps = 0
	e.lastControlTargetRateBps = 0
	e.snrDBQ7 = 0
	e.sumLogGainQ7 = 0
	e.forceFirstFrameAfterReset = false

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
	e.stereo.smthWidthQ14 = 16384
	// Keep reset parity with libopus: prevLag resets to 0.
	e.pitchState = PitchAnalysisState{prevLag: 0}
	for i := range e.pitchAnalysisBuf {
		e.pitchAnalysisBuf[i] = 0
	}
	if e.nsqState != nil {
		e.nsqState.Reset() // Reset NSQ state
		// Match Opus-level reset timing: prev_gain_Q16 is applied during first
		// control pass after reset, not immediately on Reset().
		e.nsqState.prevGainQ16 = 0
	}
	if e.noiseShapeState != nil {
		e.noiseShapeState = NewNoiseShapeState() // Reset noise shaping state
	}
	e.ltpCorr = 0

	// Reset FEC/LBRR state
	e.lbrrEnabled = false
	e.lbrrPrevLastGainIdx = 10 // Default gain index (same as decoder reset)
	e.lbrrFlag = 0
	e.nFramesEncoded = 0
	for i := range e.lbrrFlags {
		e.lbrrFlags[i] = 0
	}
	for i := range e.lbrrIndices {
		e.lbrrIndices[i] = sideInfoIndices{}
	}
	for i := range e.lbrrFrameLength {
		e.lbrrFrameLength[i] = 0
		e.lbrrNbSubfr[i] = 0
	}
	for i := range e.lbrrPulses {
		for j := range e.lbrrPulses[i] {
			e.lbrrPulses[i][j] = 0
		}
	}
}

// SetComplexity sets the SILK encoder complexity (0-10) and related pitch parameters.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = complexity

	fsKHz := e.sampleRate / 1000
	if fsKHz < 1 {
		fsKHz = 1
	}

	switch {
	case complexity < 1:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = 3 * fsKHz
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 2:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 1
		e.warpingQ16 = 0
		e.nlsfSurvivors = 3
	case complexity < 3:
		e.pitchEstimationComplexity = 0
		e.pitchEstimationThresholdQ16 = 52429
		e.pitchEstimationLPCOrder = 6
		e.shapingLPCOrder = 12
		e.laShape = 3 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 2
	case complexity < 4:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 49807
		e.pitchEstimationLPCOrder = 8
		e.shapingLPCOrder = 14
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = 0
		e.nlsfSurvivors = 4
	case complexity < 6:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 48497
		e.pitchEstimationLPCOrder = 10
		e.shapingLPCOrder = 16
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 2
		e.warpingQ16 = int(float64(fsKHz) * warpingMultiplier * 65536.0)
		e.nlsfSurvivors = 6
	case complexity < 8:
		e.pitchEstimationComplexity = 1
		e.pitchEstimationThresholdQ16 = 47186
		e.pitchEstimationLPCOrder = 12
		e.shapingLPCOrder = 20
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = 3
		e.warpingQ16 = int(float64(fsKHz) * warpingMultiplier * 65536.0)
		e.nlsfSurvivors = 8
	default:
		e.pitchEstimationComplexity = 2
		e.pitchEstimationThresholdQ16 = 45875
		e.pitchEstimationLPCOrder = 16
		e.shapingLPCOrder = 24
		e.laShape = 5 * fsKHz
		e.nStatesDelayedDecision = maxDelDecStates
		e.warpingQ16 = int(float64(fsKHz) * warpingMultiplier * 65536.0)
		e.nlsfSurvivors = 16
	}

	if e.pitchEstimationLPCOrder > e.lpcOrder {
		e.pitchEstimationLPCOrder = e.lpcOrder
	}
	if e.shapingLPCOrder > maxShapeLpcOrder {
		e.shapingLPCOrder = maxShapeLpcOrder
	}
	if e.shapingLPCOrder < 2 {
		e.shapingLPCOrder = 2
	}
	if e.shapingLPCOrder&1 != 0 {
		e.shapingLPCOrder--
	}
	e.shapeWinLength = subFrameLengthMs*fsKHz + 2*e.laShape
}

// Complexity returns the current SILK complexity setting.
func (e *Encoder) Complexity() int {
	return e.complexity
}

// SetRangeEncoder sets the range encoder for the current frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// HaveEncoded returns whether at least one frame has been encoded.
func (e *Encoder) HaveEncoded() bool {
	return e.haveEncoded
}

func (e *Encoder) firstFrameAfterResetActive() bool {
	return !e.haveEncoded || e.forceFirstFrameAfterReset
}

// MarkEncoded marks that a frame has been successfully encoded.
func (e *Encoder) MarkEncoded() {
	e.haveEncoded = true
}

// ResetPacketState resets per-packet encoder state for standalone/shared encoding.
// This mirrors the standalone EncodeFrame() packet initialization.
func (e *Encoder) ResetPacketState() {
	e.nFramesEncoded = 0
	e.forceFirstFrameAfterReset = e.reducedDependency
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

// LPMode returns the LP variable cutoff filter mode.
// 0 = idle, <0 = switching down, >0 = switching up.
func (e *Encoder) LPMode() int {
	return e.lpState.Mode
}

// GetLPState returns a copy of the LP variable cutoff filter state.
func (e *Encoder) GetLPState() LPState {
	return e.lpState
}

// SetLPState restores the LP variable cutoff filter state.
func (e *Encoder) SetLPState(state LPState) {
	e.lpState = state
}

// InWBModeWithoutVariableLP returns true when the SILK encoder is in
// wideband (16kHz) mode with the variable LP filter inactive (mode==0).
// This matches libopus silk_mode.inWBmodeWithoutVariableLP.
func (e *Encoder) InWBModeWithoutVariableLP() bool {
	return e.sampleRate == 16000 && e.lpState.Mode == 0
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

// SetVADState sets speech activity and spectral tilt from VAD analysis.
// These values influence pitch thresholding and noise shaping.
func (e *Encoder) SetVADState(speechActivityQ8 int, inputTiltQ15 int, inputQualityBandsQ15 [4]int) {
	if speechActivityQ8 < 0 {
		speechActivityQ8 = 0
	}
	if speechActivityQ8 > 255 {
		speechActivityQ8 = 255
	}
	e.speechActivityQ8 = speechActivityQ8
	e.inputTiltQ15 = inputTiltQ15
	e.inputQualityBandsQ15 = inputQualityBandsQ15
	e.speechActivitySet = true
}

// LTPCorr returns the last pitch correlation estimate.
func (e *Encoder) LTPCorr() float32 {
	return e.ltpCorr
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

// SetTrace enables or disables encoder tracing for debugging parity.
func (e *Encoder) SetTrace(trace *EncoderTrace) {
	e.trace = trace
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

// GetLastTotalEnergy returns the total energy (C0) from the last LPC Burg analysis.
// Used for debugging gain computation from prediction residual.
func (e *Encoder) GetLastTotalEnergy() float64 {
	return e.lastTotalEnergy
}

// GetLastInvGain returns the inverse prediction gain from the last LPC Burg analysis.
// invGain = residualEnergy / totalEnergy, so residualEnergy = totalEnergy * invGain.
func (e *Encoder) GetLastInvGain() float64 {
	return e.lastInvGain
}

// GetLastNumSamples returns the number of samples analyzed in the last LPC Burg analysis.
func (e *Encoder) GetLastNumSamples() int {
	return e.lastNumSamples
}

// LastEncodedSignalInfo returns the signal type and quantization offset from
// the most recently encoded frame. Used by the hybrid encoder to provide
// silk_info feedback to CELT (libopus celt_encoder.c line 2466-2467).
//
// The offset is computed from silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType]:
//
//	Unvoiced Low:  100, Unvoiced High: 240
//	Voiced Low:     32, Voiced High:   100
func (e *Encoder) LastEncodedSignalInfo() (signalType, offset int) {
	return e.ecPrevSignalType, GetQuantizationOffset(e.ecPrevSignalType, e.lastQuantOffsetType)
}

// GetQuantizationOffset returns the quantization offset Q10 value for a given
// signal type and quantization offset type. This matches libopus
// silk_Quantization_Offsets_Q10[signalType>>1][quantOffsetType].
func GetQuantizationOffset(signalType, quantOffsetType int) int {
	return getQuantizationOffset(signalType, quantOffsetType)
}

// SetBitrate sets the target bitrate in bps (per channel).
func (e *Encoder) SetBitrate(bitrate int) {
	e.targetRateBps = bitrate
}

// UpdatePacketBitsExceeded applies libopus packet-level nBitsExceeded update.
// This must be called once per packet when shared range coding is used.
func (e *Encoder) UpdatePacketBitsExceeded(nBytesOut, payloadSizeMs, bitRateBps int) {
	if bitRateBps <= 0 || payloadSizeMs <= 0 {
		return
	}
	e.nBitsExceeded += nBytesOut * 8
	e.nBitsExceeded -= (bitRateBps * payloadSizeMs) / 1000
	if e.nBitsExceeded < 0 {
		e.nBitsExceeded = 0
	} else if e.nBitsExceeded > 10000 {
		e.nBitsExceeded = 10000
	}
}

// SetBitsExceeded sets packet-level bit reservoir excess state.
func (e *Encoder) SetBitsExceeded(bits int) {
	if bits < 0 {
		bits = 0
	} else if bits > 10000 {
		bits = 10000
	}
	e.nBitsExceeded = bits
}

// BitsExceeded returns packet-level bit reservoir excess state.
func (e *Encoder) BitsExceeded() int {
	return e.nBitsExceeded
}

// SetMaxBits sets the maximum number of bits allowed for the current frame.
func (e *Encoder) SetMaxBits(maxBits int) {
	e.maxBits = maxBits
}

// SetVBR enables or disables variable bitrate mode.
func (e *Encoder) SetVBR(vbr bool) {
	e.useVBR = vbr
	e.useCBR = !vbr
}

// SetReducedDependency enables/disables reduced dependency coding.
// This mirrors libopus OPUS_SET_PREDICTION_DISABLED SILK behavior.
func (e *Encoder) SetReducedDependency(enabled bool) {
	e.reducedDependency = enabled
	if !enabled {
		e.forceFirstFrameAfterReset = false
	}
}

// ReducedDependency reports whether reduced dependency coding is enabled.
func (e *Encoder) ReducedDependency() bool {
	return e.reducedDependency
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
