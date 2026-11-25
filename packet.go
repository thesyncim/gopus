package gopus

import (
	"errors"
	"github.com/thesyncim/gopus/types"
)

type Mode = types.Mode

// Bandwidth is an alias for types.Bandwidth representing the audio bandwidth.
type Bandwidth = types.Bandwidth

// Opus coding modes, re-exported from the types package. Each TOC configuration
// (0-31) maps to exactly one mode per RFC 6716 Section 3.1.
const (
	// ModeSILK is the SILK-only mode, used for speech (TOC configs 0-11).
	ModeSILK = types.ModeSILK
	// ModeHybrid is the hybrid SILK+CELT mode, used for wideband speech and
	// music (TOC configs 12-15).
	ModeHybrid = types.ModeHybrid
	// ModeCELT is the CELT-only mode, used for music and low delay (TOC configs
	// 16-31).
	ModeCELT = types.ModeCELT
)

// Opus audio bandwidths, re-exported from the types package. The bandwidth sets
// the upper edge of the coded spectrum; each is paired with a nominal internal
// sample rate.
const (
	// BandwidthNarrowband covers audio up to 4 kHz (8 kHz internal rate).
	BandwidthNarrowband = types.BandwidthNarrowband
	// BandwidthMediumband covers audio up to 6 kHz (12 kHz internal rate).
	BandwidthMediumband = types.BandwidthMediumband
	// BandwidthWideband covers audio up to 8 kHz (16 kHz internal rate).
	BandwidthWideband = types.BandwidthWideband
	// BandwidthSuperwideband covers audio up to 12 kHz (24 kHz internal rate).
	BandwidthSuperwideband = types.BandwidthSuperwideband
	// BandwidthFullband covers audio up to 20 kHz (48 kHz internal rate).
	BandwidthFullband = types.BandwidthFullband
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

// GenerateTOC packs a configuration, channel flag, and frame-count code into
// a TOC byte.
// config contributes its low five bits, and frameCode contributes its low two bits:
//
//	0: 1 frame
//	1: 2 equal-sized frames
//	2: 2 different-sized frames
//	3: frame count is encoded in the packet
//
// The function masks these fields; it does not validate their values.
func GenerateTOC(config uint8, stereo bool, frameCode uint8) byte {
	toc := (config & 0x1F) << 3
	if stereo {
		toc |= 0x04
	}
	toc |= frameCode & 0x03
	return toc
}

// ConfigFromParams returns the TOC configuration index for mode, bandwidth,
// and frameSize.
// frameSize is measured in samples at 48 kHz. It returns -1 when the combination
// does not appear in the Opus configuration table.
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

// ParseTOC decodes the configuration, channel flag, and frame-count code in b.
// Every byte value has a TOC interpretation; the returned FrameSize is in
// samples at 48 kHz.
func ParseTOC(b byte) TOC {
	return tocTable[b]
}

func packetSamplesPerFrameAtRate(data []byte, sampleRate int) (int, error) {
	if len(data) < 1 {
		return 0, ErrPacketTooShort
	}
	return packetTOCSamplesPerFrameAtRate(data[0], sampleRate), nil
}

func packetTOCSamplesPerFrameAtRate(toc byte, sampleRate int) int {
	if toc&0x80 != 0 {
		audioSize := int((toc >> 3) & 0x3)
		return (sampleRate << audioSize) / 400
	}
	if toc&0x60 == 0x60 {
		if toc&0x08 != 0 {
			return sampleRate / 50
		}
		return sampleRate / 100
	}
	audioSize := int((toc >> 3) & 0x3)
	if audioSize == 3 {
		return sampleRate * 60 / 1000
	}
	return (sampleRate << audioSize) / 100
}

func packetFrameCountLibopus(data []byte) (int, error) {
	if len(data) < 1 {
		return 0, ErrPacketTooShort
	}
	count := data[0] & 0x3
	if count == 0 {
		return 1, nil
	}
	if count != 3 {
		return 2, nil
	}
	if len(data) < 2 {
		return 0, ErrPacketTooShort
	}
	return int(data[1] & 0x3F), nil
}

func packetSamplesAtRate(data []byte, sampleRate int) (int, error) {
	count, err := packetFrameCountLibopus(data)
	if err != nil {
		return 0, err
	}
	frameSize, err := packetSamplesPerFrameAtRate(data, sampleRate)
	if err != nil {
		return 0, err
	}
	samples := count * frameSize
	if samples*25 > sampleRate*3 {
		return 0, ErrInvalidPacket
	}
	return samples, nil
}

const maxOpusFrameBytes = 1275

// Errors returned by packet parsing functions.
var (
	// ErrPacketTooShort indicates the packet is missing bytes required to parse
	// its TOC, frame-count, length, or padding fields.
	ErrPacketTooShort = errors.New("gopus: packet too short")
	// ErrInvalidFrameCount indicates a code 3 packet declared a frame count
	// outside the valid 1-48 range (RFC 6716 Section 3.2.5).
	ErrInvalidFrameCount = errors.New("gopus: invalid frame count (M > 48)")
	// ErrInvalidPacket indicates the packet violates the RFC 6716 framing rules,
	// for example inconsistent frame lengths or a total duration above 120 ms.
	ErrInvalidPacket = errors.New("gopus: invalid packet structure")
)

// PacketInfo describes an Opus packet's TOC and frame layout.
type PacketInfo struct {
	TOC        TOC   // Parsed TOC byte.
	FrameCount int   // Number of frames in the packet.
	FrameSizes []int // Frame payload sizes in bytes, excluding headers and padding.
	Padding    int   // Trailing padding bytes; nonzero only for code 3 packets.
	TotalSize  int   // Complete packet size in bytes, including headers and padding.
}

// ParsePacket parses a complete Opus packet and returns its frame layout.
// FrameSizes contains payload lengths in packet order; packet headers and
// trailing padding are reported separately. It returns an error for truncated,
// malformed, overlong, or over-duration packets.
func ParsePacket(data []byte) (PacketInfo, error) {
	return parsePacketInto(data, nil)
}

// parsePacketInto shares framing validation between ParsePacket and the
// repacketizer. A supplied scratch array receives frame lengths; with no
// scratch, it allocates FrameSizes at the same validated points as ParsePacket.
func parsePacketInto(data []byte, scratch *[maxRepacketizerFrames]int) (PacketInfo, error) {
	if len(data) < 1 {
		return PacketInfo{}, ErrPacketTooShort
	}

	toc := ParseTOC(data[0])
	info := PacketInfo{TOC: toc, TotalSize: len(data)}

	switch toc.FrameCode {
	case 0:
		// Code 0: One frame
		if len(data)-1 > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 1
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0] = len(data) - 1

	case 1:
		// Code 1: Two equal-sized frames
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 {
			return PacketInfo{}, ErrInvalidPacket
		}
		frameSize := frameDataLen / 2
		if frameSize > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 2
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0], info.FrameSizes[1] = frameSize, frameSize

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
		if frame2Len > maxOpusFrameBytes {
			return PacketInfo{}, ErrInvalidPacket
		}
		info.FrameCount = 2
		info.FrameSizes = packetFrameSizes(scratch, info.FrameCount)
		info.FrameSizes[0], info.FrameSizes[1] = frame1Len, frame2Len

	case 3:
		// Code 3: Arbitrary number of frames
		if len(data) < 2 {
			return PacketInfo{}, ErrPacketTooShort
		}
		frameCountByte := data[1]
		vbr := (frameCountByte & 0x80) != 0
		hasPadding := (frameCountByte & 0x40) != 0
		frameCount := int(frameCountByte & 0x3F)

		if frameCount == 0 || frameCount > maxRepacketizerFrames {
			return PacketInfo{}, ErrInvalidFrameCount
		}
		if toc.FrameSize*frameCount > maxRepacketizerDuration48k {
			return PacketInfo{}, ErrInvalidPacket
		}

		offset := 2
		padding := 0

		// Parse padding if present
		if hasPadding {
			var err error
			offset, padding, err = parseCode3Padding(data, offset)
			if err != nil {
				return PacketInfo{}, err
			}
		}
		info.FrameCount = frameCount
		info.FrameSizes = packetFrameSizes(scratch, frameCount)
		info.Padding = padding

		if vbr {
			// VBR: Parse each frame length (except last)
			totalFrameLen := 0
			for i := 0; i < frameCount-1; i++ {
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
			if lastFrameLen > maxOpusFrameBytes {
				return PacketInfo{}, ErrInvalidPacket
			}
			info.FrameSizes[frameCount-1] = lastFrameLen
		} else {
			// CBR: Parse single frame length, all frames are same size
			// For CBR, no frame lengths are encoded. All frames share the
			// remaining bytes (minus padding) equally.
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			if frameDataLen%frameCount != 0 {
				return PacketInfo{}, ErrInvalidPacket
			}
			frameLen := frameDataLen / frameCount
			if frameLen > maxOpusFrameBytes {
				return PacketInfo{}, ErrInvalidPacket
			}
			for i := range frameCount {
				info.FrameSizes[i] = frameLen
			}
		}
	}

	return info, nil
}

