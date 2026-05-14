// packet.go implements TOC byte parsing and packet frame extraction per RFC 6716 Section 3.

package gopus

import "github.com/thesyncim/gopus/types"

// Mode is an alias for types.Mode representing the Opus coding mode.
type Mode = types.Mode

// Bandwidth is an alias for types.Bandwidth representing the audio bandwidth.
type Bandwidth = types.Bandwidth

// Re-export mode constants for convenience.
const (
	ModeSILK   = types.ModeSILK   // SILK-only mode (configs 0-11)
	ModeHybrid = types.ModeHybrid // Hybrid SILK+CELT (configs 12-15)
	ModeCELT   = types.ModeCELT   // CELT-only mode (configs 16-31)
)

// Re-export bandwidth constants for convenience.
const (
	BandwidthNarrowband    = types.BandwidthNarrowband    // 4kHz audio, 8kHz sample rate
	BandwidthMediumband    = types.BandwidthMediumband    // 6kHz audio, 12kHz sample rate
	BandwidthWideband      = types.BandwidthWideband      // 8kHz audio, 16kHz sample rate
	BandwidthSuperwideband = types.BandwidthSuperwideband // 12kHz audio, 24kHz sample rate
	BandwidthFullband      = types.BandwidthFullband      // 20kHz audio, 48kHz sample rate
	BandwidthUnknown       = Bandwidth(255)               // No packet bandwidth has been observed yet
)

// TOC represents the parsed Table of Contents byte from an Opus packet.
type TOC struct {
	Config    uint8     // Configuration 0-31
	Mode      Mode      // Derived from config
	Bandwidth Bandwidth // Derived from config
	FrameSize int       // Frame size in samples at 48kHz
	Stereo    bool      // True if stereo
	FrameCode uint8     // Code 0-3
}

// configEntry holds the mode, bandwidth, and frame size for a configuration.
type configEntry struct {
	Mode      Mode
	Bandwidth Bandwidth
	FrameSize int // In samples at 48kHz
}

// configTable maps configuration indices 0-31 to their properties.
// Based on RFC 6716 Section 3.1 Table.
var configTable = [32]configEntry{
	// SILK-only NB: configs 0-3 (10/20/40/60ms)
	{ModeSILK, BandwidthNarrowband, 480},  // 0: 10ms
	{ModeSILK, BandwidthNarrowband, 960},  // 1: 20ms
	{ModeSILK, BandwidthNarrowband, 1920}, // 2: 40ms
	{ModeSILK, BandwidthNarrowband, 2880}, // 3: 60ms
	// SILK-only MB: configs 4-7
	{ModeSILK, BandwidthMediumband, 480},  // 4
	{ModeSILK, BandwidthMediumband, 960},  // 5
	{ModeSILK, BandwidthMediumband, 1920}, // 6
	{ModeSILK, BandwidthMediumband, 2880}, // 7
	// SILK-only WB: configs 8-11
	{ModeSILK, BandwidthWideband, 480},  // 8
	{ModeSILK, BandwidthWideband, 960},  // 9
	{ModeSILK, BandwidthWideband, 1920}, // 10
	{ModeSILK, BandwidthWideband, 2880}, // 11
	// Hybrid SWB: configs 12-13
	{ModeHybrid, BandwidthSuperwideband, 480}, // 12: 10ms
	{ModeHybrid, BandwidthSuperwideband, 960}, // 13: 20ms
	// Hybrid FB: configs 14-15
	{ModeHybrid, BandwidthFullband, 480}, // 14
	{ModeHybrid, BandwidthFullband, 960}, // 15
	// CELT NB: configs 16-19 (2.5/5/10/20ms)
	{ModeCELT, BandwidthNarrowband, 120}, // 16: 2.5ms
	{ModeCELT, BandwidthNarrowband, 240}, // 17: 5ms
	{ModeCELT, BandwidthNarrowband, 480}, // 18: 10ms
	{ModeCELT, BandwidthNarrowband, 960}, // 19: 20ms
	// CELT WB: configs 20-23
	{ModeCELT, BandwidthWideband, 120}, // 20
	{ModeCELT, BandwidthWideband, 240}, // 21
	{ModeCELT, BandwidthWideband, 480}, // 22
	{ModeCELT, BandwidthWideband, 960}, // 23
	// CELT SWB: configs 24-27
	{ModeCELT, BandwidthSuperwideband, 120}, // 24
	{ModeCELT, BandwidthSuperwideband, 240}, // 25
	{ModeCELT, BandwidthSuperwideband, 480}, // 26
	{ModeCELT, BandwidthSuperwideband, 960}, // 27
	// CELT FB: configs 28-31
	{ModeCELT, BandwidthFullband, 120}, // 28
	{ModeCELT, BandwidthFullband, 240}, // 29
	{ModeCELT, BandwidthFullband, 480}, // 30
	{ModeCELT, BandwidthFullband, 960}, // 31
}

var tocTable = func() [256]TOC {
	var table [256]TOC
	for b := range table {
		config := uint8(b) >> 3
		entry := configTable[config]
		table[b] = TOC{
			Config:    config,
			Mode:      entry.Mode,
			Bandwidth: entry.Bandwidth,
			FrameSize: entry.FrameSize,
			Stereo:    (uint8(b) & 0x04) != 0,
			FrameCode: uint8(b) & 0x03,
		}
	}
	return table
}()

// GenerateTOC creates a TOC byte from encoding parameters.
// config: Configuration index 0-31 (from configTable)
// stereo: True for stereo, false for mono
// frameCode: Frame count code 0-3
//
//	0: 1 frame
//	1: 2 equal-sized frames
//	2: 2 different-sized frames
//	3: arbitrary number of frames
func GenerateTOC(config uint8, stereo bool, frameCode uint8) byte {
	toc := (config & 0x1F) << 3
	if stereo {
		toc |= 0x04
	}
	toc |= frameCode & 0x03
	return toc
}

// ConfigFromParams returns the config index for given mode, bandwidth, and frame size.
// Returns -1 if the combination is invalid.
func ConfigFromParams(mode Mode, bandwidth Bandwidth, frameSize int) int {
	// Search configTable for matching entry
	for i, entry := range configTable {
		if entry.Mode == mode && entry.Bandwidth == bandwidth && entry.FrameSize == frameSize {
			return i
		}
	}
	return -1
}

// ValidConfig returns true if the configuration index is valid.
func ValidConfig(config uint8) bool {
	return config < 32
}

// ParseTOC parses a TOC byte and returns the decoded fields.
func ParseTOC(b byte) TOC {
	return tocTable[b]
}
