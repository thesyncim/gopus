package celt

import "errors"

// ModeConfig contains frame-size-dependent configuration for CELT decoding.
// Parameters vary based on frame duration (2.5ms to 20ms).
type ModeConfig struct {
	FrameSize   int // Samples at 48kHz: 120, 240, 480, 960
	ShortBlocks int // Number of short MDCTs if transient: 1, 2, 4, 8
	LM          int // Log mode index: 0, 1, 2, 3
	EffBands    int // Effective number of bands for this frame size
	MDCTSize    int // MDCT window size for long blocks
}

// GetModeConfig returns the mode configuration for the given frame size.
// Valid frame sizes are 120, 240, 480, and 960 samples at 48kHz.
func GetModeConfig(frameSize int) ModeConfig {
	switch frameSize {
	case 120: // 2.5ms frame
		return ModeConfig{
			FrameSize:   120,
			ShortBlocks: 1,
			LM:          0,
			EffBands:    21,
			MDCTSize:    120,
		}
	case 240: // 5ms frame
		return ModeConfig{
			FrameSize:   240,
			ShortBlocks: 2,
			LM:          1,
			EffBands:    21,
			MDCTSize:    240,
		}
	case 480: // 10ms frame
		return ModeConfig{
			FrameSize:   480,
			ShortBlocks: 4,
			LM:          2,
			EffBands:    21,
			MDCTSize:    480,
		}
	case 960: // 20ms frame
		return ModeConfig{
			FrameSize:   960,
			ShortBlocks: 8,
			LM:          3,
			EffBands:    21,
			MDCTSize:    960,
		}
	default:
		// Default to 20ms frame for invalid sizes.
		return ModeConfig{
			FrameSize:   960,
			ShortBlocks: 8,
			LM:          3,
			EffBands:    21,
			MDCTSize:    960,
		}
	}
}

// ValidFrameSize returns true if the frame size is valid for CELT.
func ValidFrameSize(frameSize int) bool {
	switch frameSize {
	case 120, 240, 480, 960:
		return true
	default:
		return false
	}
}

// FrameSizeFromDuration returns the frame size in samples for a given
// duration in milliseconds. Valid durations: 2.5, 5, 10, 20ms.
func FrameSizeFromDuration(durationMs float64) (int, error) {
	switch {
	case durationMs == 2.5:
		return 120, nil
	case durationMs == 5.0:
		return 240, nil
	case durationMs == 10.0:
		return 480, nil
	case durationMs == 20.0:
		return 960, nil
	default:
		return 0, errors.New("invalid CELT frame duration")
	}
}

// DurationFromFrameSize returns the frame duration in milliseconds.
func DurationFromFrameSize(frameSize int) float64 {
	return float64(frameSize) / 48.0 // 48kHz sample rate
}

// CELTBandwidth represents the audio bandwidth for CELT coding.
type CELTBandwidth int

const (
	// CELTNarrowband represents 4kHz audio bandwidth (narrowband).
	CELTNarrowband CELTBandwidth = iota
	// CELTMediumband represents 6kHz audio bandwidth (mediumband).
	CELTMediumband
	// CELTWideband represents 8kHz audio bandwidth (wideband).
	CELTWideband
	// CELTSuperwideband represents 12kHz audio bandwidth (super-wideband).
	CELTSuperwideband
	// CELTFullband represents 20kHz audio bandwidth (fullband).
	CELTFullband
)

// String returns the string representation of the bandwidth.
func (bw CELTBandwidth) String() string {
	switch bw {
	case CELTNarrowband:
		return "narrowband"
	case CELTMediumband:
		return "mediumband"
	case CELTWideband:
		return "wideband"
	case CELTSuperwideband:
		return "super-wideband"
	case CELTFullband:
		return "fullband"
	default:
		return "unknown"
	}
}

// MaxFrequency returns the maximum audio frequency in Hz for this bandwidth.
func (bw CELTBandwidth) MaxFrequency() int {
	switch bw {
	case CELTNarrowband:
		return 4000
	case CELTMediumband:
		return 6000
	case CELTWideband:
		return 8000
	case CELTSuperwideband:
		return 12000
	case CELTFullband:
		return 20000
	default:
		return 20000
	}
}

// EffectiveBands returns the number of coded bands for the given bandwidth.
// This is the maximum number of bands; actual coded bands may be fewer
// depending on frame size and bit allocation.
func (bw CELTBandwidth) EffectiveBands() int {
	switch bw {
	case CELTNarrowband:
		return 13
	case CELTMediumband:
		return 15
	case CELTWideband:
		return 17
	case CELTSuperwideband:
		return 19
	case CELTFullband:
		return 21
	default:
		return MaxBands
	}
}

// EffectiveBandsForFrameSize returns the effective band count considering
// both bandwidth and frame size constraints.
func EffectiveBandsForFrameSize(bw CELTBandwidth, frameSize int) int {
	bwBands := bw.EffectiveBands()
	modeCfg := GetModeConfig(frameSize)

	// Use minimum of bandwidth limit and frame size limit
	if bwBands < modeCfg.EffBands {
		return bwBands
	}
	return modeCfg.EffBands
}

// BandwidthFromOpusConfig returns the CELT bandwidth from an Opus TOC bandwidth field.
// Opus TOC bandwidth values: 0=NB, 1=MB, 2=WB, 3=SWB, 4=FB
func BandwidthFromOpusConfig(opusBandwidth int) CELTBandwidth {
	switch opusBandwidth {
	case 0:
		return CELTNarrowband
	case 1:
		// libopus uses end band 17 for Opus mediumband (treat as wideband for CELT).
		return CELTWideband
	case 2:
		return CELTWideband
	case 3:
		return CELTSuperwideband
	case 4:
		return CELTFullband
	default:
		return CELTFullband // Default to fullband
	}
}

// LMToFrameSize converts LM (log mode) index to frame size in samples.
func LMToFrameSize(lm int) int {
	switch lm {
	case 0:
		return 120
	case 1:
		return 240
	case 2:
		return 480
	case 3:
		return 960
	default:
		return 960
	}
}

// FrameSizeToLM converts frame size to LM (log mode) index.
func FrameSizeToLM(frameSize int) int {
	cfg := GetModeConfig(frameSize)
	return cfg.LM
}
