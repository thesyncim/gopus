// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the encoder struct that mirrors the decoder state
// for synchronized prediction.

package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// Encoder encodes audio frames using CELT transform coding.
// It maintains state across frames for proper audio continuity via energy
// prediction and overlap-add analysis.
//
// The encoder state mirrors the decoder state to ensure synchronized
// prediction. This includes:
// - Energy arrays for inter-frame prediction
// - Overlap buffer for MDCT overlap-add
// - Pre-emphasis filter state
// - RNG state for deterministic folding decisions
//
// Reference: RFC 6716 Section 4.3
type Encoder struct {
	// Pointer-bearing fields are grouped first so Go's GC stops scanning at
	// ptrdata, leaving scalar state and fixed-size value arrays (including
	// tfScratch) in the noscan tail.

	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder
	// perMode carries the per-mode CELT tables (band edges, widths, logN,
	// allocVectors, pulse cache) for a non-standard Opus Custom mode whose band
	// layout differs from the static 21-band 48 kHz tables (e.g. 48000/640,
	// nbEBands=19). It is nil for the standard, family, hybrid and QEXT paths,
	// which then stay byte-identical. Populated only by EnablePerModeTables,
	// called from the gopus_custom_modes celt<->custom plumbing.
	perMode *perModeTables

	// Energy state (persists across frames, mirrors decoder)
	prevEnergy        []celtGLog  // Current oldBandE history [MaxBands * channels]
	prevLogEnergy     []celtGLog  // Current oldLogE history [MaxBands * channels]
	prevEnergy2       []celtGLog  // Current oldLogE2 history [MaxBands * channels]
	energyError       []celtGLog  // Previous coarse quantization residuals [MaxBands * channels]
	overlapBuffer     []celtSig   // MDCT overlap [Overlap * channels]
	preemphState      []celtSig   // Pre-emphasis filter state [channels]
	prevBandLogEnergy []celtGLog  // Previous frame log-energy per band for spectral flux
	energyMask        []celtGLog  // Per-band surround masking: [21] mono, [42] stereo
	hpMem             []opusVal32 // High-pass filter memory [channels]
	delayBuffer       []opusRes   // Size = delayCompensation * channels
	prefilterMem      []celtSig
	lastBandLogE      []celtGLog // bandLogE (primary MDCT energies)
	lastBandLogE2     []celtGLog // bandLogE2 (secondary MDCT for transients)

	encoderQEXTFields
	encodeStageTrace encodeStageTraceState
	scratchShape     encodeScratchShape

	// Pointer-bearing scratch structs and dynamic allocation result.
	// dynallocScratch sits last so its inline pointer-free band buffers (~800 B)
	// merge directly into Encoder's GC noscan tail.
	lastDynalloc    DynallocResult
	tonalityScratch tonalityScratch
	bandEncScratch  bandEncodeScratch
	scratch         encoderScratch
	dynallocScratch DynallocScratch

	// --- Pointer-free (noscan) tail ---

	// Scratch buffers for TF analysis (pointer-free fixed arrays)
	tfScratch TFAnalysisScratch

	// Configuration (mirrors decoder)
	channels       int32         // libopus CELTEncoder.channels
	streamChannels int32         // coded channels, mirrors CELT_SET_CHANNELS
	sampleRate     int32         // 48000 for standard CELT, 96000 for native HD mode.
	lsbDepth       int32         // Input LSB depth (8-24 bits)
	bandwidth      CELTBandwidth // Active bandwidth cap (NB..FB)
	// upsample mirrors libopus CELTEncoder.upsample = resampling_factor(Fs):
	// 1 at 48 kHz, 2/3/4/6 at 24/16/12/8 kHz. EncodeFrame multiplies the API-rate
	// frame size by upsample to the 48 kHz core block and zero-stuffs the input in
	// pre-emphasis (celt_encode_with_ec frame_size *= st->upsample; celt_preemphasis
	// with Nu = N/upsample). 0/1 both mean no upsampling.
	upsample int32

	// hd96kOverlap and hd96kPreemph hold the native 96 kHz HD analysis
	// parameters (overlap 240, 2-tap HD pre-emphasis). hd96kOverlap == 0
	// selects the 48 kHz path (Overlap constant), keeping the default build
	// unchanged. Set by EnableHD96kMode (gopus_qext).
	hd96kOverlap int
	hd96kPreemph [4]float32
	// customScaleBase is the mode short-MDCT size used to scale band-bin edges
	// for a non-standard Opus Custom mode in the Fs==400*shortMdctSize family.
	// Zero selects the 48 kHz base (Overlap=120), keeping the default and 48 kHz
	// paths byte-identical. When non-zero the band-bin scale is
	// frameSize/customScaleBase == 1<<LM, matching libopus eBands[i]<<LM.
	customScaleBase int
	// customEffBands is the effEBands clamp for the active custom mode (0 selects
	// the standard 21-band clamp).
	customEffBands int
	// overlapMax mirrors libopus st->overlap_max for CELT silence detection.
	// It tracks max absolute amplitude over the last overlap region.
	overlapMax opusVal32

	// RNG state (for deterministic folding decisions)
	rng uint32

	// Frame counting for intra mode decisions
	frameCount int32 // Number of frames encoded (0 = first frame uses intra mode)
	// Coarse-energy intra/inter decision state (libopus delayedIntra).
	delayedIntra opusVal32
	forceIntra   bool
	// CELT prediction control mirrors CELT_SET_PREDICTION:
	// 0 => force_intra=1, disable_pf=1
	// 1 => force_intra=0, disable_pf=1
	// 2 => force_intra=0, disable_pf=0
	disablePrefilter bool
	// Consecutive transient frames (used for anti-collapse flag)
	consecTransient int32

	// Allocation history for skip decisions
	lastCodedBands int32 // Previous coded band count (0 = uninitialized)
	intensity      int32 // Previous intensity stereo decision (libopus hysteresis state)

	// Bitrate control
	targetBitrate int32 // st->bitrate in bits per second, or BitrateMax to fill the payload budget
	frameBits     int32 // Per-frame bit budget for coarse energy (set during encoding)
	// coarseAvailableBytes mirrors libopus quant_coarse_energy() nbAvailableBytes
	// while coarseAvailableSet is true; otherwise the coarse intra/decay
	// decisions read budget/8.
	coarseAvailableBytes int32
	coarseAvailableSet   bool
	// coarsePassKept is set while the range coder holds the intra flag and
	// the coarse-energy pass decideIntraMode selected, with its quantized
	// energies and errors in the coarse scratch; EncodeCoarseEnergy applies
	// them instead of encoding the pass again.
	coarsePassKept   bool
	maxPayloadBytes  int32 // Optional per-frame payload cap (excludes TOC byte)
	customSignalling bool  // celt_encode_with_ec st->signalling: custom header is in packet.
	vbr              bool
	constrainedVBR   bool
	// Constrained-VBR state mirrors libopus CELT encoder cadence.
	// Units are Q3 bits unless noted.
	vbrReservoir int32
	vbrOffset    int32
	vbrDrift     int32
	vbrCount     int32

	// Complexity control (0-10)
	complexity int32

	// Spread decision state (persistent across frames for hysteresis)
	spreadDecision int32 // Current spread decision (0-3)
	tonalAverage   int32 // Running average for spread decision hysteresis
	hfAverage      int32 // High frequency average for tapset decision
	tapsetDecision int32 // Tapset decision (0, 1, or 2)

	// Tonality analysis state (for VBR decisions)
	lastTonality     opusVal16 // Running average tonality for smoothing
	lastStereoSaving opusVal16 // Running stereo_saving estimate from alloc_trim analysis
	lastPitchChange  bool      // pitch_change of the most recent hybrid prefilter run
	specAvg          celtGLog  // Smoothed spectral average for temporal VBR (libopus st->spec_avg)
	lastTemporalVBR  celtGLog  // temporal_vbr of the most recently analysed frame (compute_vbr input)

	// Analysis bandwidth state used by bit allocation gating.
	// This mirrors libopus use of st->analysis.bandwidth for clt_compute_allocation().
	analysisBandwidth     int  // 1..20 bandwidth index from previous frame analysis
	analysisValid         bool // True after at least one analysis update
	analysisActivity      opusVal16
	analysisLeakBoost     [leakBands]uint8
	analysisTonality      opusVal16
	analysisTonalitySlope opusVal16
	analysisMaxPitchRatio opusVal16

	// Hybrid mode flag
	// When true, postfilter flag encoding is skipped per RFC 6716 Section 3.2
	// Reference: libopus celt_encoder.c line 2047-2048
	hybrid bool
	// SILK side information forwarded by the Opus hybrid wrapper.
	// libopus uses this in hybrid CELT for weak-transient gating and
	// bitrate targeting. The generic EncodeFrame path only needs the
	// weak-transient part for transition prefill parity.
	silkSignalType int
	silkOffset     int

	// LFE mode flag.
	// When true, encoder applies low-frequency-effects constraints.
	lfe bool

	// Phase inversion disabled for stereo encoding
	// When true, disables stereo phase inversion decorrelation
	phaseInversionDisabled bool

	// dcRejectEnabled controls whether EncodeFrame applies dc_reject().
	// When CELT is driven by the Opus encoder, dc_reject is already applied,
	// so this should be false to avoid double filtering.
	dcRejectEnabled bool

	// lsbQuantizationEnabled controls whether EncodeFrame rounds input samples
	// to the configured LSB depth before any CELT-local preprocessing.
	// Top-level Opus encoding already does this before dc_reject, so CELT must
	// skip it there to avoid perturbing the filtered samples.
	lsbQuantizationEnabled bool

	// delayCompensationEnabled controls whether EncodeFrame prepends the
	// Fs/250 CELT lookahead history. Standalone CELT defaults this to true;
	// top-level Opus wiring should disable it to avoid double-compensation.
	delayCompensationEnabled bool

	// Prefilter (comb filter) state for postfilter signaling.
	// These mirror libopus CELT encoder fields used by run_prefilter().
	prefilterPeriod   int
	prefilterGain     float32
	prefilterTapset   int
	scratchShapeValid bool
	// Packet loss expectation (0-100) for prefilter gain scaling.
	packetLoss int32
}

