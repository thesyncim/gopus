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

const (
	maxRepacketizerFrames      = 48
	maxRepacketizerDuration48k = 5760 // 120ms at 48kHz
)

// Repacketizer accumulates Opus packet frames and emits new packets assembled
// from any contiguous frame range.
//
// It mirrors libopus repacketizer behavior:
//   - all added packets must share TOC bits 7..2,
//   - total stored duration must not exceed 120ms.
type Repacketizer struct {
	toc       byte
	frameSize int
	frames    [][]byte
}

// NewRepacketizer creates a new repacketizer state.
func NewRepacketizer() *Repacketizer {
	r := &Repacketizer{
		frames: make([][]byte, 0, maxRepacketizerFrames),
	}
	r.Reset()
	return r
}

// Reset clears repacketizer state.
func (r *Repacketizer) Reset() {
	r.toc = 0
	r.frameSize = 0
	r.frames = r.frames[:0]
}

// NumFrames returns the number of frames currently accumulated.
func (r *Repacketizer) NumFrames() int {
	return len(r.frames)
}

// Cat adds one Opus packet to the repacketizer state.
func (r *Repacketizer) Cat(packet []byte) error {
	if len(packet) < 1 {
		return ErrInvalidPacket
	}

	info, frames, err := parsePacketFrames(packet)
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return ErrInvalidPacket
	}

	if len(r.frames) == 0 {
		r.toc = packet[0]
		r.frameSize = info.TOC.FrameSize
	} else if (r.toc & 0xFC) != (packet[0] & 0xFC) {
		return ErrInvalidPacket
	}

	totalFrames := len(r.frames) + len(frames)
	if totalFrames > maxRepacketizerFrames {
		return ErrInvalidPacket
	}
	if totalFrames*r.frameSize > maxRepacketizerDuration48k {
		return ErrInvalidPacket
	}

	for _, frame := range frames {
		owned := make([]byte, len(frame))
		copy(owned, frame)
		r.frames = append(r.frames, owned)
	}

	return nil
}

// OutRange assembles frames [begin, end) into one Opus packet.
func (r *Repacketizer) OutRange(begin, end int, data []byte) (int, error) {
	if begin < 0 || begin >= end || end > len(r.frames) {
		return 0, ErrInvalidArgument
	}
	return buildRepacketizedPacket(r.toc&0xFC, r.frames[begin:end], data)
}

// Out assembles all accumulated frames into one Opus packet.
func (r *Repacketizer) Out(data []byte) (int, error) {
	return r.OutRange(0, len(r.frames), data)
}

// PacketPad pads a packet in-place to newLen bytes.
//
// data must have capacity for at least newLen bytes.
// length is the current packet length in data.
func PacketPad(data []byte, length, newLen int) error {
	if length < 1 || newLen < length {
		return ErrInvalidArgument
	}
	if newLen == length {
		return nil
	}
	if length > len(data) {
		return ErrInvalidArgument
	}
	if newLen > cap(data) {
		return ErrBufferTooSmall
	}
	data = data[:newLen]

	src := make([]byte, length)
	copy(src, data[:length])

	_, frames, err := parsePacketFrames(src)
	if err != nil {
		return err
	}

	_, err = buildCode3Packet(src[0]&0xFC, frames, data, newLen, true)
	return err
}

// PacketUnpad removes packet padding in-place and returns the new packet length.
func PacketUnpad(data []byte, length int) (int, error) {
	if length < 1 || length > len(data) {
		return 0, ErrInvalidArgument
	}

	src := make([]byte, length)
	copy(src, data[:length])

	_, frames, err := parsePacketFrames(src)
	if err != nil {
		return 0, err
	}

	return buildRepacketizedPacket(src[0]&0xFC, frames, data[:length])
}

