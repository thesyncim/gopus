// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the encoder struct that mirrors the decoder state
// for synchronized prediction.

package celt

import (
	"github.com/thesyncim/gopus/rangecoding"
)

// CeltTargetStats captures per-frame VBR target diagnostics for CELT.
type CeltTargetStats struct {
	FrameSize     int
	BaseBits      int
	TargetBits    int
	Tonality      float64
	DynallocBoost int
	TFBoost       int
	PitchChange   bool
	FloorLimited  bool
	MaxDepth      float64
}

// PrefilterDebugStats captures per-frame prefilter diagnostics.
type PrefilterDebugStats struct {
	Frame          int
	Enabled        bool
	UsedTonePath   bool
	UsedPitchPath  bool
	TFEstimate     float64
	NBBytes        int
	ToneFreq       float64
	Toneishness    float64
	MaxPitchRatio  float64
	PitchSearchOut int
	PitchBeforeRD  int
	PitchAfterRD   int
	PFOn           bool
	QG             int
	Gain           float64
}

// CoarseDecisionStats captures per-band coarse energy quantization decisions.
// This is intended for diagnostics and is only emitted when a hook is installed.
type CoarseDecisionStats struct {
	Frame     int
	Band      int
	Channel   int
	Intra     bool
	LM        int
	X         float64
	Pred      float64
	Residual  float64
	QIInitial int
	QIFinal   int
	Tell      int
	BitsLeft  int
}

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
	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// Configuration (mirrors decoder)
	channels   int           // 1 or 2
	sampleRate int           // Always 48000
	lsbDepth   int           // Input LSB depth (8-24 bits)
	bandwidth  CELTBandwidth // Active bandwidth cap (NB..FB)

	// Energy state (persists across frames, mirrors decoder)
	prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []float64 // Two frames ago energies (for anti-collapse)
	energyError []float64 // Previous coarse quantization residuals [MaxBands * channels]

	// Analysis state for overlap (mirrors decoder's synthesis state)
	overlapBuffer []float64 // MDCT overlap [Overlap * channels]
	preemphState  []float64 // Pre-emphasis filter state [channels]
	// overlapMax mirrors libopus st->overlap_max for CELT silence detection.
	// It tracks max absolute amplitude over the last overlap region.
	overlapMax float64

	// RNG state (for deterministic folding decisions)
	rng uint32

	// Analysis buffers (encoder-specific)
	inputBuffer []float64 // Input sample lookahead buffer
	mdctBuffer  []float64 // MDCT output working buffer

	// Frame counting for intra mode decisions
	frameCount int // Number of frames encoded (0 = first frame uses intra mode)
	// Coarse-energy intra/inter decision state (libopus delayedIntra).
	delayedIntra float64
	forceIntra   bool
	// Consecutive transient frames (used for anti-collapse flag)
	consecTransient int

	// Allocation history for skip decisions
	lastCodedBands int // Previous coded band count (0 = uninitialized)
	intensity      int // Previous intensity stereo decision (libopus hysteresis state)

	// Bitrate control
	targetBitrate  int // Target bitrate in bits per second (0 = use buffer size)
	frameBits      int // Per-frame bit budget for coarse energy (set during encoding)
	vbr            bool
	constrainedVBR bool

	// Complexity control (0-10)
	complexity int

	// Spread decision state (persistent across frames for hysteresis)
	spreadDecision int // Current spread decision (0-3)
	tonalAverage   int // Running average for spread decision hysteresis
	hfAverage      int // High frequency average for tapset decision
	tapsetDecision int // Tapset decision (0, 1, or 2)

	// Tonality analysis state (for VBR decisions)
	prevBandLogEnergy []float64 // Previous frame log-energy per band for spectral flux
	lastTonality      float64   // Running average tonality for smoothing
	lastStereoSaving  float64   // Running stereo_saving estimate from alloc_trim analysis
	lastPitchChange   bool      // Previous frame pitch_change flag for VBR targeting

	// Analysis bandwidth state used by bit allocation gating.
	// This mirrors libopus use of st->analysis.bandwidth for clt_compute_allocation().
	analysisBandwidth int  // 1..20 bandwidth index from previous frame analysis
	analysisValid     bool // True after at least one analysis update
	analysisLeakBoost [leakBands]uint8
	// Bootstrap leak boost used when external analysis is valid but doesn't yet
	// provide leak_boost (matches early-frame libopus behavior more closely).
	analysisLeakBootstrap [leakBands]uint8

	// Dynamic allocation analysis state (for VBR decisions)
	// These are computed from the previous frame and used for current frame's VBR target.
	// Reference: libopus celt_encoder.c dynalloc_analysis()
	lastDynalloc DynallocResult

	// Debug hook for capturing per-frame CELT VBR target stats.
	targetStatsHook func(CeltTargetStats)
	// Debug hook for capturing prefilter decisions.
	prefilterDebugHook func(PrefilterDebugStats)
	// Debug hook for capturing coarse energy quantization decisions.
	coarseDecisionHook func(CoarseDecisionStats)

	// Hybrid mode flag
	// When true, postfilter flag encoding is skipped per RFC 6716 Section 3.2
	// Reference: libopus celt_encoder.c line 2047-2048
	hybrid bool

	// Pre-emphasized signal buffer for transient analysis overlap
	// Stores the previous frame's pre-emphasized samples (last Overlap samples per channel)
	// This matches libopus behavior where transient_analysis() is called with
	// N+overlap samples of pre-emphasized signal.
	// Reference: libopus celt_encoder.c line 2030
	preemphBuffer []float64

	// Force transient mode for testing/debugging
	// When true, the encoder forces short blocks for the next frame
	forceTransient bool

	// Phase inversion disabled for stereo encoding
	// When true, disables stereo phase inversion decorrelation
	phaseInversionDisabled bool

	// DC rejection filter state (high-pass filter to remove DC offset)
	// libopus applies this at the Opus encoder level before CELT processing
	// Reference: libopus src/opus_encoder.c dc_reject()
	hpMem []float64 // High-pass filter memory [channels]

	// dcRejectEnabled controls whether EncodeFrame applies dc_reject().
	// When CELT is driven by the Opus encoder, dc_reject is already applied,
	// so this should be false to avoid double filtering.
	dcRejectEnabled bool

	// delayCompensationEnabled controls whether EncodeFrame prepends the
	// Fs/250 CELT lookahead history. Standalone CELT defaults this to true;
	// top-level Opus wiring should disable it to avoid double-compensation.
	delayCompensationEnabled bool

	// Delay buffer for lookahead compensation (matches libopus delay_compensation)
	// libopus uses Fs/250 = 192 samples at 48kHz for delay compensation.
	// This provides a 4ms lookahead that allows for better transient handling.
	// Reference: libopus src/opus_encoder.c delay_compensation
	delayBuffer []float64 // Size = delayCompensation * channels

	// Prefilter (comb filter) state for postfilter signaling.
	// These mirror libopus CELT encoder fields used by run_prefilter().
	prefilterPeriod int
	prefilterGain   float64
	prefilterTapset int
	prefilterMem    []float64
	// Approximate analysis->max_pitch_ratio state for run_prefilter().
	maxPitchDownState   [3]float64
	maxPitchInmem       []float64
	maxPitchMemFill     int
	maxPitchHPEnerAccum float64
	maxPitchRatio       float64

	// Packet loss expectation (0-100) for prefilter gain scaling.
	packetLoss int

	// Debug: last frame's band energies for dynalloc analysis tracing
	lastBandLogE  []float64 // bandLogE (primary MDCT energies)
	lastBandLogE2 []float64 // bandLogE2 (secondary MDCT for transients)

	// Scratch buffers for hot path to eliminate heap allocations
	scratch encoderScratch

	// Scratch buffers for band encoding (PVQ, theta RDO, etc.)
	bandEncScratch bandEncodeScratch

	// Scratch buffers for tonality analysis (zero-alloc)
	tonalityScratch TonalityScratch

	// Scratch buffers for TF analysis (zero-alloc)
	tfScratch TFAnalysisScratch

	// Scratch buffers for dynalloc analysis (zero-alloc)
	dynallocScratch DynallocScratch

	// Transient detection state (persisted across frames for better attack detection)
	// These are used to track attack characteristics across frame boundaries.
	// Reference: libopus celt_encoder.c transient_analysis() and attack_duration tracking

	// transientHPMem stores high-pass filter memory for transient analysis.
	// Using float32 to match libopus floating-point precision.
	// The HP filter is: (1 - 2*z^-1 + z^-2) / (1 - z^-1 + 0.5*z^-2)
	// Persisting this state improves detection of attacks that span frame boundaries.
	transientHPMem [2][2]float32 // [channel][mem0, mem1]

	// attackDuration counts consecutive frames with detected transients.
	// This helps identify sustained percussive passages vs. isolated attacks.
	// A value > 1 indicates ongoing percussive activity.
	// Reference: libopus attack_duration tracking for hybrid mode decisions
	attackDuration int

	// lastMaskMetric stores the mask_metric from the previous frame.
	// Used for hysteresis to prevent rapid toggling between transient/non-transient.
	lastMaskMetric float64

	// peakEnergy tracks the maximum frame energy for adaptive thresholding.
	// This helps detect transients in both loud and quiet passages.
	peakEnergy float64
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

	e := &Encoder{
		channels:   channels,
		sampleRate: 48000, // CELT always operates at 48kHz internally
		lsbDepth:   24,    // Default to full 24-bit depth
		bandwidth:  CELTFullband,

		// Allocate energy arrays for all bands and channels
		prevEnergy:  make([]float64, MaxBands*channels),
		prevEnergy2: make([]float64, MaxBands*channels),
		energyError: make([]float64, MaxBands*channels),

		// Overlap buffer for MDCT overlap-add analysis
		// Size is Overlap (120) samples per channel
		overlapBuffer: make([]float64, Overlap*channels),

		// Pre-emphasis filter state, one per channel
		preemphState: make([]float64, channels),

		// Initialize RNG to zero (libopus default)
		rng: 0,

		// Analysis buffers
		inputBuffer: make([]float64, 0),
		mdctBuffer:  make([]float64, 0),

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
		prevBandLogEnergy: make([]float64, MaxBands*channels),
		lastTonality:      0.5, // Start with neutral tonality estimate
		lastStereoSaving:  0.0,
		lastPitchChange:   false,
		analysisBandwidth: 20,
		analysisValid:     false,

		// Pre-emphasized signal buffer for transient analysis overlap
		// Size is Overlap samples per channel (interleaved for stereo)
		preemphBuffer: make([]float64, Overlap*channels),

		// DC rejection (high-pass) filter memory, one per channel
		hpMem: make([]float64, channels),

		// Apply dc_reject by default for standalone CELT usage
		dcRejectEnabled: true,

		// Standalone CELT defaults to Opus-style delay compensation.
		// Top-level Opus integration should disable this to avoid double-applying.
		delayCompensationEnabled: true,

		// Delay buffer for lookahead (192 samples at 48kHz = 4ms)
		// This matches libopus delay_compensation
		delayBuffer: make([]float64, DelayCompensation*channels),

		// Prefilter state (comb filter history) for postfilter signaling.
		// libopus zero-initializes this state on reset.
		prefilterPeriod: 0,
		prefilterGain:   0,
		prefilterTapset: 0,
		prefilterMem:    make([]float64, combFilterMaxPeriod*channels),
		// analysis starts with 10 ms history and max_pitch_ratio defaults to 1.
		maxPitchInmem:   make([]float64, 720),
		maxPitchMemFill: 240,
		// Match libopus startup behavior: analysis info starts invalid, so
		// max_pitch_ratio defaults to 0 until analysis becomes available.
		maxPitchRatio: 0.0,

		// Default to VBR enabled to mirror libopus behavior.
		vbr: true,
	}

	// Energy arrays default to zero after allocation (matches libopus init).

	return e
}