// NewEncoder creates a new CELT encoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The encoder is ready to process CELT frames after creation.
//
// The initialization mirrors libopus encoder reset state:
// - prevEnergy starts at 0.0 (oldBandE cleared)
// - RNG seed 0 (matches libopus initialization)
func NewEncoder(channels int) *Encoder {
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	// Carve fixed-size float32 state slices from one contiguous backing array
	// to minimize heap objects per Encoder.
	bandLen := MaxBands * channels
	overlapLen := Overlap * channels
	delayLen := DelayCompensation * channels
	prefilterLen := combFilterMaxPeriod * channels
	stateSlab := make([]float32, 5*bandLen+overlapLen+2*channels+delayLen+prefilterLen)
	off := 0
	carve := func(n int) []float32 {
		s := stateSlab[off : off+n : off+n]
		off += n
		return s
	}

	e := &Encoder{
		channels:       int32(channels),
		streamChannels: int32(channels),
		sampleRate:     48000, // Standard-mode default; EnableHD96kMode selects 96 kHz.
		lsbDepth:       24,    // Default to full 24-bit depth
		bandwidth:      CELTFullband,

		// Allocate energy arrays for all bands and channels
		prevEnergy:    carve(bandLen),
		prevLogEnergy: carve(bandLen),
		prevEnergy2:   carve(bandLen),
		energyError:   carve(bandLen),

		// Overlap buffer for MDCT overlap-add analysis
		// Size is Overlap (120) samples per channel
		overlapBuffer: carve(overlapLen),

		// Pre-emphasis filter state, one per channel
		preemphState: carve(channels),

		// Initialize RNG to zero (libopus default)
		rng: 0,

		// Complexity defaults to max quality (libopus default)
		complexity: 10,
		// libopus initializes delayedIntra to 1.
		delayedIntra: 1.0,

		// Initialize spread decision state (libopus defaults to SPREAD_NORMAL)
		// Reference: libopus celt_encoder.c line 3088-3089
		spreadDecision: spreadNormal,
		tonalAverage:   256, // libopus initializes to 256
		hfAverage:      0,
		tapsetDecision: 0,

		// Initialize tonality analysis state
		prevBandLogEnergy:     carve(bandLen),
		lastTonality:          opusVal16(0.5), // Start with neutral tonality estimate
		lastStereoSaving:      0.0,
		lastPitchChange:       false,
		analysisBandwidth:     20,
		analysisValid:         false,
		analysisActivity:      0.0,
		analysisTonality:      0.0,
		analysisTonalitySlope: 0.0,
		analysisMaxPitchRatio: 0.0,

		// DC rejection (high-pass) filter memory, one per channel
		hpMem: carve(channels),

		// Apply dc_reject by default for standalone CELT usage
		dcRejectEnabled: true,

		// Standalone CELT also owns the initial LSB-depth rounding.
		lsbQuantizationEnabled: true,

		// Standalone CELT defaults to Opus-style delay compensation.
		// Top-level Opus integration should disable this to avoid double-applying.
		delayCompensationEnabled: true,

		// Delay buffer for lookahead (192 samples at 48kHz = 4ms)
		// This matches libopus delay_compensation
		delayBuffer: carve(delayLen),

		// Prefilter state (comb filter history) for postfilter signaling.
		// libopus zero-initializes this state on reset.
		prefilterPeriod: 0,
		prefilterGain:   0,
		prefilterTapset: 0,
		prefilterMem:    carve(prefilterLen),

		// A standalone encoder codes VBR at 64 kb/s per channel until
		// SetBitrate/SetVBR configure it.
		targetBitrate: int32(64000 * channels),
		vbr:           true,
	}

	// celt_encoder.c initializes oldBandE to zero and oldLogE/oldLogE2 to -28.
	for i := range e.prevLogEnergy {
		e.prevLogEnergy[i] = -28
		e.prevEnergy2[i] = -28
	}

	return e
}

