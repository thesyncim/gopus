package ogg

import (
	"encoding/binary"
	"strings"
)

const maxDecodedChannelCount = 255

const maxOpusTagsSize = 125_829_120 // RFC 7845 §5.2 permits rejecting larger comment headers.

func opusTagsSizeWithinLimit(currentSize, additionalSize int) bool {
	return currentSize >= 0 && additionalSize >= 0 && currentSize <= maxOpusTagsSize &&
		additionalSize <= maxOpusTagsSize-currentSize
}

func decodedChannelCount(streams, coupled uint8) int {
	return int(streams) + int(coupled)
}

// expectedDemixingMatrixSize returns the byte length of a mapping-family-3
// demixing matrix for the given layout. The matrix has
// channels*(streams+coupled) S16LE coefficients, each two bytes. The stream
// and coupled counts are widened to int before being summed so the addition
// cannot wrap a uint8 and yield a short matrix size for malformed headers.
func expectedDemixingMatrixSize(channels, streams, coupled uint8) int {
	return 2 * int(channels) * decodedChannelCount(streams, coupled)
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

	// opusHeadVersion is the version number emitted by this package.
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

// OpusHead stores the identification header fields for an Ogg Opus stream.
type OpusHead struct {
	// Version is the format version. This package emits 1 and parses compatible
	// minor versions 0 through 15.
	Version uint8

	// Channels is the output channel count (1-255).
	Channels uint8

	// PreSkip is the number of samples to discard at the start (at 48kHz).
	// Typically 312 for standard Opus encoder lookahead.
	PreSkip uint16

	// SampleRate records the original input rate in hertz. Ogg Opus granule
	// positions use 48 kHz sample units.
	SampleRate uint32

	// OutputGain is the gain to apply in Q7.8 dB format.
	// Positive values amplify, negative values attenuate.
	OutputGain int16

	// MappingFamily identifies the channel mapping format. Defined values are
	// MappingFamilyRTP, MappingFamilyVorbis, MappingFamilyAmbisonics,
	// MappingFamilyProjection, and MappingFamilyDiscrete.
	MappingFamily uint8

	// Nonzero mapping families store StreamCount and CoupledCount. Families
	// other than 3 also store ChannelMapping.

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

	// ExtraData stores opaque trailing fields from a compatible minor version.
	// ParseOpusHead preserves these bytes so Encode does not discard extensions.
	ExtraData []byte
}

// Encode returns a serialized copy of the OpusHead. It copies mapping and
// demixing bytes into the result but does not validate the fields or their
// consistency; ParseOpusHead validates serialized headers.
func (h *OpusHead) Encode() []byte {
	if h.MappingFamily == 0 {
		data := make([]byte, opusHeadMinSize+len(h.ExtraData))
		copy(data[0:8], opusHeadMagic)
		data[8] = h.Version
		data[9] = h.Channels
		binary.LittleEndian.PutUint16(data[10:12], h.PreSkip)
		binary.LittleEndian.PutUint32(data[12:16], h.SampleRate)
		binary.LittleEndian.PutUint16(data[16:18], uint16(h.OutputGain))
		data[18] = h.MappingFamily
		copy(data[opusHeadMinSize:], h.ExtraData)
		return data
	}

	if h.MappingFamily == MappingFamilyProjection {
		matrix := h.DemixingMatrix
		if len(matrix) == 0 {
			if defaultMatrix, _, ok := defaultProjectionDemixingMatrix(h.Channels, h.StreamCount, h.CoupledCount); ok {
				matrix = defaultMatrix
			} else {
				matrix = identityDemixingMatrix(h.Channels, h.StreamCount, h.CoupledCount)
			}
		}

		size := 21 + len(matrix) + len(h.ExtraData)
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
		copy(data[21+len(matrix):], h.ExtraData)
		return data
	}

	// Mapping families with a channel table: 21 + Channels bytes.
	size := 21 + len(h.ChannelMapping) + len(h.ExtraData)
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
	copy(data[21+len(h.ChannelMapping):], h.ExtraData)
	return data
}

// ParseOpusHead parses an OpusHead identification header (RFC 7845 §5.1) from
// data. The returned header owns copies of its channel-mapping table and
// demixing matrix, so data may be reused afterwards.
//
// It returns ErrInvalidHeader when data is too short, lacks the "OpusHead"
// magic, uses an incompatible version, has a zero channel count, or carries
// missing, truncated, or inconsistent fields. Compatible minor versions 0
// through 15 are accepted; versions 0 and 1 require the known header length,
// while later minor versions may append extension data. Checks include coupled
// streams exceeding streams, more than 255 decoded stream channels, mapping
// indices outside the decoded streams, more than two channels for mapping
// family 0, more than eight channels for family 1, and a truncated RFC 8486
// family-3 demixing matrix. For nonzero mapping families other than 3, it parses
// the generic channel-mapping layout and preserves the family byte; it does not
// reject unknown family numbers.
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
	if version > 15 {
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
		decodedChannels := decodedChannelCount(h.StreamCount, h.CoupledCount)
		if decodedChannels > maxDecodedChannelCount {
			return nil, ErrInvalidHeader
		}

		if h.MappingFamily == MappingFamilyProjection {
			matrixSize := expectedDemixingMatrixSize(h.Channels, h.StreamCount, h.CoupledCount)
			matrixEnd := 21 + matrixSize
			if len(data) < matrixEnd {
				return nil, ErrInvalidHeader
			}
			h.DemixingMatrix = make([]byte, matrixSize)
			copy(h.DemixingMatrix, data[21:matrixEnd])
		} else {
			if h.MappingFamily == MappingFamilyVorbis && h.Channels > 8 {
				return nil, ErrInvalidHeader
			}
			// Need at least 21 + Channels bytes.
			minSize := 21 + int(h.Channels)
			if len(data) < minSize {
				return nil, ErrInvalidHeader
			}

			// Parse channel mapping table.
			h.ChannelMapping = make([]byte, h.Channels)
			copy(h.ChannelMapping, data[21:21+int(h.Channels)])

			// Validate mapping values.
			maxStream := decodedChannels
			for _, m := range h.ChannelMapping {
				if int(m) >= maxStream && m != 255 { // 255 = silence
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

	knownSize := opusHeadMinSize
	if h.MappingFamily != 0 {
		if h.MappingFamily == MappingFamilyProjection {
			knownSize = 21 + expectedDemixingMatrixSize(h.Channels, h.StreamCount, h.CoupledCount)
		} else {
			knownSize += 2 + int(h.Channels)
		}
	}
	if len(data) < knownSize || (version <= opusHeadVersion && len(data) != knownSize) {
		return nil, ErrInvalidHeader
	}
	if len(data) > knownSize {
		h.ExtraData = append([]byte(nil), data[knownSize:]...)
	}

	return h, nil
}

// OpusTags is the comment header for Opus in Ogg.
type OpusTags struct {
	// Vendor is the encoder name (e.g., "gopus").
	Vendor string

	// Comments contains raw comment vectors, usually NAME=value, in wire order.
	// It preserves duplicate names, original name casing, and entries without '='.
	Comments []string

	// ExtraData contains opaque bytes after the declared comment vectors.
	// ParseOpusTags preserves this trailing data so Encode retains it.
	ExtraData []byte
}

// Encode returns a serialized copy of the OpusTags without validating comment
// syntax.
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
	for _, comment := range t.Comments {
		size += 4 + len(comment)
	}
	size += len(t.ExtraData)

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
	for _, comment := range t.Comments {
		binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(len(comment)))
		offset += 4
		copy(data[offset:offset+len(comment)], comment)
		offset += len(comment)
	}
	copy(data[offset:], t.ExtraData)

	return data
}

// ParseOpusTags parses an OpusTags comment header (RFC 7845 §5.2, Vorbis
// comment layout) from data. The vendor string and comments are copied into
// the returned struct, so data may be reused afterwards.
//
// Comments preserve each raw vector, including duplicate names, original name
// casing, and entries without '='. The unsigned 32-bit lengths are bounds-
// checked against the remaining input, so an over-long vendor or comment
// length yields ErrInvalidHeader rather than reading past the buffer. Opaque
// trailing data is preserved in ExtraData.
//
// It returns ErrInvalidHeader when data is too short, lacks the "OpusTags"
// magic, or declares a vendor, comment count, or comment length that extends
// past the end of data, or exceeds this package's 120 MiB comment-header bound.
// RFC 7845 permits unspecified trailing data after the declared comments;
// ParseOpusTags preserves it without interpreting it.
func ParseOpusTags(data []byte) (*OpusTags, error) {
	if !opusTagsSizeWithinLimit(0, len(data)) {
		return nil, ErrInvalidHeader
	}
	// Minimum size: 8 (magic) + 4 (vendor len) + 4 (comment count) = 16
	if len(data) < 16 {
		return nil, ErrInvalidHeader
	}

	// Verify magic signature.
	if string(data[0:8]) != opusTagsMagic {
		return nil, ErrInvalidHeader
	}

	offset := 8

	// Read vendor string length. The length fields are unsigned 32-bit and may
	// exceed the buffer, so every bound is checked against the bytes remaining
	// after offset. The comparison uses uint64 to stay overflow-safe even where
	// int is 32 bits, where int(0xFFFFFFFF) would wrap to a negative value and
	// silently pass an offset+length check before slicing.
	vendorLen := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4

	if uint64(vendorLen) > uint64(len(data)-offset) {
		return nil, ErrInvalidHeader
	}

	t := &OpusTags{
		Vendor: string(data[offset : offset+int(vendorLen)]),
	}
	offset += int(vendorLen)

	// Read comment count.
	if offset+4 > len(data) {
		return nil, ErrInvalidHeader
	}
	commentCount := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4
	if uint64(commentCount) > uint64(len(data)-offset)/4 {
		return nil, ErrInvalidHeader
	}
	t.Comments = make([]string, 0, int(commentCount))

	// Read comments.
	for range commentCount {
		if offset+4 > len(data) {
			return nil, ErrInvalidHeader
		}
		commentLen := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		if uint64(commentLen) > uint64(len(data)-offset) {
			return nil, ErrInvalidHeader
		}
		comment := string(data[offset : offset+int(commentLen)])
		offset += int(commentLen)

		t.Comments = append(t.Comments, comment)
	}
	if offset < len(data) {
		t.ExtraData = append([]byte(nil), data[offset:]...)
	}

	return t, nil
}

// Value returns the first value for tag, comparing field names as ASCII
// case-insensitive strings. The returned value is the text after the first '='.
func (t *OpusTags) Value(tag string) (string, bool) {
	if t == nil || tag == "" {
		return "", false
	}
	for _, comment := range t.Comments {
		if i := strings.IndexByte(comment, '='); i > 0 && equalTagName(comment[:i], tag) {
			return comment[i+1:], true
		}
	}
	return "", false
}

// Values returns all values for tag in wire order, comparing field names as
// ASCII case-insensitive strings.
func (t *OpusTags) Values(tag string) []string {
	if t == nil || tag == "" {
		return nil
	}
	var values []string
	for _, comment := range t.Comments {
		if i := strings.IndexByte(comment, '='); i > 0 && equalTagName(comment[:i], tag) {
			values = append(values, comment[i+1:])
		}
	}
	return values
}

func equalTagName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ac, bc := a[i], b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 'a' - 'A'
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 'a' - 'A'
		}
		if ac != bc {
			return false
		}
	}
	return true
}

