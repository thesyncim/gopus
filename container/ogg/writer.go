package ogg

import (
	"io"
	"math/rand"
	"time"
)

// WriterConfig configures the OggWriter.
type WriterConfig struct {
	// SampleRate is the original input sample rate (informational only).
	// Opus always operates at 48kHz internally.
	SampleRate uint32

	// Channels is the output channel count (1-255).
	Channels uint8

	// PreSkip is the number of samples to discard at the start (at 48kHz).
	// Default is 312 for standard Opus encoder lookahead.
	PreSkip uint16

	// OutputGain is the gain to apply in Q7.8 dB format.
	// Positive values amplify, negative values attenuate.
	OutputGain int16

	// MappingFamily specifies the channel mapping:
	//   0: Mono/stereo (implicit order) - for 1-2 channels
	//   1: Surround 1-8 channels (Vorbis order)
	//   2: Ambisonics ACN/SN3D
	//   3: Projection-based ambisonics
	//   255: Discrete (no defined relationship)
	MappingFamily uint8

	// StreamCount is the number of Opus streams in the packet (for non-RTP mappings).
	StreamCount uint8

	// CoupledCount is the number of coupled (stereo) streams (for non-RTP mappings).
	CoupledCount uint8

	// ChannelMapping maps output channels to decoder channels (for family 1/2/255).
	ChannelMapping []byte

	// DemixingMatrix stores RFC 8486 family-3 demixing metadata.
	// If empty for family 3, libopus default projection matrices are emitted
	// when (channels,streams,coupled) matches a valid projection layout;
	// otherwise an identity matrix is emitted.
	DemixingMatrix []byte
}

// Writer writes Opus packets to an Ogg container.
// Files created by Writer are playable by standard players (VLC, FFmpeg, browsers).
type Writer struct {
	w           io.Writer
	config      WriterConfig
	serial      uint32 // Random bitstream serial number
	pageSeq     uint32 // Page sequence counter
	granulePos  uint64 // Sample position (at 48kHz)
	headersDone bool   // Headers written?
	closed      bool   // Stream closed?
}

// NewWriter creates a new OggWriter with default configuration.
// sampleRate is the original input sample rate (informational only).
// channels is 1 for mono or 2 for stereo.
// Returns an error if channels is 0 or greater than 2 (use NewWriterWithConfig for multistream).
func NewWriter(w io.Writer, sampleRate uint32, channels uint8) (*Writer, error) {
	if channels == 0 || channels > 2 {
		return nil, ErrInvalidHeader
	}

	config := WriterConfig{
		SampleRate:    sampleRate,
		Channels:      channels,
		PreSkip:       DefaultPreSkip,
		OutputGain:    0,
		MappingFamily: MappingFamilyRTP,
		StreamCount:   1,
		CoupledCount:  0,
	}

	if channels == 2 {
		config.CoupledCount = 1
	}

	return NewWriterWithConfig(w, config)
}

// NewWriterWithConfig creates a new OggWriter with explicit configuration.
// This supports all multistream mapping families (1/2/3/255).
func NewWriterWithConfig(w io.Writer, config WriterConfig) (*Writer, error) {
	// Validate config.
	if config.Channels == 0 {
		return nil, ErrInvalidHeader
	}

	// Validate mapping family 0 constraints.
	if config.MappingFamily == 0 && config.Channels > 2 {
		return nil, ErrInvalidHeader
	}

	// Validate non-RTP multistream requirements.
	if config.MappingFamily != 0 {
		if config.StreamCount == 0 {
			return nil, ErrInvalidHeader
		}
		if int(config.CoupledCount) > int(config.StreamCount) {
			return nil, ErrInvalidHeader
		}

		if config.MappingFamily == MappingFamilyProjection {
			expected := expectedDemixingMatrixSize(config.Channels, config.StreamCount, config.CoupledCount)
			if len(config.DemixingMatrix) == 0 {
				if matrix, gain, ok := defaultProjectionDemixingMatrix(config.Channels, config.StreamCount, config.CoupledCount); ok {
					config.DemixingMatrix = matrix
					if config.OutputGain == 0 {
						config.OutputGain = gain
					}
				} else {
					config.DemixingMatrix = identityDemixingMatrix(config.Channels, config.StreamCount, config.CoupledCount)
				}
			} else if len(config.DemixingMatrix) != expected {
				return nil, ErrInvalidHeader
			}
		} else {
			if len(config.ChannelMapping) != int(config.Channels) {
				return nil, ErrInvalidHeader
			}
			// Validate mapping values.
			maxStream := config.StreamCount + config.CoupledCount
			for _, m := range config.ChannelMapping {
				if m >= maxStream && m != 255 { // 255 = silence
					return nil, ErrInvalidHeader
				}
			}
		}
	}

	// Set defaults.
	if config.PreSkip == 0 {
		config.PreSkip = DefaultPreSkip
	}

	// Generate random serial number.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	serial := rng.Uint32()

	ow := &Writer{
		w:      w,
		config: config,
		serial: serial,
	}

	// Write headers immediately.
	if err := ow.writeHeaders(); err != nil {
		return nil, err
	}

	return ow, nil
}