// SetAnalysisBandwidth provides the analysis-derived bandwidth index (1..20)
// used by allocation gating in clt_compute_allocation().
func (e *Encoder) SetAnalysisBandwidth(bandwidth int, valid bool) {
	if !valid {
		e.analysisValid = false
		e.analysisActivity = 0
		e.analysisTonality = 0
		e.analysisTonalitySlope = 0
		e.analysisMaxPitchRatio = 0
		for i := range e.analysisLeakBoost {
			e.analysisLeakBoost[i] = 0
		}
		return
	}
	if bandwidth < 1 {
		bandwidth = 1
	}
	if bandwidth > 20 {
		bandwidth = 20
	}
	e.analysisBandwidth = bandwidth
	e.analysisValid = true
	e.analysisActivity = 0
	e.analysisTonality = 0
	e.analysisTonalitySlope = 0
	e.analysisMaxPitchRatio = 1.0
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
}

// SetAnalysisInfo provides analysis-derived state from the top-level Opus analysis
// pipeline. This mirrors libopus use of AnalysisInfo in CELT dynalloc.
func (e *Encoder) SetAnalysisInfo(bandwidth int, leakBoost [leakBands]uint8, activity, tonalitySlope opusVal16, maxPitchRatio opusVal16, valid bool) {
	e.SetAnalysisInfoWithTonality(bandwidth, leakBoost, activity, 0, tonalitySlope, maxPitchRatio, valid)
}