func packetFrameSizes(scratch *[maxRepacketizerFrames]int, frameCount int) []int {
	if scratch == nil {
		return make([]int, frameCount)
	}
	return scratch[:frameCount]
}

// validatePacketFraming checks the same packet-size constraints as ParsePacket
// without materializing a FrameSizes slice. DecodeWithFEC only needs to reject
// malformed framing before it changes decoder state.
func validatePacketFraming(data []byte) error {
	toc, count, err := packetFrameCount(data)
	if err != nil {
		return err
	}
	switch toc.FrameCode {
	case 0:
		if len(data)-1 > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 1:
		frameDataLen := len(data) - 1
		if frameDataLen%2 != 0 || frameDataLen/2 > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 2:
		if len(data) < 2 {
			return ErrPacketTooShort
		}
		first, headerBytes, err := parseFrameLength(data, 1)
		if err != nil {
			return err
		}
		last := len(data) - 1 - headerBytes - first
		if last < 0 || last > maxOpusFrameBytes {
			return ErrInvalidPacket
		}
	case 3:
		vbr := data[1]&0x80 != 0
		hasPadding := data[1]&0x40 != 0
		offset := 2
		padding := 0
		if hasPadding {
			var err error
			offset, padding, err = parseCode3Padding(data, offset)
			if err != nil {
				return err
			}
		}
		if vbr {
			total := 0
			for range count - 1 {
				n, bytesRead, err := parseFrameLength(data, offset)
				if err != nil {
					return err
				}
				total += n
				offset += bytesRead
			}
			last := len(data) - offset - padding - total
			if last < 0 || last > maxOpusFrameBytes {
				return ErrInvalidPacket
			}
		} else {
			frameDataLen := len(data) - offset - padding
			if frameDataLen < 0 || frameDataLen%count != 0 || frameDataLen/count > maxOpusFrameBytes {
				return ErrInvalidPacket
			}
		}
	}
	return nil
}

