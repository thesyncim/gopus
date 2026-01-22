// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides the encoder struct that mirrors the decoder state
// for synchronized prediction.

package celt

import (
	"gopus/internal/rangecoding"
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
}

// NewEncoder creates a new CELT encoder with the given number of channels.
// Valid channel counts are 1 (mono) or 2 (stereo).
// The encoder is ready to process CELT frames after creation.
//
// The initialization mirrors NewDecoder exactly (D03-01-01, D03-01-02):
// - prevEnergy initialized to -28.0 (low but finite starting energy)
// - RNG seed 22222 (matches libopus convention)
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

		// Initialize RNG with non-zero seed (D03-01-02)
		rng: 22222,

		// Analysis buffers
		inputBuffer: make([]float64, 0),
		mdctBuffer:  make([]float64, 0),
	}

	// Initialize energy arrays to reasonable defaults (D03-01-01)
	// Using negative infinity would cause issues; use small energy instead
	for i := range e.prevEnergy {
		e.prevEnergy[i] = -28.0 // Low but finite starting energy
		e.prevEnergy2[i] = -28.0
	}

	return e
}

// Reset clears encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	// Clear energy arrays
	for i := range e.prevEnergy {
		e.prevEnergy[i] = -28.0
		e.prevEnergy2[i] = -28.0
	}

	// Clear overlap buffer
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}

	// Clear pre-emphasis state
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}

	// Reset RNG
	e.rng = 22222

	// Clear range encoder reference
	e.rangeEncoder = nil

	// Clear analysis buffers
	e.inputBuffer = e.inputBuffer[:0]
	e.mdctBuffer = e.mdctBuffer[:0]

	// Reset frame counter
	e.frameCount = 0
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
func (e *Encoder) RNG() uint32 {
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
// Intra mode is used for the first frame or after a reset.
func (e *Encoder) IsIntraFrame() bool {
	return e.frameCount == 0
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