// SetAnalysisInfoWithTonality provides the full AnalysisInfo subset used by
// libopus CELT decisions, including analysis.tonality for compute_vbr().
func (e *Encoder) SetAnalysisInfoWithTonality(bandwidth int, leakBoost [leakBands]uint8, activity, tonality, tonalitySlope opusVal16, maxPitchRatio opusVal16, valid bool) {
	if !valid {
		e.SetAnalysisBandwidth(0, false)
		return
	}
	if bandwidth < 1 {
		bandwidth = 1
	}
	if bandwidth > 20 {
		bandwidth = 20
	}
	e.analysisBandwidth = bandwidth
	e.analysisValid = true
	e.analysisActivity = activity
	e.analysisLeakBoost = leakBoost
	if tonality < 0 {
		tonality = 0
	} else if tonality > 1 {
		tonality = 1
	}
	e.analysisTonality = tonality
	e.analysisTonalitySlope = tonalitySlope
	if maxPitchRatio < 0 {
		maxPitchRatio = 0
	}
	if maxPitchRatio > 1 {
		maxPitchRatio = 1
	}
	e.analysisMaxPitchRatio = maxPitchRatio
}

// AnalysisBandwidth returns the current analysis-derived bandwidth index.
func (e *Encoder) AnalysisBandwidth() int {
	return e.analysisBandwidth
}

func (e *Encoder) dynallocLeakBoost() []uint8 {
	leak := e.analysisLeakBoost[:]
	if !e.analysisValid {
		return leak
	}
	return leak
}

// Reset clears encoder state for a new stream.
// It clears frame and analysis history while retaining the active channel,
// sample-rate, and mode configuration.
func (e *Encoder) Reset() {
	// Clear energy arrays (match libopus reset: oldBandE=0).
	for i := range e.prevEnergy {
		e.prevEnergy[i] = 0
		e.prevLogEnergy[i] = -28
		e.prevEnergy2[i] = -28
		e.energyError[i] = 0
	}

	// Clear overlap buffer
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}
	e.overlapMax = 0

	// Reset prefilter state
	e.prefilterPeriod = 0
	e.prefilterGain = 0
	e.prefilterTapset = 0
	for i := range e.prefilterMem {
		e.prefilterMem[i] = 0
	}
	// Clear pre-emphasis state
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}

	// Reset RNG to zero (libopus default)
	e.rng = 0

	// Clear range encoder reference
	e.rangeEncoder = nil

	// Reset frame counter
	e.frameCount = 0
	e.frameBits = 0
	e.coarseAvailableBytes = 0
	e.coarseAvailableSet = false
	e.coarsePassKept = false
	e.maxPayloadBytes = 0
	e.lastDynalloc = DynallocResult{}
	clear(e.lastBandLogE)
	clear(e.lastBandLogE2)
	e.delayedIntra = 1.0
	e.lastCodedBands = 0
	e.intensity = 0
	e.consecTransient = 0
	e.vbrReservoir = 0
	e.vbrOffset = 0
	e.vbrDrift = 0
	e.vbrCount = 0
	e.clearLastQEXTPayload()
	e.resetQEXTEnergyHistory()

	// Reset spread decision state (match libopus init values)
	// Reference: libopus celt_encoder.c line 3088-3089
	e.spreadDecision = spreadNormal
	e.tonalAverage = 256
	e.hfAverage = 0
	e.tapsetDecision = 0

	// Reset tonality analysis state
	for i := range e.prevBandLogEnergy {
		e.prevBandLogEnergy[i] = 0
	}
	e.lastTonality = opusVal16(0.5)
	e.lastStereoSaving = 0
	e.lastPitchChange = false
	e.specAvg = 0
	e.lastTemporalVBR = 0
	e.silkSignalType = 0
	e.silkOffset = 0
	e.analysisBandwidth = 20
	e.analysisValid = false
	e.analysisActivity = 0
	e.analysisTonality = 0
	e.analysisTonalitySlope = 0
	e.analysisMaxPitchRatio = 0
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
	if len(e.energyMask) > 0 {
		clear(e.energyMask)
		e.energyMask = e.energyMask[:0]
	}

	// Clear DC rejection filter state
	for i := range e.hpMem {
		e.hpMem[i] = 0
	}

	// Clear delay buffer
	for i := range e.delayBuffer {
		e.delayBuffer[i] = 0
	}

}

