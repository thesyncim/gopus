package ogg

import (
	"encoding/binary"
)

func expectedDemixingMatrixSize(channels, streams, coupled uint8) int {
	return 2 * int(channels) * int(streams+coupled)
}

func identityDemixingMatrix(channels, streams, coupled uint8) []byte {
	cols := int(streams + coupled)
	rows := int(channels)
	matrix := make([]byte, 2*rows*cols)

	for col := 0; col < cols; col++ {
		for row := 0; row < rows; row++ {
			var v uint16
			if row == col {
				v = 32767 // Q15 identity coefficient
			}
			offset := 2 * (col*rows + row)
			binary.LittleEndian.PutUint16(matrix[offset:offset+2], v)
		}
	}

	return matrix
}

// Opus header constants per RFC 7845.
const (
	// DefaultPreSkip is the standard Opus encoder lookahead at 48kHz.
	// This is the number of samples to discard at the beginning of decode.
	DefaultPreSkip = 312

	// opusHeadMagic is the magic signature for the OpusHead header.
	opusHeadMagic = "OpusHead"

	// opusTagsMagic is the magic signature for the OpusTags header.
	opusTagsMagic = "OpusTags"

	// opusHeadMinSize is the minimum size of an OpusHead packet (mapping family 0).
	opusHeadMinSize = 19

	// opusHeadVersion is the required version number for OpusHead.
	opusHeadVersion = 1
)

// MappingFamily values per RFC 7845.
const (
	// MappingFamilyRTP is for mono/stereo with implicit channel order (RTP).
	MappingFamilyRTP = 0

	// MappingFamilyVorbis is for 1-8 channels with Vorbis channel order.
	MappingFamilyVorbis = 1

	// MappingFamilyAmbisonics is for ambisonics ACN/SN3D channel mapping.
	MappingFamilyAmbisonics = 2

	// MappingFamilyProjection is for projection-based ambisonics mapping.
	MappingFamilyProjection = 3

	// MappingFamilyDiscrete is for N channels with no defined relationship.
	MappingFamilyDiscrete = 255
)

// OpusHead is the identification header for Opus in Ogg.
// This appears in the first Ogg page (BOS) and describes the stream format.
type OpusHead struct {
	// Version is the format version (must be 1).
	Version uint8

	// Channels is the output channel count (1-255).
	Channels uint8

	// PreSkip is the number of samples to discard at the start (at 48kHz).
	// Typically 312 for standard Opus encoder lookahead.
	PreSkip uint16

	// SampleRate is the original input sample rate (informational only).
	// Opus always operates at 48kHz internally.
	SampleRate uint32

	// OutputGain is the gain to apply in Q7.8 dB format.
	// Positive values amplify, negative values attenuate.
	OutputGain int16

	// MappingFamily specifies the channel mapping:
	//   0: Mono/stereo (implicit order)
	//   1: Surround 1-8 channels (Vorbis order)
	//   2: Ambisonics ACN/SN3D
	//   3: Projection-based ambisonics
	//   255: Discrete (no defined relationship)
	MappingFamily uint8

	// Extended fields for mapping family 1 and 255:

	// StreamCount is the number of Opus streams in the packet.
	StreamCount uint8

	// CoupledCount is the number of coupled (stereo) streams.
	CoupledCount uint8

	// ChannelMapping maps output channels to decoder channels.
	// For mapping family 0, this is implicit (not stored).
	// For family 1/2/255, length equals Channels.
	ChannelMapping []byte

	// DemixingMatrix stores RFC 8486 family-3 demixing metadata.
	// Size is 2*Channels*(StreamCount+CoupledCount) bytes in S16LE format.
	DemixingMatrix []byte
}

