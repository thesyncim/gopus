// Package plc implements Packet Loss Concealment (PLC) for Opus.
// PLC generates plausible audio when packets are lost, preventing jarring
// silence or glitches. This is essential for real-time audio applications
// over unreliable networks.
//
// Reference: RFC 6716 Section 4.2.8 (PLC), libopus silk/dec_API.c
package plc

// Mode indicates which Opus mode to use for concealment.
type Mode int

const (
	// ModeSILK indicates SILK-only concealment.
	// Uses LPC extrapolation and pitch prediction for speech.
	ModeSILK Mode = iota

	// ModeCELT indicates CELT-only concealment.
	// Uses energy decay with noise fill for music/general audio.
	ModeCELT

	// ModeHybrid indicates combined SILK+CELT concealment.
	// Coordinates both layers for hybrid mode frames.
	ModeHybrid
)

// MaxConcealedFrames is the maximum consecutive frames to conceal
// before fading to silence. ~100ms at 20ms frames = 5 frames.
// After this many frames, the output should be near-silent.
const MaxConcealedFrames = 5

// FadePerFrame is the gain reduction per lost frame (linear).
// Approximately -6dB per frame: 10^(-6/20) ~ 0.5
// This provides smooth fade-out during extended packet loss.
const FadePerFrame = 0.5

// State tracks PLC state across frames.
// It maintains information about consecutive losses and coordinates
// the fade-out behavior for concealment.
type State struct {
	// lostCount tracks consecutive lost packets.
	// Reset to 0 when a good packet is received.
	lostCount int

	// mode indicates which concealment algorithm to use.
	// Set from the mode of the last successfully decoded packet.
	mode Mode

	// fadeFactor is the current gain multiplier (1.0 = full volume).
	// Decays toward 0 with each consecutive loss.
	fadeFactor float64

	// lastFrameSize stores the frame size from the last good packet.
	// Used to generate concealment of the same duration.
	lastFrameSize int

	// lastChannels stores the channel count from the last good packet.
	lastChannels int
}

// NewState creates a new PLC state with initial values.
// The state starts with full gain (fadeFactor = 1.0) and
// zero lost packet count.
func NewState() *State {
	return &State{
		lostCount:     0,
		mode:          ModeSILK, // Default; will be set by actual decoding
		fadeFactor:    1.0,
		lastFrameSize: 960, // Default 20ms at 48kHz
		lastChannels:  1,
	}
}

// Reset clears PLC state after receiving a good packet.
// This should be called whenever a packet is successfully decoded.
// It resets the lost count and restores full gain.
func (s *State) Reset() {
	s.lostCount = 0
	s.fadeFactor = 1.0
}

// RecordLoss records a lost packet and returns the current fade factor.
// Call this before generating concealment audio to get the correct gain.
//
// The fade factor decays exponentially:
//   - First loss: fadeFactor = 1.0 * FadePerFrame = 0.5
//   - Second loss: fadeFactor = 0.5 * FadePerFrame = 0.25
//   - After MaxConcealedFrames: fadeFactor approaches 0
//
// Returns: current fade factor to apply to concealment audio
func (s *State) RecordLoss() float64 {
	s.lostCount++

	// Apply fade: multiply by FadePerFrame each lost packet
	s.fadeFactor *= FadePerFrame

	// Clamp to minimum (effectively zero)
	if s.fadeFactor < 0.001 {
		s.fadeFactor = 0.0
	}

	return s.fadeFactor
}

// LostCount returns the number of consecutive lost packets.
// This can be used to determine if we're in extended loss condition.
func (s *State) LostCount() int {
	return s.lostCount
}

// FadeFactor returns the current fade level (0.0 to 1.0).
// This is the gain to apply to concealment audio.
//   - 1.0: Full volume (no loss yet recorded)
//   - 0.5: One packet lost
//   - 0.0: After several consecutive losses (silent)
func (s *State) FadeFactor() float64 {
	return s.fadeFactor
}

// Mode returns the current concealment mode.
func (s *State) Mode() Mode {
	return s.mode
}

// SetLastFrameParams stores parameters from the last good frame.
// These parameters are used to generate concealment of the correct
// duration and channel configuration.
//
// Parameters:
//   - mode: The Opus mode (SILK, CELT, or Hybrid)
//   - frameSize: Frame size in samples at 48kHz
//   - channels: Number of channels (1 or 2)
func (s *State) SetLastFrameParams(mode Mode, frameSize, channels int) {
	s.mode = mode
	s.lastFrameSize = frameSize
	s.lastChannels = channels
}

// LastFrameSize returns the frame size from the last good packet.
func (s *State) LastFrameSize() int {
	return s.lastFrameSize
}

// LastChannels returns the channel count from the last good packet.
func (s *State) LastChannels() int {
	return s.lastChannels
}

// IsExhausted returns true if PLC has exceeded its maximum concealment.
// After this, the output should effectively be silence.
func (s *State) IsExhausted() bool {
	return s.lostCount >= MaxConcealedFrames || s.fadeFactor <= 0.001
}