// SetEnergyMask sets per-band surround masking for CELT surround control.
// Expected sizes: 21 values for mono, 42 values for stereo.
// Invalid sizes clear the mask.
func (e *Encoder) SetEnergyMask(mask []float32) {
	needed := MaxBands * int(e.channels)
	if needed <= 0 || len(mask) < needed {
		if len(e.energyMask) > 0 {
			clear(e.energyMask)
			e.energyMask = e.energyMask[:0]
		}
		return
	}
	if cap(e.energyMask) < needed {
		e.energyMask = make([]celtGLog, needed)
	} else {
		e.energyMask = e.energyMask[:needed]
	}
	copy(e.energyMask, mask[:needed])
}

// EnergyMask returns the current per-band surround mask.
func (e *Encoder) EnergyMask() []float32 {
	out := make([]float32, len(e.energyMask))
	copy(out, e.energyMask)
	return out
}

// SetComplexity sets encoder complexity (0-10).
// Higher values use more CPU for better quality.
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = int32(complexity)
}

// SetVBR enables or disables variable bitrate mode.
func (e *Encoder) SetVBR(enabled bool) {
	e.vbr = enabled
}

// VBR reports whether variable bitrate mode is enabled.
func (e *Encoder) VBR() bool {
	return e.vbr
}

// SetConstrainedVBR enables or disables constrained VBR mode.
func (e *Encoder) SetConstrainedVBR(enabled bool) {
	e.constrainedVBR = enabled
}

// ConstrainedVBR reports whether VBR is constrained by the configured target.
func (e *Encoder) ConstrainedVBR() bool {
	return e.constrainedVBR
}

// SetPrediction controls CELT inter-frame prediction behavior.
// Valid modes mirror libopus CELT_SET_PREDICTION:
// - 0: disable prediction and force intra (disable_pf=1, force_intra=1)
// - 1: disable prefilter only (disable_pf=1, force_intra=0)
// - 2: normal prediction (disable_pf=0, force_intra=0)
func (e *Encoder) SetPrediction(mode int) {
	if mode < 0 {
		mode = 0
	}
	if mode > 2 {
		mode = 2
	}
	e.disablePrefilter = mode <= 1
	e.forceIntra = mode == 0
}

// Prediction returns the active CELT prediction mode (0, 1, or 2).
func (e *Encoder) Prediction() int {
	if e.forceIntra {
		return 0
	}
	if e.disablePrefilter {
		return 1
	}
	return 2
}

// SetDCRejectEnabled controls whether EncodeFrame applies dc_reject().
// For Opus-level encoding, this should be false because dc_reject is already applied.
func (e *Encoder) SetDCRejectEnabled(enabled bool) {
	e.dcRejectEnabled = enabled
}

// DCRejectEnabled reports whether dc_reject is applied in EncodeFrame.
func (e *Encoder) DCRejectEnabled() bool {
	return e.dcRejectEnabled
}

// SetLSBQuantizationEnabled controls whether EncodeFrame rounds inputs to the
// configured LSB depth before CELT-local processing.
func (e *Encoder) SetLSBQuantizationEnabled(enabled bool) {
	e.lsbQuantizationEnabled = enabled
}

// LSBQuantizationEnabled reports whether EncodeFrame applies the CELT-local
// LSB-depth rounding step.
func (e *Encoder) LSBQuantizationEnabled() bool {
	return e.lsbQuantizationEnabled
}

// SetDelayCompensationEnabled controls whether EncodeFrame prepends Fs/250
// lookahead history before CELT analysis/quantization.
func (e *Encoder) SetDelayCompensationEnabled(enabled bool) {
	e.delayCompensationEnabled = enabled
}

// DelayCompensationEnabled reports whether EncodeFrame applies lookahead
// delay compensation.
func (e *Encoder) DelayCompensationEnabled() bool {
	return e.delayCompensationEnabled
}

// SetTopLevelDelayCompensatedInput tells the standalone CELT core whether the
// Opus wrapper has already supplied delay-compensated input.
func (e *Encoder) SetTopLevelDelayCompensatedInput(alreadyCompensated bool) {
	e.delayCompensationEnabled = !alreadyCompensated
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return int(e.complexity)
}

// SetRangeEncoder sets the range encoder for the current frame.
// This must be called before encoding each frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return int(e.channels)
}

// SetStreamChannels mirrors libopus CELT_SET_CHANNELS.
func (e *Encoder) SetStreamChannels(channels int) {
	if channels < 1 || channels > int(e.channels) {
		return
	}
	e.streamChannels = int32(channels)
}

// StreamChannels returns the coded channel count.
func (e *Encoder) StreamChannels() int {
	return int(e.streamChannels)
}