// writeHeaders writes the OpusHead (BOS page) and OpusTags pages.
func (ow *Writer) writeHeaders() error {
	if ow.headersDone {
		return nil
	}

	// Create OpusHead.
	var head *OpusHead
	if ow.config.MappingFamily == 0 {
		head = DefaultOpusHead(ow.config.SampleRate, ow.config.Channels)
		head.PreSkip = ow.config.PreSkip
		head.OutputGain = ow.config.OutputGain
	} else {
		head = DefaultOpusHeadMultistreamWithFamily(
			ow.config.SampleRate,
			ow.config.Channels,
			ow.config.MappingFamily,
			ow.config.StreamCount,
			ow.config.CoupledCount,
			ow.config.ChannelMapping,
		)
		if ow.config.MappingFamily == MappingFamilyProjection && len(ow.config.DemixingMatrix) > 0 {
			head.DemixingMatrix = ow.config.DemixingMatrix
		}
		head.PreSkip = ow.config.PreSkip
		head.OutputGain = ow.config.OutputGain
	}

	headPayload := head.Encode()

	// Write BOS page with OpusHead.
	// Header pages MUST have granulePos = 0.
	if err := ow.writePage(headPayload, PageFlagBOS); err != nil {
		return err
	}

	// Create and write OpusTags page.
	tags := DefaultOpusTags()
	tagsPayload := tags.Encode()

	// Tags page is normal (not BOS, not EOS), granulePos = 0.
	if err := ow.writePage(tagsPayload, 0); err != nil {
		return err
	}

	ow.headersDone = true
	return nil
}

// writePage writes a single Ogg page.
// For header pages, granulePos is always 0.
// For audio pages, granulePos is the current granule position.
func (ow *Writer) writePage(payload []byte, headerType byte) error {
	page := &Page{
		Version:      0,
		HeaderType:   headerType,
		SerialNumber: ow.serial,
		PageSequence: ow.pageSeq,
		Segments:     BuildSegmentTable(len(payload)),
		Payload:      payload,
	}

	// Set granule position.
	// Header pages (BOS flag set or before headersDone) have granule = 0.
	if headerType&PageFlagBOS != 0 || !ow.headersDone {
		page.GranulePos = 0
	} else {
		page.GranulePos = ow.granulePos
	}

	encoded := page.Encode()
	if _, err := ow.w.Write(encoded); err != nil {
		return err
	}

	ow.pageSeq++
	return nil
}

// WritePacket writes an Opus packet to the stream.
// samples is the number of PCM samples at 48kHz represented by this packet
// (typically 960 for 20ms frames).
// Updates the granule position accordingly.
func (ow *Writer) WritePacket(packet []byte, samples int) error {
	if ow.closed {
		return ErrUnexpectedEOS
	}

	if !ow.headersDone {
		if err := ow.writeHeaders(); err != nil {
			return err
		}
	}

	// Update granule position BEFORE writing.
	// RFC 7845: The granule position represents the total number of samples
	// that could be decoded from all packets completed on this page.
	ow.granulePos += uint64(samples)

	// Write audio page.
	// One packet per page (simple approach per RFC 7845 recommendation).
	return ow.writePage(packet, 0)
}

// Close writes the EOS page and marks the stream as closed.
// The writer should not be used after Close.
func (ow *Writer) Close() error {
	if ow.closed {
		return nil
	}

	// Write empty EOS page.
	if err := ow.writePage(nil, PageFlagEOS); err != nil {
		return err
	}

	ow.closed = true
	return nil
}

// Serial returns the bitstream serial number.
func (ow *Writer) Serial() uint32 {
	return ow.serial
}

// GranulePos returns the current granule position (samples at 48kHz).
func (ow *Writer) GranulePos() uint64 {
	return ow.granulePos
}

// PageCount returns the number of pages written so far.
func (ow *Writer) PageCount() uint32 {
	return ow.pageSeq
}