// DefaultOpusHead returns a mapping-family-0 header with the standard pre-skip.
// sampleRate is the original input rate in hertz; channels is 1 for mono or 2
// for stereo.
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

// DefaultOpusHeadMultistreamWithFamily returns a header with the supplied
// multistream fields. For mapping family 3 it selects the default projection
// matrix when available, otherwise an identity matrix. For other families,
// ChannelMapping aliases mapping; the function does not copy that slice.
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
		if matrix, gain, ok := defaultProjectionDemixingMatrix(channels, streams, coupled); ok {
			h.DemixingMatrix = matrix
			h.OutputGain = gain
		} else {
			h.DemixingMatrix = identityDemixingMatrix(channels, streams, coupled)
		}
	} else {
		h.ChannelMapping = mapping
	}
	return h
}

// DefaultOpusHeadMultistream returns a mapping-family-1 header for a surround
// layout. ChannelMapping aliases mapping; the function does not copy that slice.
func DefaultOpusHeadMultistream(sampleRate uint32, channels uint8, streams, coupled uint8, mapping []byte) *OpusHead {
	return DefaultOpusHeadMultistreamWithFamily(sampleRate, channels, MappingFamilyVorbis, streams, coupled, mapping)
}

// DefaultOpusTags returns tags with the vendor set to "gopus" and no comments.
func DefaultOpusTags() *OpusTags {
	return &OpusTags{
		Vendor: "gopus",
	}
}