func (e *Encoder) codedChannels() int {
	channels := min(max(int(e.streamChannels), 1), int(e.channels))
	return channels
}

// SampleRate returns the active CELT mode's sample rate in hertz.
func (e *Encoder) SampleRate() int {
	return int(e.sampleRate)
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy encoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (e *Encoder) PrevEnergy() []CeltGLog {
	out := make([]celtGLog, len(e.prevEnergy))
	copy(out, e.prevEnergy)
	return out
}

// PrevEnergy2 returns the encoder's oldLogE2 band history.
func (e *Encoder) PrevEnergy2() []CeltGLog {
	out := make([]celtGLog, len(e.prevEnergy2))
	copy(out, e.prevEnergy2)
	return out
}

// SetPrevEnergy advances the encoder energy history as a non-transient frame.
func (e *Encoder) SetPrevEnergy(energies []celtGLog) {
	copy(e.prevEnergy2, e.prevLogEnergy)
	copy(e.prevLogEnergy, e.prevEnergy)
	copy(e.prevEnergy, energies)
}

// SetPrevEnergyWithPrev advances CELT energy history from the supplied
// oldBandE and current band energies as a non-transient frame.
func (e *Encoder) SetPrevEnergyWithPrev(prev, energies []celtGLog) {
	copy(e.prevEnergy2, e.prevLogEnergy)
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevLogEnergy, prev)
	} else {
		copy(e.prevLogEnergy, e.prevEnergy)
	}
	copy(e.prevEnergy, energies)
}

// OverlapBuffer returns the overlap buffer for MDCT analysis.
// Size is Overlap * channels samples.
func (e *Encoder) OverlapBuffer() []float32 {
	out := make([]float32, len(e.overlapBuffer))
	copySigToFloat32(out, e.overlapBuffer)
	return out
}

// SetOverlapBuffer copies the given samples to the overlap buffer.
func (e *Encoder) SetOverlapBuffer(samples []float32) {
	copyFloat32ToSig(e.overlapBuffer, samples)
}

// PreemphState returns the pre-emphasis filter state.
// One value per channel.
func (e *Encoder) PreemphState() []float32 {
	return e.preemphState
}

// RNG returns the current RNG state.
// After encoding, this contains the final range coder state for verification.
func (e *Encoder) RNG() uint32 {
	return e.rng
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after EncodeFrame() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	return e.rng
}

// SetRNG sets the RNG state.
func (e *Encoder) SetRNG(seed uint32) {
	e.rng = seed
}

// NextRNG advances the RNG and returns the new value.
// Uses the same LCG as libopus for deterministic behavior (D03-04-03).
func (e *Encoder) NextRNG() uint32 {
	e.rng = e.rng*1664525 + 1013904223
	return e.rng
}

// GetEnergy returns the energy for a specific band and channel from prevEnergy.
func (e *Encoder) GetEnergy(band, channel int) float32 {
	stride := e.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(e.channels) {
		return 0
	}
	return float32(e.prevEnergy[channel*stride+band])
}

// SetEnergy sets the energy for a specific band and channel.
func (e *Encoder) SetEnergy(band, channel int, energy float32) {
	stride := e.predStride()
	if band < 0 || band >= stride || channel < 0 || channel >= int(e.channels) {
		return
	}
	e.prevEnergy[channel*stride+band] = celtGLog(energy)
}

// IsIntraFrame returns a conservative pre-encode advisory value.
//
// The actual intra/inter decision is made inside EncodeFrame once band energies
// are available, so this helper is intentionally not a packet-exact predictor
// of the libopus-style two-pass coarse-energy search.
func (e *Encoder) IsIntraFrame() bool {
	return false
}

// IncrementFrameCount increments the frame counter.
// Call this after successfully encoding a frame.
func (e *Encoder) IncrementFrameCount() {
	e.frameCount++
}

// FrameCount returns the number of frames encoded.
func (e *Encoder) FrameCount() int {
	return int(e.frameCount)
}

// SetBitrate sets st->bitrate, the rate in bits per second the VBR target and
// the CBR payload size derive from; BitrateMax makes the encoder fill its
// payload budget. It stores the rate as given: callers forwarding
// OPUS_SET_BITRATE apply the range checks of celt_encoder_ctl.
func (e *Encoder) SetBitrate(bps int) {
	e.targetBitrate = int32(bps)
}

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return int(e.targetBitrate)
}

// SetMaxPayloadBytes sets an optional payload cap for the next CELT frame.
// The value excludes the Opus TOC byte. A value <= 0 disables the cap.
func (e *Encoder) SetMaxPayloadBytes(maxPayloadBytes int) {
	if maxPayloadBytes < 0 {
		maxPayloadBytes = 0
	}
	e.maxPayloadBytes = int32(maxPayloadBytes)
}