// SetPrefilterDebugHook installs a callback that receives per-frame prefilter stats.
func (e *Encoder) SetPrefilterDebugHook(fn func(PrefilterDebugStats)) {
	e.prefilterDebugHook = fn
}

// SetCoarseDecisionHook installs a callback that receives per-band coarse
// quantization decisions during EncodeCoarseEnergy.
func (e *Encoder) SetCoarseDecisionHook(fn func(CoarseDecisionStats)) {
	e.coarseDecisionHook = fn
}

// SetTargetStatsHook installs a callback that receives per-frame CELT VBR targets.
func (e *Encoder) SetTargetStatsHook(fn func(CeltTargetStats)) {
	e.targetStatsHook = fn
}

// SetAnalysisBandwidth provides the analysis-derived bandwidth index (1..20)
// used by allocation gating in clt_compute_allocation().
func (e *Encoder) SetAnalysisBandwidth(bandwidth int, valid bool) {
	if !valid {
		e.analysisValid = false
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
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
}

// SetAnalysisInfo provides analysis-derived state from the top-level Opus analysis
// pipeline. This mirrors libopus use of AnalysisInfo in CELT dynalloc.
func (e *Encoder) SetAnalysisInfo(bandwidth int, leakBoost [leakBands]uint8, valid bool) {
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
	e.analysisLeakBoost = leakBoost
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
	for i := 0; i < leakBands; i++ {
		if leak[i] != 0 {
			return leak
		}
	}
	// Bootstrap early valid-analysis frames when leak_boost is unavailable.
	// Frame count is pre-increment at this point in EncodeFrame.
	if e.frameCount <= 2 {
		for i := range e.analysisLeakBootstrap {
			e.analysisLeakBootstrap[i] = 0
		}
		e.analysisLeakBootstrap[2] = 64 // +1.0 at band 2 (Q6)
		return e.analysisLeakBootstrap[:]
	}
	return leak
}

func (e *Encoder) emitTargetStats(stats CeltTargetStats, baseBits, targetBits int) {
	if e.targetStatsHook == nil {
		return
	}
	stats.BaseBits = baseBits
	stats.TargetBits = targetBits
	e.targetStatsHook(stats)
}

// Reset clears encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	// Clear energy arrays (match libopus reset: oldBandE=0).
	for i := range e.prevEnergy {
		e.prevEnergy[i] = 0
		e.prevEnergy2[i] = 0
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
	e.maxPitchDownState = [3]float64{}
	for i := range e.maxPitchInmem {
		e.maxPitchInmem[i] = 0
	}
	e.maxPitchMemFill = 240
	e.maxPitchHPEnerAccum = 0
	// Match libopus reset behavior (analysis invalid -> max_pitch_ratio = 0).
	e.maxPitchRatio = 0.0

	// Clear pre-emphasis state
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}

	// Reset RNG to zero (libopus default)
	e.rng = 0

	// Clear range encoder reference
	e.rangeEncoder = nil

	// Clear analysis buffers
	e.inputBuffer = e.inputBuffer[:0]
	e.mdctBuffer = e.mdctBuffer[:0]

	// Reset frame counter
	e.frameCount = 0
	e.frameBits = 0
	e.delayedIntra = 1.0
	e.lastCodedBands = 0
	e.intensity = 0
	e.consecTransient = 0

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
	e.lastTonality = 0.5
	e.lastStereoSaving = 0
	e.lastPitchChange = false
	e.analysisBandwidth = 20
	e.analysisValid = false
	for i := range e.analysisLeakBoost {
		e.analysisLeakBoost[i] = 0
	}
	for i := range e.analysisLeakBootstrap {
		e.analysisLeakBootstrap[i] = 0
	}

	// Clear pre-emphasis buffer for transient analysis
	for i := range e.preemphBuffer {
		e.preemphBuffer[i] = 0
	}

	// Clear DC rejection filter state
	for i := range e.hpMem {
		e.hpMem[i] = 0
	}

	// Clear delay buffer
	for i := range e.delayBuffer {
		e.delayBuffer[i] = 0
	}

	// Reset transient detection state
	for c := 0; c < 2; c++ {
		e.transientHPMem[c][0] = 0
		e.transientHPMem[c][1] = 0
	}
	e.attackDuration = 0
	e.lastMaskMetric = 0
	e.peakEnergy = 0
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
	e.complexity = complexity
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

// ConstrainedVBR reports whether constrained VBR mode is enabled.
func (e *Encoder) ConstrainedVBR() bool {
	return e.constrainedVBR
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

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return e.complexity
}

// SetRangeEncoder sets the range encoder for the current frame.
// This must be called before encoding each frame.
func (e *Encoder) SetRangeEncoder(re *rangecoding.Encoder) {
	e.rangeEncoder = re
}

// RangeEncoder returns the current range encoder.
func (e *Encoder) RangeEncoder() *rangecoding.Encoder {
	return e.rangeEncoder
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return e.channels
}

// SampleRate returns the operating sample rate (always 48000 for CELT).
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// PrevEnergy returns the previous frame's band energies.
// Used for inter-frame energy prediction in coarse energy encoding.
// Layout: [band0_ch0, band1_ch0, ..., band20_ch0, band0_ch1, ..., band20_ch1]
func (e *Encoder) PrevEnergy() []float64 {
	return e.prevEnergy
}

// PrevEnergy2 returns the band energies from two frames ago.
// Used for anti-collapse detection.
func (e *Encoder) PrevEnergy2() []float64 {
	return e.prevEnergy2
}

// SetPrevEnergy shifts current prev to prev2 and sets new prev energies.
// This should be called after encoding a frame with the actual energies used.
func (e *Encoder) SetPrevEnergy(energies []float64) {
	// Shift: current prev becomes prev2
	copy(e.prevEnergy2, e.prevEnergy)
	// Copy new energies to prev
	copy(e.prevEnergy, energies)
}

// SetPrevEnergyWithPrev updates prevEnergy using the provided previous state.
// This avoids losing the prior frame when prevEnergy is updated during encoding.
func (e *Encoder) SetPrevEnergyWithPrev(prev, energies []float64) {
	if len(prev) == len(e.prevEnergy2) {
		copy(e.prevEnergy2, prev)
	} else {
		copy(e.prevEnergy2, e.prevEnergy)
	}
	copy(e.prevEnergy, energies)
}

// OverlapBuffer returns the overlap buffer for MDCT analysis.
// Size is Overlap * channels samples.
func (e *Encoder) OverlapBuffer() []float64 {
	return e.overlapBuffer
}

// SetOverlapBuffer copies the given samples to the overlap buffer.
func (e *Encoder) SetOverlapBuffer(samples []float64) {
	copy(e.overlapBuffer, samples)
}

// PreemphState returns the pre-emphasis filter state.
// One value per channel.
func (e *Encoder) PreemphState() []float64 {
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
func (e *Encoder) GetEnergy(band, channel int) float64 {
	if band < 0 || band >= MaxBands || channel < 0 || channel >= e.channels {
		return 0
	}
	return e.prevEnergy[channel*MaxBands+band]
}

// SetEnergy sets the energy for a specific band and channel.
func (e *Encoder) SetEnergy(band, channel int, energy float64) {
	if band < 0 || band >= MaxBands || channel < 0 || channel >= e.channels {
		return
	}
	e.prevEnergy[channel*MaxBands+band] = energy
}

// IsIntraFrame returns true if this frame should use intra mode.
//
// This matches libopus two-pass behavior for complexity >= 4:
// - libopus uses force_intra=0 by default
// - With two_pass=1 (complexity >= 4), intra starts as force_intra (=0)
// - Then two-pass encoding compares intra vs inter and picks the better one
//
// For simplicity, we match the libopus default: always return false (inter mode)
// even for frame 0, because libopus's two-pass typically chooses inter mode
// for the first frame when encoding simple signals (like sine waves).
//
// Reference: libopus celt/quant_bands.c line 279:
//
//	intra = force_intra || (!two_pass && *delayedIntra>2*C*(end-start) && ...)
//
// With two_pass=1 and force_intra=0, this evaluates to intra=0.
func (e *Encoder) IsIntraFrame() bool {
	// Match libopus two-pass behavior: never force intra
	// The two-pass algorithm in libopus dynamically decides, but with
	// complexity >= 4 and force_intra=0, the initial intra value is 0.
	// For most signals, the two-pass comparison also chooses inter mode.
	return false
}

// IncrementFrameCount increments the frame counter.
// Call this after successfully encoding a frame.
func (e *Encoder) IncrementFrameCount() {
	e.frameCount++
}

// FrameCount returns the number of frames encoded.
func (e *Encoder) FrameCount() int {
	return e.frameCount
}

// SetBitrate sets the target bitrate in bits per second.
// This affects bit allocation for frame encoding.
func (e *Encoder) SetBitrate(bps int) {
	e.targetBitrate = bps
}

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return e.targetBitrate
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
	e.packetLoss = lossPercent
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return e.packetLoss
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
	e.lsbDepth = depth
}

// LSBDepth returns the current input signal LSB depth.
func (e *Encoder) LSBDepth() int {
	if e.lsbDepth <= 0 {
		return 24
	}
	return e.lsbDepth
}

// SetBandwidth sets the CELT bandwidth cap used for band allocation.
func (e *Encoder) SetBandwidth(bw CELTBandwidth) {
	if bw < CELTNarrowband || bw > CELTFullband {
		bw = CELTFullband
	}
	e.bandwidth = bw
}

// Bandwidth returns the active CELT bandwidth cap.
func (e *Encoder) Bandwidth() CELTBandwidth {
	return e.bandwidth
}

func (e *Encoder) effectiveBandCount(frameSize int) int {
	nbBands := GetModeConfig(frameSize).EffBands
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
	return e.tapsetDecision
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
	e.tapsetDecision = tapset
}

// HFAverage returns the high-frequency average used for tapset decision.
// This is updated during SpreadingDecision when updateHF=true.
func (e *Encoder) HFAverage() int {
	return e.hfAverage
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

// SetForceTransient forces short blocks for testing/debugging.
// When true, the encoder uses short blocks (transient mode) for the next frame
// regardless of transient analysis result.
func (e *Encoder) SetForceTransient(force bool) {
	e.forceTransient = force
}

// LastTonality returns the most recently computed tonality estimate.
// The value ranges from 0 (noise-like spectrum) to 1 (pure tone).
// This is used by computeVBRTarget for bit allocation decisions.
func (e *Encoder) LastTonality() float64 {
	return e.lastTonality
}

// SetLastTonality sets the tonality estimate (for testing or manual override).
// Valid range is [0, 1] where 0 = noise and 1 = pure tone.
func (e *Encoder) SetLastTonality(tonality float64) {
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
func (e *Encoder) PrevBandLogEnergy() []float64 {
	return e.prevBandLogEnergy
}

// GetLastDynalloc returns the last computed dynalloc result.
// This is computed during encoding and stored for the next frame's VBR decisions.
func (e *Encoder) GetLastDynalloc() DynallocResult {
	return e.lastDynalloc
}

// GetLastBandLogE returns the last frame's primary band log-energies.
// These are the bandLogE values passed to DynallocAnalysis.
func (e *Encoder) GetLastBandLogE() []float64 {
	return e.lastBandLogE
}

// GetLastBandLogE2 returns the last frame's secondary band log-energies.
// For transients, this is from the long MDCT; otherwise same as bandLogE.
func (e *Encoder) GetLastBandLogE2() []float64 {
	return e.lastBandLogE2
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

// encoderScratch holds pre-allocated scratch buffers for the encoder hot path.
// These buffers are reused across frames to eliminate heap allocations during encoding.
type encoderScratch struct {
	// LSB-depth quantized input buffer
	quantizedInput []float64

	// DC rejection output buffer
	dcRejected []float64

	// Combined delay buffer + PCM
	combinedBuf []float64

	// Pre-emphasized signal buffer
	preemph []float64

	// Transient analysis input buffer (overlap + frame)
	transientInput []float64

	// Prefilter (comb filter) scratch buffers
	prefilterPre      []float64
	prefilterOut      []float64
	prefilterPitchBuf []float64
	pitchHPPrefix     []float64
	prefilterXLP4     []float64
	prefilterYLP4     []float64
	prefilterXcorr    []float64
	prefilterYYLookup []float64

	// MDCT coefficient buffers
	mdctCoeffs []float64
	mdctLeft   []float64
	mdctRight  []float64

	// Band energy buffers
	energies  []float64
	bandLogE2 []float64
	bandE     []float64
	bandEL    []float64
	bandER    []float64

	// History buffers for MDCT
	leftHist  []float64
	rightHist []float64

	// Range encoder buffer
	reBuf []byte

	// Quantized energies
	quantizedEnergies []float64
	coarseError       []float64
	analysisEnergies  []float64
	prev1LogE         []float64

	// Normalized coefficient buffers
	normL []float64
	normR []float64
	// Interleaved stereo normalized coefficients for spread analysis.
	normStereo []float64

	// Allocation-related buffers
	caps    []int
	offsets []int
	logN    []int16

	// TF analysis buffers
	tfRes []int

	// PVQ search buffers
	pvqSignx []int
	pvqY     []float32
	pvqAbsX  []float32
	pvqIy    []int

	// Deinterleave buffers
	deintLeft  []float64
	deintRight []float64

	// MDCT forward transform scratch (float32)
	mdctF           []float32
	mdctFFTIn       []complex64
	mdctFFTOut      []complex64
	mdctFFTTmp      []kissCpx
	mdctBlockCoeffs []float64 // Per-block coefficients for short MDCT

	// Transient analysis scratch
	transientTmp          []float64
	transientEnergy       []float64
	transientChannelSamps []float64
	transientX            []float32

	// Tonality analysis scratch
	tonalityPowers       []float64
	tonalityBandPowers   []float64
	tonalityBandTonality []float64

	// CWRS encoding scratch
	cwrsU []uint32

	// TF analysis scratch
	tfMetric []int     // Per-band metric (size: nbEBands)
	tfTmp    []float64 // Band coefficients (size: max band width)
	tfTmp1   []float64 // Copy for transient analysis (size: max band width)
	tfPath0  []int     // Viterbi path state 0 (size: nbEBands)
	tfPath1  []int     // Viterbi path state 1 (size: nbEBands)

	// Dynalloc analysis scratch
	dynallocFollower   []float64
	dynallocNoise      []float64
	dynallocImportance []int

	// ComputeAllocation scratch
	allocBits     []int
	allocFineBits []int
	allocFinePrio []int
	allocThresh   []int
	allocTrim     []int
	allocCaps     []int
	allocResult   AllocationResult // Pre-allocated result struct

	// MDCT input buffer for ComputeMDCTWithHistory
	mdctInput []float64

	// Pitch ratio FFT scratch buffers
	pitchFFTIn  []complex64 // size: fft N (e.g. 480)
	pitchFFTOut []complex64 // size: fft N
	pitchFFTTmp []kissCpx   // FFT workspace, size: fft N
	pitchDown   []float64   // downsampled signal, size: fft N

	// Band encode scratch (for quantAllBandsEncode)
	bandEncode bandEncodeScratch

	// Range encoder (reused between frames)
	rangeEncoder rangecoding.Encoder

	// Coarse-energy two-pass scratch
	coarseStartState rangecoding.EncoderState
	coarseOldStart   []float64
}

// EnsureScratch ensures all scratch buffers are properly sized for the given frame size.
// Call this before using the encoder's scratch-aware methods from an external path
// (e.g., hybrid encoding) that does not go through EncodeFrame.
func (e *Encoder) EnsureScratch(frameSize int) {
	e.ensureScratch(frameSize)
}

// ensureScratch ensures all scratch buffers are properly sized for the given frame parameters.
// Call this at the start of EncodeFrame to prepare buffers for reuse.
func (e *Encoder) ensureScratch(frameSize int) {
	channels := e.channels
	expectedLen := frameSize * channels
	overlap := Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	s := &e.scratch

	// DC rejection output
	s.quantizedInput = ensureFloat64Slice(&s.quantizedInput, expectedLen)
	s.dcRejected = ensureFloat64Slice(&s.dcRejected, expectedLen)

	// Combined delay buffer
	delayComp := DelayCompensation * channels
	combinedLen := delayComp + expectedLen
	s.combinedBuf = ensureFloat64Slice(&s.combinedBuf, combinedLen)

	// Pre-emphasis buffer
	s.preemph = ensureFloat64Slice(&s.preemph, expectedLen)

	// Transient analysis input (overlap + frameSize) * channels
	transientLen := (overlap + frameSize) * channels
	s.transientInput = ensureFloat64Slice(&s.transientInput, transientLen)

	// Prefilter scratch buffers
	maxPeriod := combFilterMaxPeriod
	if maxPeriod < combFilterMinPeriod {
		maxPeriod = combFilterMinPeriod
	}
	prefilterLen := (maxPeriod + frameSize) * channels
	s.prefilterPre = ensureFloat64Slice(&s.prefilterPre, prefilterLen)
	s.prefilterOut = ensureFloat64Slice(&s.prefilterOut, prefilterLen)
	pitchBufLen := (maxPeriod + frameSize) >> 1
	if pitchBufLen < 1 {
		pitchBufLen = 1
	}
	s.prefilterPitchBuf = ensureFloat64Slice(&s.prefilterPitchBuf, pitchBufLen)
	maxPitch := maxPeriod - 3*combFilterMinPeriod
	if maxPitch < 1 {
		maxPitch = 1
	}
	s.prefilterXcorr = ensureFloat64Slice(&s.prefilterXcorr, maxPitch>>1)
	xlp4Len := frameSize >> 2
	if xlp4Len < 1 {
		xlp4Len = 1
	}
	s.prefilterXLP4 = ensureFloat64Slice(&s.prefilterXLP4, xlp4Len)
	lag := frameSize + maxPitch
	ylp4Len := lag >> 2
	if ylp4Len < 1 {
		ylp4Len = 1
	}
	s.prefilterYLP4 = ensureFloat64Slice(&s.prefilterYLP4, ylp4Len)
	yyLookupLen := (maxPeriod >> 1) + 1
	if yyLookupLen < 1 {
		yyLookupLen = 1
	}
	s.prefilterYYLookup = ensureFloat64Slice(&s.prefilterYYLookup, yyLookupLen)

	// MDCT coefficients
	s.mdctCoeffs = ensureFloat64Slice(&s.mdctCoeffs, frameSize*2)
	s.mdctLeft = ensureFloat64Slice(&s.mdctLeft, frameSize)
	s.mdctRight = ensureFloat64Slice(&s.mdctRight, frameSize)

	// Band energies
	bandCount := MaxBands * channels
	s.energies = ensureFloat64Slice(&s.energies, bandCount)
	s.bandLogE2 = ensureFloat64Slice(&s.bandLogE2, bandCount)
	s.bandE = ensureFloat64Slice(&s.bandE, bandCount)
	s.coarseError = ensureFloat64Slice(&s.coarseError, bandCount)
	s.bandEL = ensureFloat64Slice(&s.bandEL, MaxBands)
	s.bandER = ensureFloat64Slice(&s.bandER, MaxBands)

	// History buffers
	s.leftHist = ensureFloat64Slice(&s.leftHist, overlap)
	s.rightHist = ensureFloat64Slice(&s.rightHist, overlap)

	// Range encoder buffer
	bufSize := 256
	if len(s.reBuf) < bufSize {
		s.reBuf = make([]byte, bufSize)
	}

	// Quantized energies
	s.quantizedEnergies = ensureFloat64Slice(&s.quantizedEnergies, bandCount)
	s.prev1LogE = ensureFloat64Slice(&s.prev1LogE, bandCount)

	// Normalized coefficients
	s.normL = ensureFloat64Slice(&s.normL, frameSize)
	s.normR = ensureFloat64Slice(&s.normR, frameSize)
	s.normStereo = ensureFloat64Slice(&s.normStereo, frameSize*2)

	// Allocation buffers
	s.caps = ensureIntSlice(&s.caps, MaxBands)
	s.offsets = ensureIntSlice(&s.offsets, MaxBands)
	if len(s.logN) < MaxBands {
		s.logN = make([]int16, MaxBands)
	}
	s.allocBits = ensureIntSlice(&s.allocBits, MaxBands)
	s.allocFineBits = ensureIntSlice(&s.allocFineBits, MaxBands)
	s.allocFinePrio = ensureIntSlice(&s.allocFinePrio, MaxBands)
	s.allocCaps = ensureIntSlice(&s.allocCaps, MaxBands)
	// Initialize AllocationResult with pre-allocated slices
	s.allocResult.BandBits = s.allocBits
	s.allocResult.FineBits = s.allocFineBits
	s.allocResult.FinePriority = s.allocFinePrio
	s.allocResult.Caps = s.allocCaps

	// TF results
	s.tfRes = ensureIntSlice(&s.tfRes, MaxBands)

	// Deinterleave buffers
	s.deintLeft = ensureFloat64Slice(&s.deintLeft, frameSize)
	s.deintRight = ensureFloat64Slice(&s.deintRight, frameSize)

	// MDCT forward transform scratch (float32)
	n4 := frameSize / 2 // n4 = frameSize/2 for N=2*frameSize MDCT
	s.mdctF = ensureFloat32Slice(&s.mdctF, frameSize)
	s.mdctFFTIn = ensureComplex64Slice(&s.mdctFFTIn, n4)
	s.mdctFFTOut = ensureComplex64Slice(&s.mdctFFTOut, n4)
	s.mdctFFTTmp = ensureKissCpxSlice(&s.mdctFFTTmp, n4)
	// For short MDCT: max short size is frameSize/8 (for 8 short blocks)
	s.mdctBlockCoeffs = ensureFloat64Slice(&s.mdctBlockCoeffs, frameSize/2)

	// Transient analysis scratch
	samplesPerChannel := frameSize + overlap
	s.transientTmp = ensureFloat64Slice(&s.transientTmp, samplesPerChannel)
	s.transientEnergy = ensureFloat64Slice(&s.transientEnergy, samplesPerChannel/2)
	s.transientChannelSamps = ensureFloat64Slice(&s.transientChannelSamps, samplesPerChannel)
	s.transientX = ensureFloat32Slice(&s.transientX, samplesPerChannel)

	// Tonality analysis scratch
	s.tonalityPowers = ensureFloat64Slice(&s.tonalityPowers, frameSize)
	s.tonalityBandPowers = ensureFloat64Slice(&s.tonalityBandPowers, maxBandWidth)
	s.tonalityBandTonality = ensureFloat64Slice(&s.tonalityBandTonality, MaxBands)

	// CWRS encoding scratch (k can be up to ~128 for typical encoding)
	s.cwrsU = ensureUint32Slice(&s.cwrsU, 256)

	// TF analysis scratch
	s.tfMetric = ensureIntSlice(&s.tfMetric, MaxBands)
	// Max band width is ~176 bins (band 20 at LM=3), but we need 2x for safety
	const maxTFBandWidth = 384
	s.tfTmp = ensureFloat64Slice(&s.tfTmp, maxTFBandWidth)
	s.tfTmp1 = ensureFloat64Slice(&s.tfTmp1, maxTFBandWidth)
	s.tfPath0 = ensureIntSlice(&s.tfPath0, MaxBands)
	s.tfPath1 = ensureIntSlice(&s.tfPath1, MaxBands)

	// Dynalloc analysis scratch
	s.dynallocFollower = ensureFloat64Slice(&s.dynallocFollower, MaxBands)
	s.dynallocNoise = ensureFloat64Slice(&s.dynallocNoise, MaxBands)
	s.dynallocImportance = ensureIntSlice(&s.dynallocImportance, MaxBands)

	// ComputeAllocation scratch
	s.allocBits = ensureIntSlice(&s.allocBits, MaxBands)
	s.allocFineBits = ensureIntSlice(&s.allocFineBits, MaxBands)
	s.allocFinePrio = ensureIntSlice(&s.allocFinePrio, MaxBands)
	s.allocThresh = ensureIntSlice(&s.allocThresh, MaxBands)
	s.allocTrim = ensureIntSlice(&s.allocTrim, MaxBands)

	// MDCT input buffer for ComputeMDCTWithHistory
	s.mdctInput = ensureFloat64Slice(&s.mdctInput, frameSize+overlap)

	// PVQ search buffers
	maxPVQN := maxBandWidth * 2 // Max band width with stereo doubling
	s.pvqSignx = ensureIntSlice(&s.pvqSignx, maxPVQN)
	s.pvqY = ensureFloat32Slice(&s.pvqY, maxPVQN)
	s.pvqAbsX = ensureFloat32Slice(&s.pvqAbsX, maxPVQN)
	s.pvqIy = ensureIntSlice(&s.pvqIy, maxPVQN)

	// Band encode scratch
	s.bandEncode.collapse = ensureByteSlice(&s.bandEncode.collapse, channels*MaxBands)
	normLen := 8 * EBands[MaxBands-1] // M=8 for 20ms frames
	s.bandEncode.norm = ensureFloat64Slice(&s.bandEncode.norm, channels*normLen)
	maxBand := 8 * (EBands[MaxBands] - EBands[MaxBands-1])
	s.bandEncode.lowbandScratch = ensureFloat64Slice(&s.bandEncode.lowbandScratch, maxBand)
	s.bandEncode.xSave = ensureFloat64Slice(&s.bandEncode.xSave, maxBandWidth)
	s.bandEncode.ySave = ensureFloat64Slice(&s.bandEncode.ySave, maxBandWidth)
	s.bandEncode.normSave = ensureFloat64Slice(&s.bandEncode.normSave, maxBandWidth)
	s.bandEncode.xResult0 = ensureFloat64Slice(&s.bandEncode.xResult0, maxBandWidth)
	s.bandEncode.yResult0 = ensureFloat64Slice(&s.bandEncode.yResult0, maxBandWidth)
	s.bandEncode.normResult0 = ensureFloat64Slice(&s.bandEncode.normResult0, maxBandWidth)
	s.bandEncode.pvqSignx = ensureIntSlice(&s.bandEncode.pvqSignx, maxPVQN)
	s.bandEncode.pvqY = ensureFloat32Slice(&s.bandEncode.pvqY, maxPVQN)
	s.bandEncode.pvqAbsX = ensureFloat32Slice(&s.bandEncode.pvqAbsX, maxPVQN)
	s.bandEncode.pvqIy = ensureIntSlice(&s.bandEncode.pvqIy, maxPVQN)
	s.bandEncode.cwrsU = ensureUint32Slice(&s.bandEncode.cwrsU, 256)
	s.bandEncode.hadamardTmp = ensureFloat64Slice(&s.bandEncode.hadamardTmp, maxBandWidth*16) // For stride up to 16
}

// computeAllocationScratch computes bit allocation using scratch buffers (zero-alloc).
// This is the zero-allocation version of ComputeAllocationWithEncoder.
func (e *Encoder) computeAllocationScratch(re *rangecoding.Encoder, totalBitsQ3, nbBands int, cap, offsets []int, trim int, intensity int, dualStereo bool, lm int, prev int, signalBandwidth int) *AllocationResult {
	if nbBands > MaxBands {
		nbBands = MaxBands
	}
	if nbBands < 0 {
		nbBands = 0
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if lm < 0 {
		lm = 0
	}
	if lm > 3 {
		lm = 3
	}

	// Use pre-allocated result from scratch
	result := &e.scratch.allocResult
	result.BandBits = e.scratch.allocBits[:nbBands]
	result.FineBits = e.scratch.allocFineBits[:nbBands]
	result.FinePriority = e.scratch.allocFinePrio[:nbBands]
	result.Caps = e.scratch.allocCaps[:nbBands]
	result.Balance = 0
	result.CodedBands = nbBands
	result.Intensity = 0
	result.DualStereo = false

	// Zero the slices
	for i := 0; i < nbBands; i++ {
		result.BandBits[i] = 0
		result.FineBits[i] = 0
		result.FinePriority[i] = 0
		result.Caps[i] = 0
	}

	if nbBands == 0 || totalBitsQ3 <= 0 {
		return result
	}

	if cap == nil || len(cap) < nbBands {
		cap = initCaps(nbBands, lm, channels)
	}
	copy(result.Caps, cap[:nbBands])

	if offsets == nil {
		offsets = e.scratch.offsets[:nbBands]
		for i := range offsets {
			offsets[i] = 0
		}
	}

	intensityVal := intensity
	dualVal := 0
	if dualStereo {
		dualVal = 1
	}
	balance := 0
	pulses := result.BandBits
	fineBits := result.FineBits
	finePriority := result.FinePriority

	codedBands := cltComputeAllocationEncode(re, 0, nbBands, offsets, cap, trim, &intensityVal, &dualVal,
		totalBitsQ3, &balance, pulses, fineBits, finePriority, channels, lm, prev, signalBandwidth)

	result.CodedBands = codedBands
	result.Balance = balance
	result.Intensity = intensityVal
	result.DualStereo = dualVal != 0

	return result
}
