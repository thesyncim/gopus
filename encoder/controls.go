// Package encoder provides bitrate and bandwidth control for the Opus encoder.
// This file implements VBR/CBR/CVBR modes and bitrate management per RFC 6716.
//
// Reference: RFC 6716 Section 2.1.1
package encoder

// BitrateMode specifies how the encoder manages packet sizes.
type BitrateMode int

const (
	// ModeVBR is variable bitrate mode (default).
	// Packet size varies based on content complexity.
	// Provides best quality for a given average bitrate.
	ModeVBR BitrateMode = iota

	// ModeCVBR is constrained variable bitrate mode.
	// Packet size varies but stays within +/-15% of target.
	// Good balance of quality and bandwidth predictability.
	ModeCVBR

	// ModeCBR is constant bitrate mode.
	// Every packet is exactly the same size (or within 1 byte).
	// Required for some streaming protocols.
	ModeCBR
)

// Bitrate limits per RFC 6716
const (
	MinBitrate = 6000   // 6 kbps minimum
	MaxBitrate = 510000 // 510 kbps maximum

	// Mode-specific typical ranges
	SILKMinBitrate   = 6000   // 6 kbps
	SILKMaxBitrate   = 40000  // 40 kbps (WB)
	CELTMinBitrate   = 32000  // 32 kbps
	CELTMaxBitrate   = 510000 // 510 kbps
	HybridMinBitrate = 12000  // 12 kbps
	HybridMaxBitrate = 128000 // 128 kbps typical

	// Maximum SILK packet size in bytes (libopus MAX_DATA_BYTES).
	maxSilkPacketBytes = 1275
)

// CVBR tolerance (percentage)
const CVBRTolerance = 0.15 // +/- 15%

// ValidBitrate returns true if the bitrate is within Opus limits.
func ValidBitrate(bitrate int) bool {
	return bitrate >= MinBitrate && bitrate <= MaxBitrate
}

// ClampBitrate ensures bitrate is within valid range.
func ClampBitrate(bitrate int) int {
	if bitrate < MinBitrate {
		return MinBitrate
	}
	if bitrate > MaxBitrate {
		return MaxBitrate
	}
	return bitrate
}

// frameDurationMs returns frame duration in milliseconds.
func frameDurationMs(frameSize int) int {
	// At 48kHz: 480 samples = 10ms, 960 = 20ms, etc.
	return frameSize * 1000 / 48000
}

// targetBytesForBitrate computes target packet size in bytes.
func targetBytesForBitrate(bitrate, frameSize int) int {
	durationMs := frameDurationMs(frameSize)
	// bitrate is bits/second, convert to bytes/frame
	return (bitrate * durationMs) / 8000
}

// padToSize pads packet to exact size without truncating.
// Used for CBR mode.
func padToSize(packet []byte, targetSize int) []byte {
	if len(packet) >= targetSize {
		return packet
	}
	if len(packet) == 0 {
		return packet
	}

	toc, frames, err := parsePacketFrames(packet)
	if err != nil || len(frames) == 0 || len(frames) > 48 {
		return packet
	}

	totalFrameBytes := 0
	lengthBytes := 0
	for i, frame := range frames {
		totalFrameBytes += len(frame)
		if i < len(frames)-1 {
			lengthBytes += frameLengthBytes(len(frame))
		}
	}

	base := 1 + 1 + lengthBytes + totalFrameBytes
	if base >= targetSize {
		return packet
	}

	extraNeeded := targetSize - base
	padding := 0
	padLenBytes := 0
	for k := 1; k <= 10; k++ {
		p := extraNeeded - k
		if p <= 0 {
			continue
		}
		minP := 254*(k-1) + 1
		maxP := 254 * k
		if p >= minP && p <= maxP {
			padding = p
			padLenBytes = k
			break
		}
	}
	if padding == 0 {
		return packet
	}

	newLen := base + padLenBytes + padding
	if newLen != targetSize {
		return packet
	}

	padded := make([]byte, newLen)
	padded[0] = (toc & 0xFC) | 0x03
	countByte := byte(len(frames)&0x3F) | 0x80
	if padding > 0 {
		countByte |= 0x40
	}
	padded[1] = countByte

	offset := 2
	remaining := padding
	for remaining > 254 {
		padded[offset] = 255
		offset++
		remaining -= 254
	}
	padded[offset] = byte(remaining)
	offset++

	for i := 0; i < len(frames)-1; i++ {
		offset += writeFrameLength(padded[offset:], len(frames[i]))
	}
	for _, frame := range frames {
		copy(padded[offset:], frame)
		offset += len(frame)
	}

	return padded
}

// constrainSize adjusts packet size to stay within CVBR tolerance.
func constrainSize(packet []byte, target int, tolerance float64) []byte {
	minSize := int(float64(target) * (1 - tolerance))
	maxSize := int(float64(target) * (1 + tolerance))

	if len(packet) < minSize {
		for size := minSize; size <= maxSize; size++ {
			padded := padToSize(packet, size)
			if len(padded) >= size {
				return padded
			}
		}
		return packet
	}
	if len(packet) > maxSize {
		return packet
	}
	return packet
}