// SetCustomSignalling mirrors st->signalling for opus_custom_encode. The
// signalled header consumes one byte from finite bitrate accounting; the
// caller still supplies the payload cap after removing that byte.
func (e *Encoder) SetCustomSignalling(enabled bool) {
	e.customSignalling = enabled
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
// This affects the prefilter gain for improved loss resilience.
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLoss = int32(lossPercent)
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return int(e.packetLoss)
}

// SetLSBDepth sets the input signal LSB depth (8-24 bits).
// This affects masking/spread decisions at low bitrates.
func (e *Encoder) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	e.lsbDepth = int32(depth)
}

// LSBDepth returns the current input signal LSB depth.
func (e *Encoder) LSBDepth() int {
	if e.lsbDepth <= 0 {
		return 24
	}
	return int(e.lsbDepth)
}

// SetBandwidth sets the CELT bandwidth cap used for band allocation.
func (e *Encoder) SetBandwidth(bw CELTBandwidth) {
	if bw < CELTNarrowband || bw > CELTFullband {
		bw = CELTFullband
	}
	e.bandwidth = bw
}

// SetUpsample sets the CELT input upsample factor, mirroring libopus
// CELTEncoder.upsample = resampling_factor(Fs). At sub-48 kHz API rates the
// encoder consumes native-Fs frame sizes and the CELT core block is
// frameSize*upsample (the input is zero-stuffed in pre-emphasis). Factor 1 (or
// 0) is the 48 kHz path.
func (e *Encoder) SetUpsample(factor int) {
	if factor < 1 {
		factor = 1
	}
	e.upsample = int32(factor)
}

// effectiveUpsample returns the CELT input upsample factor (>=1).
func (e *Encoder) effectiveUpsample() int {
	if e.upsample <= 0 {
		return 1
	}
	return int(e.upsample)
}

// Bandwidth returns the active CELT bandwidth cap.
func (e *Encoder) Bandwidth() CELTBandwidth {
	return e.bandwidth
}

// scaleBase returns the active mode's short-MDCT size. It is Overlap (120) for
// standard 48 kHz modes, 240 for native 96 kHz CELT, and the configured size
// for a custom mode.
func (e *Encoder) scaleBase() int {
	if e.customScaleBase > 0 {
		return e.customScaleBase
	}
	if e.hd96kOverlap > 0 && e.sampleRate == 96000 {
		return e.hd96kOverlap
	}
	return Overlap
}

// modeConfig returns the frame-size-dependent ModeConfig for the active mode.
// Custom-family and native 96 kHz HD modes derive LM from the active mode's
// short-MDCT size instead of the 48 kHz grid, matching their short-block count.
func (e *Encoder) modeConfig(frameSize int) ModeConfig {
	if e.customScaleBase > 0 || (e.hd96kOverlap > 0 && e.sampleRate == 96000) {
		shortMDCTSize := e.scaleBase()
		nbShort := frameSize / shortMDCTSize
		lm := 0
		for (1 << lm) < nbShort {
			lm++
		}
		eff := MaxBands
		if e.customEffBands > 0 {
			eff = e.customEffBands
		}
		return ModeConfig{
			FrameSize:   frameSize,
			ShortBlocks: nbShort,
			LM:          lm,
			EffBands:    eff,
			MDCTSize:    frameSize,
		}
	}
	return GetModeConfig(frameSize)
}

// toneDetectFs returns the active mode's sample rate for tone_detect and
// transient_analysis. Standard modes use 48000, native HD mode uses 96000, and
// scaled custom modes use their configured rate, matching libopus mode->Fs.
func (e *Encoder) toneDetectFs() int {
	if fs := e.celtModeFs(); fs > 0 {
		return fs
	}
	return 48000
}

// validFrameSize reports whether frameSize is acceptable for the active mode.
func (e *Encoder) validFrameSize(frameSize int) bool {
	if frameSize <= 0 {
		return false
	}
	if e.customScaleBase > 0 {
		for lm := 0; lm <= 3; lm++ {
			if e.customScaleBase<<lm == frameSize {
				return true
			}
		}
		return false
	}
	if e.hd96kOverlap == 240 && e.sampleRate == 96000 {
		return frameSize == 240 || frameSize == 480 || frameSize == 960 || frameSize == 1920
	}
	return frameSize == 120 || frameSize == 240 || frameSize == 480 || frameSize == 960
}

func (e *Encoder) effectiveBandCount(frameSize int) int {
	if e.perMode != nil {
		return min(e.perMode.nbEBands, e.customEffBands)
	}
	nbBands := e.modeConfig(frameSize).EffBands
	if e.customEffBands > 0 && e.customEffBands < nbBands {
		nbBands = e.customEffBands
	}
	bwBands := EffectiveBandsForFrameSize(e.bandwidth, frameSize)
	if bwBands < nbBands {
		nbBands = bwBands
	}
	if nbBands < 1 {
		nbBands = 1
	}
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	return nbBands
}