func parseSelfDelimitedPacket(data []byte) (tocBase byte, frames [][]byte, consumed int, err error) {
	if len(data) < 1 {
		return 0, nil, 0, ErrPacketTooShort
	}

	toc := data[0]
	code := toc & 0x03
	offset := 1
	padding := 0
	frameCount := 1
	frameSizes := make([]int, 0, 2)

	switch code {
	case 0:
		length, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, 0, err
		}
		offset += bytesRead
		frameSizes = append(frameSizes, length)

	case 1:
		length, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, 0, err
		}
		offset += bytesRead
		frameCount = 2
		frameSizes = append(frameSizes, length, length)

	case 2:
		length0, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, 0, err
		}
		offset += bytesRead

		length1, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, 0, err
		}
		offset += bytesRead

		frameCount = 2
		frameSizes = append(frameSizes, length0, length1)

	case 3:
		if offset >= len(data) {
			return 0, nil, 0, ErrPacketTooShort
		}
		frameCountByte := data[offset]
		offset++

		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount = int(frameCountByte & 0x3F)
		if frameCount == 0 || frameCount > maxRepacketizerFrames {
			return 0, nil, 0, ErrInvalidPacket
		}

		frameSizes = make([]int, frameCount)
		if hasPadding {
			for {
				if offset >= len(data) {
					return 0, nil, 0, ErrPacketTooShort
				}
				padByte := int(data[offset])
				offset++
				if padByte == 255 {
					padding += 254
				} else {
					padding += padByte
					break
				}
			}
		}

		if vbr {
			for i := 0; i < frameCount-1; i++ {
				length, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return 0, nil, 0, err
				}
				offset += bytesRead
				frameSizes[i] = length
			}
		}

		lastSize, bytesRead, err := parseFrameLength(data, offset)
		if err != nil {
			return 0, nil, 0, err
		}
		offset += bytesRead

		if vbr {
			frameSizes[frameCount-1] = lastSize
		} else {
			for i := 0; i < frameCount; i++ {
				frameSizes[i] = lastSize
			}
		}

	default:
		return 0, nil, 0, ErrInvalidPacket
	}

	totalFrameBytes := 0
	for _, size := range frameSizes {
		if size < 0 {
			return 0, nil, 0, ErrInvalidPacket
		}
		totalFrameBytes += size
	}

	consumed = offset + totalFrameBytes + padding
	if consumed > len(data) {
		return 0, nil, 0, ErrPacketTooShort
	}

	frames = make([][]byte, frameCount)
	frameOffset := offset
	for i := 0; i < frameCount; i++ {
		next := frameOffset + frameSizes[i]
		if next > offset+totalFrameBytes {
			return 0, nil, 0, ErrInvalidPacket
		}
		frames[i] = data[frameOffset:next]
		frameOffset = next
	}

	return toc & 0xFC, frames, consumed, nil
}

func buildSelfDelimitedPacketFromFrames(tocBase byte, frames [][]byte, data []byte) (int, error) {
	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}

	lengths := make([]int, count)
	totalFrameBytes := 0
	for i := 0; i < count; i++ {
		lengths[i] = len(frames[i])
		totalFrameBytes += lengths[i]
	}

	sdBytes := frameLengthBytes(lengths[count-1])
	need := 1 + sdBytes + totalFrameBytes

	offset := 0
	switch count {
	case 1:
		if len(data) < need {
			return 0, ErrBufferTooSmall
		}
		data[offset] = tocBase
		offset++
		offset += encodeFrameLength(data[offset:], lengths[0])
		copy(data[offset:], frames[0])
		offset += lengths[0]
		return offset, nil

	case 2:
		if lengths[0] == lengths[1] {
			if len(data) < need {
				return 0, ErrBufferTooSmall
			}
			data[offset] = tocBase | 0x01
			offset++
			offset += encodeFrameLength(data[offset:], lengths[1])
			copy(data[offset:], frames[0])
			offset += lengths[0]
			copy(data[offset:], frames[1])
			offset += lengths[1]
			return offset, nil
		}

		need += frameLengthBytes(lengths[0])
		if len(data) < need {
			return 0, ErrBufferTooSmall
		}
		data[offset] = tocBase | 0x02
		offset++
		offset += encodeFrameLength(data[offset:], lengths[0])
		offset += encodeFrameLength(data[offset:], lengths[1])
		copy(data[offset:], frames[0])
		offset += lengths[0]
		copy(data[offset:], frames[1])
		offset += lengths[1]
		return offset, nil
	}

	vbr := false
	for i := 1; i < count; i++ {
		if lengths[i] != lengths[0] {
			vbr = true
			break
		}
	}
	if vbr {
		for i := 0; i < count-1; i++ {
			need += frameLengthBytes(lengths[i])
		}
	}
	if len(data) < need+1 {
		return 0, ErrBufferTooSmall
	}

	data[offset] = tocBase | 0x03
	offset++
	if vbr {
		data[offset] = byte(count) | 0x80
		offset++
		for i := 0; i < count-1; i++ {
			offset += encodeFrameLength(data[offset:], lengths[i])
		}
	} else {
		data[offset] = byte(count)
		offset++
	}

	offset += encodeFrameLength(data[offset:], lengths[count-1])
	for i := 0; i < count; i++ {
		copy(data[offset:], frames[i])
		offset += lengths[i]
	}

	return offset, nil
}

