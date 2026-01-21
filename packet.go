// packet.go implements TOC byte parsing and packet frame extraction per RFC 6716 Section 3.

package gopus

import "errors"

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

// ParseTOC parses a TOC byte and returns the decoded fields.
func ParseTOC(b byte) TOC {
	config := b >> 3          // Top 5 bits
	stereo := (b & 0x04) != 0 // Bit 2
	frameCode := b & 0x03     // Bottom 2 bits

	entry := configTable[config]

	return TOC{
		Config:    config,
		Mode:      entry.Mode,
		Bandwidth: entry.Bandwidth,
		FrameSize: entry.FrameSize,
		Stereo:    stereo,
		FrameCode: frameCode,
	}
}

// Errors returned by packet parsing functions.
var (
	ErrPacketTooShort    = errors.New("opus: packet too short")
	ErrInvalidFrameCount = errors.New("opus: invalid frame count (M > 48)")
	ErrInvalidPacket     = errors.New("opus: invalid packet structure")
)