// Encode serializes the OpusHead to bytes.
// For mapping family 0: 19 bytes.
// For mapping family 1/255: 21 + Channels bytes.
func (h *OpusHead) Encode() []byte {
	if h.MappingFamily == 0 {
		// Mapping family 0: 19 bytes total.
		data := make([]byte, 19)
		copy(data[0:8], opusHeadMagic)
		data[8] = h.Version
		data[9] = h.Channels
		binary.LittleEndian.PutUint16(data[10:12], h.PreSkip)
		binary.LittleEndian.PutUint32(data[12:16], h.SampleRate)
		binary.LittleEndian.PutUint16(data[16:18], uint16(h.OutputGain))
		data[18] = h.MappingFamily
		return data
	}

	if h.MappingFamily == MappingFamilyProjection {
		matrix := h.DemixingMatrix
		if len(matrix) == 0 {
			matrix = identityDemixingMatrix(h.Channels, h.StreamCount, h.CoupledCount)
		}

		size := 21 + len(matrix)
		data := make([]byte, size)
		copy(data[0:8], opusHeadMagic)
		data[8] = h.Version
		data[9] = h.Channels
		binary.LittleEndian.PutUint16(data[10:12], h.PreSkip)
		binary.LittleEndian.PutUint32(data[12:16], h.SampleRate)
		binary.LittleEndian.PutUint16(data[16:18], uint16(h.OutputGain))
		data[18] = h.MappingFamily
		data[19] = h.StreamCount
		data[20] = h.CoupledCount
		copy(data[21:], matrix)
		return data
	}

	// Mapping family 1/2/255: 21 + Channels bytes.
	size := 21 + len(h.ChannelMapping)
	data := make([]byte, size)
	copy(data[0:8], opusHeadMagic)
	data[8] = h.Version
	data[9] = h.Channels
	binary.LittleEndian.PutUint16(data[10:12], h.PreSkip)
	binary.LittleEndian.PutUint32(data[12:16], h.SampleRate)
	binary.LittleEndian.PutUint16(data[16:18], uint16(h.OutputGain))
	data[18] = h.MappingFamily
	data[19] = h.StreamCount
	data[20] = h.CoupledCount
	copy(data[21:], h.ChannelMapping)
	return data
}

// ParseOpusHead parses an OpusHead from bytes.
// Returns ErrInvalidHeader if the data is malformed.
func ParseOpusHead(data []byte) (*OpusHead, error) {
	if len(data) < opusHeadMinSize {
		return nil, ErrInvalidHeader
	}

	// Verify magic signature.
	if string(data[0:8]) != opusHeadMagic {
		return nil, ErrInvalidHeader
	}

	// Verify version.
	version := data[8]
	if version != opusHeadVersion {
		return nil, ErrInvalidHeader
	}

	h := &OpusHead{
		Version:       version,
		Channels:      data[9],
		PreSkip:       binary.LittleEndian.Uint16(data[10:12]),
		SampleRate:    binary.LittleEndian.Uint32(data[12:16]),
		OutputGain:    int16(binary.LittleEndian.Uint16(data[16:18])),
		MappingFamily: data[18],
	}

	// Validate channel count.
	if h.Channels == 0 {
		return nil, ErrInvalidHeader
	}

	// Parse extended fields for non-RTP mapping families.
	if h.MappingFamily != 0 {
		if len(data) < 21 {
			return nil, ErrInvalidHeader
		}

		h.StreamCount = data[19]
		h.CoupledCount = data[20]

		// Validate stream counts.
		if h.StreamCount == 0 {
			return nil, ErrInvalidHeader
		}
		if int(h.CoupledCount) > int(h.StreamCount) {
			return nil, ErrInvalidHeader
		}

		if h.MappingFamily == MappingFamilyProjection {
			matrixSize := expectedDemixingMatrixSize(h.Channels, h.StreamCount, h.CoupledCount)
			if len(data) < 21+matrixSize {
				return nil, ErrInvalidHeader
			}
			h.DemixingMatrix = make([]byte, matrixSize)
			copy(h.DemixingMatrix, data[21:21+matrixSize])
		} else {
			// Need at least 21 + Channels bytes.
			minSize := 21 + int(h.Channels)
			if len(data) < minSize {
				return nil, ErrInvalidHeader
			}

			// Parse channel mapping table.
			h.ChannelMapping = make([]byte, h.Channels)
			copy(h.ChannelMapping, data[21:21+int(h.Channels)])

			// Validate mapping values.
			maxStream := h.StreamCount + h.CoupledCount
			for _, m := range h.ChannelMapping {
				if m >= maxStream && m != 255 { // 255 = silence
					return nil, ErrInvalidHeader
				}
			}
		}
	} else {
		// Mapping family 0: implicit mapping.
		if h.Channels > 2 {
			return nil, ErrInvalidHeader
		}
		h.StreamCount = 1
		h.CoupledCount = 0
		if h.Channels == 2 {
			h.CoupledCount = 1
		}
	}

	return h, nil
}

// OpusTags is the comment header for Opus in Ogg.
// This appears in the second Ogg page and contains metadata.
type OpusTags struct {
	// Vendor is the encoder name (e.g., "gopus").
	Vendor string

	// Comments is a map of user comments (key=value pairs).
	// Common keys: TITLE, ARTIST, ALBUM, DATE, TRACKNUMBER, etc.
	Comments map[string]string
}

