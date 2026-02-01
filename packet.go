// packet.go implements TOC byte parsing and packet frame extraction per RFC 6716 Section 3.

package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/types"
)

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

// PacketInfo contains parsed information about an Opus packet.
type PacketInfo struct {
	TOC        TOC   // Parsed TOC byte
	FrameCount int   // Number of frames (1-48 for code 3)
	FrameSizes []int // Size in bytes of each frame
	Padding    int   // Padding bytes (code 3 only)
	TotalSize  int   // Total packet size
}

// ParsePacket parses an Opus packet and returns information about its structure.
// It determines the frame boundaries based on the TOC byte's frame code (0-3).
func ParsePacket(data []byte) (PacketInfo, error) {
	if len(data) < 1 {
		return PacketInfo{}, ErrPacketTooShort
	}

	toc := ParseTOC(data[0])
	info := PacketInfo{
		TOC:       toc,
		TotalSize: len(data),
	}

	switch toc.FrameCode {
	case 0:
		// Code 0: One frame
		info.FrameCount = 1
		info.FrameSizes = []int{len(data) - 1}

	case 1:
		// Code 1: Two equal-sized frames
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return PacketInfo{}, ErrInvalidPacket
		}
		frameSize := frameDataLen / 2
		info.FrameCount = 2
		info.FrameSizes = []int{frameSize, frameSize}

	case 2:
		// Code 2: Two frames with different sizes
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return PacketInfo{}, err
		}
		headerLen := 1 + bytesRead
		frame2Len := len(data) - headerLen - frame1Len
		if frame2Len < 0 {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 2
		info.FrameSizes = []int{frame1Len, frame2Len}

	case 3:
		// Code 3: Arbitrary number of frames
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		m := int(frameCountByte & 0x3F)

		if m == 0 || m > 48 {
			return PacketInfo{}, ErrInvalidFrameCount
		}

		offset := 2
		padding := 0

		// Parse padding if present
		if hasPadding {
			for {
				if offset >= len(data) {
					return PacketInfo{}, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
				}
				if padByte < 255 {
					break
				}
			}
		}

		info.FrameCount = m
		info.Padding = padding
		info.FrameSizes = make([]int, m)

		if vbr {
			// VBR: Parse each frame length (except last)
			totalFrameLen := 0
			for i := 0; i < m-1; i++ {
				frameLen, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return PacketInfo{}, err
				}
				info.FrameSizes[i] = frameLen
				totalFrameLen += frameLen
				offset += bytesRead
			}
			// Last frame is remainder
			lastFrameLen := len(data) - offset - padding - totalFrameLen
			if lastFrameLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			info.FrameSizes[m-1] = lastFrameLen
		} else {
			// CBR: Parse single frame length, all frames are same size
			// For CBR, no frame lengths are encoded. All frames share the
			// remaining bytes (minus padding) equally.
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			if m == 0 {
				return PacketInfo{}, ErrInvalidFrameCount
			}
			if frameDataLen%m != 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / m
			for i := 0; i < m; i++ {
				info.FrameSizes[i] = frameLen
			}
		}
	}

	return info, nil
}

// parseFrameLength parses a frame length from the packet data at the given offset.
// Per RFC 6716 Section 3.2.1, lengths < 252 use one byte, lengths >= 252 use two bytes.
// Returns the length, number of bytes read, and any error.
func parseFrameLength(data []byte, offset int) (int, int, error) {
	if offset >= len(data) {
		return 0, 0, ErrPacketTooShort
	}

	firstByte := int(data[offset])
	if firstByte < 252 {
		return firstByte, 1, nil
	}

	// Two-byte encoding: length = 4*secondByte + firstByte
	if offset+1 >= len(data) {
		return 0, 0, ErrPacketTooShort
	}
	secondByte := int(data[offset+1])
	return 4*secondByte + firstByte, 2, nil
}
