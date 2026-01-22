// Package encoder provides packet assembly functions for combining TOC bytes
// with encoded frame data to create complete Opus packets per RFC 6716.
//
// Reference: RFC 6716 Section 3
package encoder

import (
	"errors"

	"gopus"
)

// Packet assembly errors.
var (
	ErrInvalidConfig     = errors.New("encoder: invalid config for mode/bandwidth/frameSize")
	ErrInvalidFrameCount = errors.New("encoder: frame count must be 1-48")
)

// BuildPacket creates a complete Opus packet from encoded frame data.
// Uses frame code 0 (single frame).
func BuildPacket(frameData []byte, mode gopus.Mode, bandwidth gopus.Bandwidth, frameSize int, stereo bool) ([]byte, error) {
	config := gopus.ConfigFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	toc := gopus.GenerateTOC(uint8(config), stereo, 0)

	// Packet = TOC + frame data
	packet := make([]byte, 1+len(frameData))
	packet[0] = toc
	copy(packet[1:], frameData)

	return packet, nil
}

// BuildMultiFramePacket creates a packet with multiple frames (code 3).
// frames: slice of encoded frame data
// vbr: true for variable bitrate (different frame sizes), false for CBR
func BuildMultiFramePacket(frames [][]byte, mode gopus.Mode, bandwidth gopus.Bandwidth, frameSize int, stereo bool, vbr bool) ([]byte, error) {
	if len(frames) == 0 || len(frames) > 48 {
		return nil, ErrInvalidFrameCount
	}

	config := gopus.ConfigFromParams(mode, bandwidth, frameSize)
	if config < 0 {
		return nil, ErrInvalidConfig
	}

	toc := gopus.GenerateTOC(uint8(config), stereo, 3) // Code 3

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