// Encode serializes the OpusTags to bytes.
func (t *OpusTags) Encode() []byte {
	// Calculate size.
	// 8 bytes: "OpusTags"
	// 4 bytes: vendor string length
	// N bytes: vendor string
	// 4 bytes: comment count
	// For each comment:
	//   4 bytes: comment length
	//   N bytes: comment string ("KEY=value")

	size := 8 + 4 + len(t.Vendor) + 4
	for k, v := range t.Comments {
		size += 4 + len(k) + 1 + len(v) // "KEY=value"
	}

	data := make([]byte, size)
	offset := 0

	// Write magic.
	copy(data[offset:offset+8], opusTagsMagic)
	offset += 8

	// Write vendor string.
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(t.Vendor)))
	offset += 4
	copy(data[offset:offset+len(t.Vendor)], t.Vendor)
	offset += len(t.Vendor)

	// Write comment count.
	binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(t.Comments)))
	offset += 4

	// Write comments.
	for k, v := range t.Comments {
		comment := k + "=" + v
		binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(comment)))
		offset += 4
		copy(data[offset:offset+len(comment)], comment)
		offset += len(comment)
	}

	return data
}

// ParseOpusTags parses an OpusTags from bytes.
// Returns ErrInvalidHeader if the data is malformed.
func ParseOpusTags(data []byte) (*OpusTags, error) {
	// Minimum size: 8 (magic) + 4 (vendor len) + 4 (comment count) = 16
	if len(data) < 16 {
		return nil, ErrInvalidHeader
	}

	// Verify magic signature.
	if string(data[0:8]) != opusTagsMagic {
		return nil, ErrInvalidHeader
	}

	offset := 8

	// Read vendor string length.
	vendorLen := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	if offset+int(vendorLen) > len(data) {
		return nil, ErrInvalidHeader
	}

	t := &OpusTags{
		Vendor:   string(data[offset : offset+int(vendorLen)]),
		Comments: make(map[string]string),
	}
	offset += int(vendorLen)

	// Read comment count.
	if offset+4 > len(data) {
		return nil, ErrInvalidHeader
	}
	commentCount := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	// Read comments.
	for i := uint32(0); i < commentCount; i++ {
		if offset+4 > len(data) {
			return nil, ErrInvalidHeader
		}
		commentLen := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		if offset+int(commentLen) > len(data) {
			return nil, ErrInvalidHeader
		}
		comment := string(data[offset : offset+int(commentLen)])
		offset += int(commentLen)

		// Split on first '=' to get key=value.
		for j := 0; j < len(comment); j++ {
			if comment[j] == '=' {
				key := comment[:j]
				value := comment[j+1:]
				t.Comments[key] = value
				break
			}
		}
	}

	return t, nil
}

// DefaultOpusHead returns an OpusHead with standard settings.
// sampleRate is the original input sample rate (informational).
// channels is 1 for mono, 2 for stereo.
func DefaultOpusHead(sampleRate uint32, channels uint8) *OpusHead {
	h := &OpusHead{
		Version:       opusHeadVersion,
		Channels:      channels,
		PreSkip:       DefaultPreSkip,
		SampleRate:    sampleRate,
		OutputGain:    0,
		MappingFamily: 0,
		StreamCount:   1,
		CoupledCount:  0,
	}
	if channels == 2 {
		h.CoupledCount = 1
	}
	return h
}

// DefaultOpusHeadMultistreamWithFamily returns an OpusHead for multistream mappings.
func DefaultOpusHeadMultistreamWithFamily(sampleRate uint32, channels uint8, mappingFamily, streams, coupled uint8, mapping []byte) *OpusHead {
	h := &OpusHead{
		Version:       opusHeadVersion,
		Channels:      channels,
		PreSkip:       DefaultPreSkip,
		SampleRate:    sampleRate,
		OutputGain:    0,
		MappingFamily: mappingFamily,
		StreamCount:   streams,
		CoupledCount:  coupled,
	}
	if mappingFamily == MappingFamilyProjection {
		h.DemixingMatrix = identityDemixingMatrix(channels, streams, coupled)
	} else {
		h.ChannelMapping = mapping
	}
	return h
}

// DefaultOpusHeadMultistream returns an OpusHead for multistream with mapping family 1.
// This is for surround configurations (1-8 channels).
func DefaultOpusHeadMultistream(sampleRate uint32, channels uint8, streams, coupled uint8, mapping []byte) *OpusHead {
	return DefaultOpusHeadMultistreamWithFamily(sampleRate, channels, MappingFamilyVorbis, streams, coupled, mapping)
}

// DefaultOpusTags returns an OpusTags with gopus vendor string.
func DefaultOpusTags() *OpusTags {
	return &OpusTags{
		Vendor:   "gopus",
		Comments: make(map[string]string),
	}
}