// parseCode3Padding parses RFC 6716 Section 3.2.5 code-3 padding length bytes.
func parseCode3Padding(data []byte, offset int) (int, int, error) {
	padding := 0
	for {
		if offset >= len(data) {
			return 0, 0, ErrPacketTooShort
		}
		padByte := int(data[offset])
		offset++
		if padByte == 255 {
			if offset >= len(data) {
				return 0, 0, ErrPacketTooShort
			}
			padding += 254
			if offset+padding > len(data) {
				return 0, 0, ErrInvalidPacket
			}
		} else {
			padding += padByte
			if offset+padding > len(data) {
				return 0, 0, ErrInvalidPacket
			}
			return offset, padding, nil
		}
	}
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

func lastMultistreamPacketOffset(src []byte, length, numStreams int) (int, error) {
	offset := 0
	for s := 0; s < numStreams-1; s++ {
		_, _, consumed, err := parseSelfDelimitedPacket(src[offset:length])
		if err != nil {
			return 0, err
		}
		offset += consumed
	}
	if offset >= length {
		return 0, ErrInvalidPacket
	}
	return offset, nil
}

func decodeMultistreamPacket(src []byte, srcOffset, length int, selfDelimited bool) ([]byte, int, error) {
	if selfDelimited {
		return decodeSelfDelimitedPacket(src[srcOffset:length])
	}
	if srcOffset >= length {
		return nil, 0, ErrInvalidPacket
	}
	return src[srcOffset:length], length - srcOffset, nil
}

// MultistreamPacketPad pads the final stream packet in a multistream packet
// in place.
// length is the current packet length in bytes, newLen is the target length in
// bytes, and numStreams is the number of constituent Opus streams. The input
// slice must contain length bytes and have capacity for newLen bytes. If its
// original length is shorter, reslice the caller's slice to newLen after success.
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

	offset, err := lastMultistreamPacketOffset(data, length, numStreams)
	if err != nil {
		return err
	}

	data = data[:newLen]
	lastOldLen := length - offset
	lastNewLen := lastOldLen + (newLen - length)
	return PacketPad(data[offset:], lastOldLen, lastNewLen)
}

// MultistreamPacketUnpad removes padding from every stream in a multistream packet
// in place and returns the new packet length in bytes. length is the number of
// input bytes and numStreams is the number of constituent Opus streams; all but
// the final stream packet must use self-delimited framing. data must contain
// length bytes. The function does not extend data, so len(data) must also be
// large enough for the rebuilt output.
func MultistreamPacketUnpad(data []byte, length, numStreams int) (int, error) {
	if numStreams < 1 || length < 1 || length > len(data) {
		return 0, ErrInvalidArgument
	}

	src := make([]byte, length)
	copy(src, data[:length])

	srcOffset := 0
	dstOffset := 0
	for s := range numStreams {
		selfDelimited := s < numStreams-1

		packet, consumed, err := decodeMultistreamPacket(src, srcOffset, length, selfDelimited)
		if err != nil {
			return 0, err
		}
		srcOffset += consumed

		newPacketLen, err := PacketUnpad(packet, len(packet))
		if err != nil {
			return 0, err
		}

		if selfDelimited {
			selfDelimitedPacket, err := makeSelfDelimitedPacket(packet[:newPacketLen])
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

		copy(data[dstOffset:], packet[:newPacketLen])
		dstOffset += newPacketLen
	}

	return dstOffset, nil
}
