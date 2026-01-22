// Package encoder provides packet assembly functions for combining TOC bytes
// with encoded frame data to create complete Opus packets per RFC 6716.
//
// Reference: RFC 6716 Section 3
package encoder

import (
	"errors"

	"gopus/internal/types"
)

// Packet assembly errors.
var (
	ErrInvalidConfig     = errors.New("encoder: invalid config for mode/bandwidth/frameSize")
	ErrInvalidFrameCount = errors.New("encoder: frame count must be 1-48")
)

// configEntry holds the mode, bandwidth, and frame size for a configuration.
type configEntry struct {
	Mode      types.Mode
	Bandwidth types.Bandwidth
	FrameSize int // In samples at 48kHz
}

// configTable maps configuration indices 0-31 to their properties.
// Based on RFC 6716 Section 3.1 Table.
var configTable = [32]configEntry{
	// SILK-only NB: configs 0-3 (10/20/40/60ms)
	{types.ModeSILK, types.BandwidthNarrowband, 480},  // 0: 10ms
	{types.ModeSILK, types.BandwidthNarrowband, 960},  // 1: 20ms
	{types.ModeSILK, types.BandwidthNarrowband, 1920}, // 2: 40ms
	{types.ModeSILK, types.BandwidthNarrowband, 2880}, // 3: 60ms
	// SILK-only MB: configs 4-7
	{types.ModeSILK, types.BandwidthMediumband, 480},  // 4
	{types.ModeSILK, types.BandwidthMediumband, 960},  // 5
	{types.ModeSILK, types.BandwidthMediumband, 1920}, // 6
	{types.ModeSILK, types.BandwidthMediumband, 2880}, // 7
	// SILK-only WB: configs 8-11
	{types.ModeSILK, types.BandwidthWideband, 480},  // 8
	{types.ModeSILK, types.BandwidthWideband, 960},  // 9
	{types.ModeSILK, types.BandwidthWideband, 1920}, // 10
	{types.ModeSILK, types.BandwidthWideband, 2880}, // 11
	// Hybrid SWB: configs 12-13
	{types.ModeHybrid, types.BandwidthSuperwideband, 480}, // 12: 10ms
	{types.ModeHybrid, types.BandwidthSuperwideband, 960}, // 13: 20ms
	// Hybrid FB: configs 14-15
	{types.ModeHybrid, types.BandwidthFullband, 480}, // 14
	{types.ModeHybrid, types.BandwidthFullband, 960}, // 15
	// CELT NB: configs 16-19 (2.5/5/10/20ms)
	{types.ModeCELT, types.BandwidthNarrowband, 120}, // 16: 2.5ms
	{types.ModeCELT, types.BandwidthNarrowband, 240}, // 17: 5ms
	{types.ModeCELT, types.BandwidthNarrowband, 480}, // 18: 10ms
	{types.ModeCELT, types.BandwidthNarrowband, 960}, // 19: 20ms
	// CELT WB: configs 20-23
	{types.ModeCELT, types.BandwidthWideband, 120}, // 20
	{types.ModeCELT, types.BandwidthWideband, 240}, // 21
	{types.ModeCELT, types.BandwidthWideband, 480}, // 22
	{types.ModeCELT, types.BandwidthWideband, 960}, // 23
	// CELT SWB: configs 24-27
	{types.ModeCELT, types.BandwidthSuperwideband, 120}, // 24
	{types.ModeCELT, types.BandwidthSuperwideband, 240}, // 25
	{types.ModeCELT, types.BandwidthSuperwideband, 480}, // 26
	{types.ModeCELT, types.BandwidthSuperwideband, 960}, // 27
	// CELT FB: configs 28-31
	{types.ModeCELT, types.BandwidthFullband, 120}, // 28
	{types.ModeCELT, types.BandwidthFullband, 240}, // 29
	{types.ModeCELT, types.BandwidthFullband, 480}, // 30
	{types.ModeCELT, types.BandwidthFullband, 960}, // 31
}

// configFromParams returns the config index for given mode, bandwidth, and frame size.
// Returns -1 if the combination is invalid.
func configFromParams(mode types.Mode, bandwidth types.Bandwidth, frameSize int) int {
	for i, entry := range configTable {
		if entry.Mode == mode && entry.Bandwidth == bandwidth && entry.FrameSize == frameSize {
			return i
		}
	}
	return -1
}

// generateTOC creates a TOC byte from encoding parameters.
func generateTOC(config uint8, stereo bool, frameCode uint8) byte {
	toc := (config & 0x1F) << 3
	if stereo {
		toc |= 0x04
	}
	toc |= frameCode & 0x03
	return toc
}

// BuildPacket creates a complete Opus packet from encoded frame data.
// Uses frame code 0 (single frame).
func BuildPacket(frameData []byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool) ([]byte, error) {
	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	toc := generateTOC(uint8(config), stereo, 0)

	// Packet = TOC + frame data
	packet := make([]byte, 1+len(frameData))
	packet[0] = toc
	copy(packet[1:], frameData)

	return packet, nil
}

// BuildMultiFramePacket creates a packet with multiple frames (code 3).
// frames: slice of encoded frame data
// vbr: true for variable bitrate (different frame sizes), false for CBR
func BuildMultiFramePacket(frames [][]byte, mode types.Mode, bandwidth types.Bandwidth, frameSize int, stereo bool, vbr bool) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 48 {
		return nil, ErrInvalidFrameCount
	}

	config := configFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	toc := generateTOC(uint8(config), stereo, 3) // Code 3

	// Frame count byte: VBR flag | padding flag | count
	var countByte byte
	if vbr {
		countByte |= 0x80 // VBR bit
	}
	countByte |= byte(len(frames) & 0x3F)

	// Calculate total size
	headerSize := 2 // TOC + count
	if vbr {
		// Add frame length bytes for all but last frame
		for i := 0; i < len(frames)-1; i++ {
			headerSize += frameLengthBytes(len(frames[i]))
		}
	}

	totalFrameSize := 0
	for _, f := range frames {
		totalFrameSize += len(f)
	}

	packet := make([]byte, headerSize+totalFrameSize)
	packet[0] = toc
	packet[1] = countByte

	offset := 2
	if vbr {
		// Write frame lengths for all but last
		for i := 0; i < len(frames)-1; i++ {
			n := writeFrameLength(packet[offset:], len(frames[i]))
			offset += n
		}
	}

	// Write frame data
	for _, f := range frames {
		copy(packet[offset:], f)
		offset += len(f)
	}

	return packet, nil
}

// frameLengthBytes returns number of bytes needed to encode frame length.
func frameLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

// writeFrameLength writes frame length at offset, returns bytes written.
func writeFrameLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	// Two-byte encoding per RFC 6716 Section 3.2.1:
	// For lengths >= 252, use two bytes where:
	// length = 4*secondByte + firstByte
	// Solve for firstByte and secondByte:
	// firstByte = 252 + (length % 4)  (must be >= 252, so add base)
	// secondByte = (length - firstByte) / 4 = (length - 252 - (length % 4)) / 4
	//            = (length - 252) / 4 (integer division handles the remainder)
	dst[0] = byte(252 + (length % 4))
	dst[1] = byte((length - 252) / 4)
	return 2
}
