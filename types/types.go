// Package types defines shared types used across gopus packages.
// This package exists to break import cycles between packages.
package types

// Mode represents the Opus coding mode.
type Mode uint8

const (
	ModeSILK   Mode = iota // SILK-only mode (configs 0-11)
	ModeHybrid             // Hybrid SILK+CELT (configs 12-15)
	ModeCELT               // CELT-only mode (configs 16-31)
)

// Bandwidth represents the audio bandwidth.
type Bandwidth uint8

const (
	BandwidthNarrowband    Bandwidth = iota // 4kHz audio, 8kHz sample rate
	BandwidthMediumband                     // 6kHz audio, 12kHz sample rate
	BandwidthWideband                       // 8kHz audio, 16kHz sample rate
	BandwidthSuperwideband                  // 12kHz audio, 24kHz sample rate
	BandwidthFullband                       // 20kHz audio, 48kHz sample rate
)

// Signal represents the input signal type hint for the encoder.
// This helps the encoder optimize for speech or music content.
type Signal int

const (
	// SignalAuto lets the encoder detect the signal type automatically.
	SignalAuto Signal = -1000
	// SignalVoice hints that the input is speech, biasing toward SILK mode.
	SignalVoice Signal = 3001
	// SignalMusic hints that the input is music, biasing toward CELT mode.
	SignalMusic Signal = 3002
)
