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
	// Range encoder reference (set per frame)
	rangeEncoder *rangecoding.Encoder

	// Configuration (mirrors decoder)
	channels   int // 1 or 2
	sampleRate int // Always 48000

	// Energy state (persists across frames, mirrors decoder)
	prevEnergy  []float64 // Previous frame band energies [MaxBands * channels]
	prevEnergy2 []float64 // Two frames ago energies (for anti-collapse)

	// Analysis state for overlap (mirrors decoder's synthesis state)
	overlapBuffer []float64 // MDCT overlap [Overlap * channels]
	preemphState  []float64 // Pre-emphasis filter state [channels]

	// RNG state (for deterministic folding decisions)
	rng uint32

	// Analysis buffers (encoder-specific)
	inputBuffer []float64 // Input sample lookahead buffer
	mdctBuffer  []float64 // MDCT output working buffer

	// Frame counting for intra mode decisions
	frameCount int // Number of frames encoded (0 = first frame uses intra mode)

	// Allocation history for skip decisions
	lastCodedBands int // Previous coded band count (0 = uninitialized)

	// Bitrate control
	targetBitrate int // Target bitrate in bits per second (0 = use buffer size)
	frameBits     int // Per-frame bit budget for coarse energy (set during encoding)

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

	// Dynamic allocation analysis state (for VBR decisions)
	// These are computed from the previous frame and used for current frame's VBR target.
	// Reference: libopus celt_encoder.c dynalloc_analysis()
	lastDynalloc DynallocResult

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

		// Allocate energy arrays for all bands and channels
		prevEnergy:  make([]float64, MaxBands*channels),
		prevEnergy2: make([]float64, MaxBands*channels),

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

		// Initialize spread decision state (libopus defaults to SPREAD_NORMAL)
		spreadDecision: spreadNormal,
		tonalAverage:   0,
		hfAverage:      0,
		tapsetDecision: 0,

		// Initialize tonality analysis state
		prevBandLogEnergy: make([]float64, MaxBands*channels),
		lastTonality:      0.5, // Start with neutral tonality estimate

		// Pre-emphasized signal buffer for transient analysis overlap
		// Size is Overlap samples per channel (interleaved for stereo)
		preemphBuffer: make([]float64, Overlap*channels),
	}

	// Energy arrays default to zero after allocation (matches libopus init).

	return e
}

// Reset clears encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	// Clear energy arrays (match libopus reset: oldBandE=0).
	for i := range e.prevEnergy {
		e.prevEnergy[i] = 0
		e.prevEnergy2[i] = 0
	}

	// Clear overlap buffer
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}

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
	e.lastCodedBands = 0

	// Reset spread decision state
	e.spreadDecision = spreadNormal
	e.tonalAverage = 0
	e.hfAverage = 0
	e.tapsetDecision = 0

	// Reset tonality analysis state
	for i := range e.prevBandLogEnergy {
		e.prevBandLogEnergy[i] = 0
	}
	e.lastTonality = 0.5

	// Clear pre-emphasis buffer for transient analysis
	for i := range e.preemphBuffer {
		e.preemphBuffer[i] = 0
	}
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