// TapsetDecision returns the current tapset decision (0, 1, or 2).
// The tapset controls the window taper used in the prefilter/postfilter comb filter:
// - 0: Narrow taper (concentrated energy)
// - 1: Medium taper (balanced)
// - 2: Wide taper (spread energy)
// This is computed during SpreadingDecision when updateHF=true.
// Reference: libopus celt/bands.c spreading_decision() and celt/celt.c comb_filter()
func (e *Encoder) TapsetDecision() int {
	return int(e.tapsetDecision)
}

// SetTapsetDecision sets the tapset decision value.
// Valid values are 0, 1, or 2.
func (e *Encoder) SetTapsetDecision(tapset int) {
	if tapset < 0 {
		tapset = 0
	}
	if tapset > 2 {
		tapset = 2
	}
	e.tapsetDecision = int32(tapset)
}

// HFAverage returns the high-frequency average used for tapset decision.
// This is updated during SpreadingDecision when updateHF=true.
func (e *Encoder) HFAverage() int {
	return int(e.hfAverage)
}

// SetHybrid sets the hybrid mode flag.
// When true, postfilter flag encoding is skipped per RFC 6716 Section 3.2.
// Reference: libopus celt_encoder.c line 2047-2048:
//
//	if(!hybrid && tell+16<=total_bits) ec_enc_bit_logp(enc, 0, 1);
func (e *Encoder) SetHybrid(hybrid bool) {
	e.hybrid = hybrid
}

// IsHybrid returns true if the encoder is in hybrid mode.
func (e *Encoder) IsHybrid() bool {
	return e.hybrid
}

// SetSilkInfo stores the current SILK signal classification for hybrid CELT.
// This mirrors libopus CELT_SET_SILK_INFO.
func (e *Encoder) SetSilkInfo(signalType, offset int) {
	e.silkSignalType = signalType
	e.silkOffset = offset
}

// FillHybridTFResolution applies the libopus hybrid fixed-TF fallback used when
// variable TF analysis is disabled.
func FillHybridTFResolution(tfRes []int32, end int, transient, weakTransient, allowWeakTransients bool) int {
	if end > len(tfRes) {
		end = len(tfRes)
	}
	if end < 0 {
		end = 0
	}
	tfSelect := 0
	switch {
	case weakTransient:
		for i := 0; i < end; i++ {
			tfRes[i] = 1
		}
	case allowWeakTransients:
		for i := 0; i < end; i++ {
			tfRes[i] = 0
		}
		if transient {
			tfSelect = 1
		}
	default:
		value := 0
		if transient {
			value = 1
		}
		for i := 0; i < end; i++ {
			tfRes[i] = int32(value)
		}
	}
	return tfSelect
}

// SetLFE enables or disables LFE mode constraints.
func (e *Encoder) SetLFE(enabled bool) {
	e.lfe = enabled
}

// LFE reports whether LFE mode constraints are enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
}

// LastTonality returns the most recently computed tonality estimate.
// The value ranges from 0 (noise-like spectrum) to 1 (pure tone).
// This is used by computeVBRTarget for bit allocation decisions.
func (e *Encoder) LastTonality() OpusVal16 {
	return e.lastTonality
}

// SetLastTonality sets the tonality estimate (for testing or manual override).
// Valid range is [0, 1] where 0 = noise and 1 = pure tone.
func (e *Encoder) SetLastTonality(tonality opusVal16) {
	if tonality < 0 {
		tonality = 0
	}
	if tonality > 1 {
		tonality = 1
	}
	e.lastTonality = tonality
}

// PrevBandLogEnergy returns the previous frame's band log-energies.
// Used for spectral flux computation in tonality analysis.
func (e *Encoder) PrevBandLogEnergy() []CeltGLog {
	out := make([]celtGLog, len(e.prevBandLogEnergy))
	copy(out, e.prevBandLogEnergy)
	return out
}

// GetLastDynalloc returns the last computed dynalloc result.
// This is computed during encoding and stored for the next frame's VBR decisions.
func (e *Encoder) GetLastDynalloc() DynallocResult {
	return e.lastDynalloc
}

// GetLastBandLogE returns the last frame's primary band log-energies.
// These are the bandLogE values passed to DynallocAnalysis.
func (e *Encoder) GetLastBandLogE() []CeltGLog {
	out := make([]celtGLog, len(e.lastBandLogE))
	copy(out, e.lastBandLogE)
	return out
}

// GetLastBandLogE2 returns the last frame's secondary band log-energies.
// For transients, this is from the long MDCT; otherwise same as bandLogE.
func (e *Encoder) GetLastBandLogE2() []CeltGLog {
	out := make([]celtGLog, len(e.lastBandLogE2))
	copy(out, e.lastBandLogE2)
	return out
}

// SetPhaseInversionDisabled disables stereo phase inversion.
// When true, the encoder will not use phase inversion for stereo decorrelation.
// This can improve compatibility with some audio processing chains.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.phaseInversionDisabled = disabled
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.phaseInversionDisabled
}