func makeSelfDelimitedPacket(packet []byte) ([]byte, error) {
	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		return nil, err
	}

	// Self-delimited adds at most 2 bytes versus standard framing.
	dst := make([]byte, len(packet)+2)
	n, err := buildSelfDelimitedPacketFromFrames(packet[0]&0xFC, frames, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

func decodeSelfDelimitedPacket(data []byte) ([]byte, int, error) {
	tocBase, frames, consumed, err := parseSelfDelimitedPacket(data)
	if err != nil {
		return nil, 0, err
	}

	dst := make([]byte, consumed)
	n, err := buildRepacketizedPacket(tocBase, frames, dst)
	if err != nil {
		return nil, 0, err
	}
	return dst[:n], consumed, nil
}

// MultistreamPacketPad pads the final stream packet inside a multistream packet.
func MultistreamPacketPad(data []byte, length, newLen, numStreams int) error {
	if numStreams < 1 || length < 1 || newLen < length {
		return ErrInvalidArgument
	}
	if length > len(data) {
		return ErrInvalidArgument
	}
	if newLen > cap(data) {
		return ErrBufferTooSmall
	}
	if newLen == length {
		return nil
	}

	src := make([]byte, length)
	copy(src, data[:length])

	offset := 0
	for s := 0; s < numStreams-1; s++ {
		_, _, consumed, err := parseSelfDelimitedPacket(src[offset:length])
		if err != nil {
			return err
		}
		offset += consumed
	}
	if offset >= length {
		return ErrInvalidPacket
	}

	data = data[:newLen]
	copy(data[:length], src)

	lastOldLen := length - offset
	lastNewLen := lastOldLen + (newLen - length)
	return PacketPad(data[offset:], lastOldLen, lastNewLen)
}

// MultistreamPacketUnpad removes padding from all streams in a multistream packet.
// It returns the new packet length.
func MultistreamPacketUnpad(data []byte, length, numStreams int) (int, error) {
	if numStreams < 1 || length < 1 || length > len(data) {
		return 0, ErrInvalidArgument
	}

	src := make([]byte, length)
	copy(src, data[:length])

	srcOffset := 0
	dstOffset := 0
	for s := 0; s < numStreams; s++ {
		selfDelimited := s < numStreams-1

		var packet []byte
		if selfDelimited {
			decoded, consumed, err := decodeSelfDelimitedPacket(src[srcOffset:length])
			if err != nil {
				return 0, err
			}
			packet = decoded
			srcOffset += consumed
		} else {
			if srcOffset >= length {
				return 0, ErrInvalidPacket
			}
			packet = src[srcOffset:length]
			srcOffset = length
		}

		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		newPacketLen, err := PacketUnpad(packetCopy, len(packetCopy))
		if err != nil {
			return 0, err
		}

		if selfDelimited {
			selfDelimitedPacket, err := makeSelfDelimitedPacket(packetCopy[:newPacketLen])
			if err != nil {
				return 0, err
			}
			if dstOffset+len(selfDelimitedPacket) > len(data) {
				return 0, ErrBufferTooSmall
			}
			copy(data[dstOffset:], selfDelimitedPacket)
			dstOffset += len(selfDelimitedPacket)
			continue
		}

		if dstOffset+newPacketLen > len(data) {
			return 0, ErrBufferTooSmall
		}

		copy(data[dstOffset:], packetCopy[:newPacketLen])
		dstOffset += newPacketLen
	}

	return dstOffset, nil
}

func parsePacketFrames(data []byte) (PacketInfo, [][]byte, error) {
	info, err := ParsePacket(data)
	if err != nil {
		return PacketInfo{}, nil, err
	}

	frames := make([][]byte, info.FrameCount)
	switch info.TOC.FrameCode {
	case 0:
		if len(data) < 1+info.FrameSizes[0] {
			return PacketInfo{}, nil, ErrInvalidPacket
		}
		frames[0] = data[1 : 1+info.FrameSizes[0]]
	case 1:
		offset := 1
		for i := 0; i < info.FrameCount; i++ {
			frameLen := info.FrameSizes[i]
			if offset+frameLen > len(data) {
				return PacketInfo{}, nil, ErrInvalidPacket
			}
			frames[i] = data[offset : offset+frameLen]
			offset += frameLen
		}
	case 2:
		frame1Len, bytesRead, err := parseFrameLength(data, 1)
		if err != nil {
			return PacketInfo{}, nil, err
		}
		headerLen := 1 + bytesRead
		if frame1Len != info.FrameSizes[0] {
			return PacketInfo{}, nil, ErrInvalidPacket
		}
		if headerLen+info.FrameSizes[0]+info.FrameSizes[1] > len(data) {
			return PacketInfo{}, nil, ErrInvalidPacket
		}
		frames[0] = data[headerLen : headerLen+info.FrameSizes[0]]
		frames[1] = data[headerLen+info.FrameSizes[0] : headerLen+info.FrameSizes[0]+info.FrameSizes[1]]
	case 3:
		if len(data) < 2 {
			return PacketInfo{}, nil, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0

		offset := 2
		padding := 0
		if hasPadding {
			for {
				if offset >= len(data) {
					return PacketInfo{}, nil, ErrPacketTooShort
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

		if vbr {
			for i := 0; i < info.FrameCount-1; i++ {
				_, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return PacketInfo{}, nil, err
				}
				offset += bytesRead
			}
		}

		frameOffset := offset
		frameDataEnd := len(data) - padding
		for i := 0; i < info.FrameCount; i++ {
			frameLen := info.FrameSizes[i]
			if frameLen < 0 || frameOffset+frameLen > frameDataEnd {
				return PacketInfo{}, nil, ErrInvalidPacket
			}
			frames[i] = data[frameOffset : frameOffset+frameLen]
			frameOffset += frameLen
		}
	default:
		return PacketInfo{}, nil, ErrInvalidPacket
	}

	return info, frames, nil
}

func buildRepacketizedPacket(tocBase byte, frames [][]byte, data []byte) (int, error) {
	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}

	if count == 1 {
		need := 1 + len(frames[0])
		if len(data) < need {
			return 0, ErrBufferTooSmall
		}
		data[0] = tocBase
		copy(data[1:], frames[0])
		return need, nil
	}

	if count == 2 {
		if len(frames[0]) == len(frames[1]) {
			need := 1 + len(frames[0]) + len(frames[1])
			if len(data) < need {
				return 0, ErrBufferTooSmall
			}
			data[0] = tocBase | 0x01
			offset := 1
			copy(data[offset:], frames[0])
			offset += len(frames[0])
			copy(data[offset:], frames[1])
			offset += len(frames[1])
			return offset, nil
		}

		len0 := len(frames[0])
		need := 1 + frameLengthBytes(len0) + len(frames[0]) + len(frames[1])
		if len(data) < need {
			return 0, ErrBufferTooSmall
		}
		data[0] = tocBase | 0x02
		offset := 1
		offset += encodeFrameLength(data[offset:], len0)
		copy(data[offset:], frames[0])
		offset += len(frames[0])
		copy(data[offset:], frames[1])
		offset += len(frames[1])
		return offset, nil
	}

	return buildCode3Packet(tocBase, frames, data, 0, false)
}

func buildCode3Packet(tocBase byte, frames [][]byte, data []byte, targetLen int, withPadding bool) (int, error) {
	count := len(frames)
	if count < 1 || count > maxRepacketizerFrames {
		return 0, ErrInvalidArgument
	}

	vbr := false
	for i := 1; i < count; i++ {
		if len(frames[i]) != len(frames[0]) {
			vbr = true
			break
		}
	}

	lengthBytes := 0
	if vbr {
		for i := 0; i < count-1; i++ {
			lengthBytes += frameLengthBytes(len(frames[i]))
		}
	}

	frameBytes := 0
	for _, frame := range frames {
		frameBytes += len(frame)
	}

	baseLen := 2 + lengthBytes + frameBytes
	need := baseLen
	paddingBytes := 0
	if withPadding {
		if targetLen < baseLen+1 {
			return 0, ErrBufferTooSmall
		}
		need = targetLen
		extra := targetLen - baseLen
		padFieldBytes := paddingLengthBytes(extra)
		if extra < padFieldBytes {
			return 0, ErrBufferTooSmall
		}
		paddingBytes = extra - padFieldBytes
	}

	if len(data) < need {
		return 0, ErrBufferTooSmall
	}

	offset := 0
	data[offset] = tocBase | 0x03
	offset++

	countByte := byte(count & 0x3F)
	if vbr {
		countByte |= 0x80
	}
	if withPadding {
		countByte |= 0x40
	}
	data[offset] = countByte
	offset++

	if withPadding {
		extra := need - baseLen
		offset += writePaddingLength(data[offset:], extra)
	}

	if vbr {
		for i := 0; i < count-1; i++ {
			offset += encodeFrameLength(data[offset:], len(frames[i]))
		}
	}

	for _, frame := range frames {
		copy(data[offset:], frame)
		offset += len(frame)
	}

	for i := 0; i < paddingBytes; i++ {
		data[offset+i] = 0
	}
	offset += paddingBytes

	return offset, nil
}

func frameLengthBytes(size int) int {
	if size < 252 {
		return 1
	}
	return 2
}

func encodeFrameLength(dst []byte, size int) int {
	if size < 252 {
		dst[0] = byte(size)
		return 1
	}
	first := 252 + (size & 0x03)
	second := (size - first) / 4
	dst[0] = byte(first)
	dst[1] = byte(second)
	return 2
}

func paddingLengthBytes(extra int) int {
	if extra <= 0 {
		return 0
	}
	return (extra-1)/255 + 1
}

func writePaddingLength(dst []byte, extra int) int {
	w := 0
	remaining := extra
	for remaining > 255 {
		dst[w] = 255
		w++
		remaining -= 255
	}
	dst[w] = byte(remaining - 1)
	return w + 1
}
