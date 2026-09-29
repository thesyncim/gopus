package ogg

import (
	"encoding/binary"
	"io"
	"math/rand"
	"time"
)

// WriterConfig configures a Writer.
type WriterConfig struct {
	// SampleRate records the original input rate in hertz. Ogg Opus granule
	// positions use 48 kHz sample units.
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

// oggPageScratchSize is the inline page-serialization buffer carried by each
// Writer. Audio pages (one Opus packet plus header and lacing) fit comfortably;
// only unusually large pages such as an OpusTags packet with many comments spill
// to a one-off heap buffer.
const oggPageScratchSize = 4096

// Writer writes Opus packets to an Ogg stream. It retains page and granule
// state and is not safe for concurrent use.
type Writer struct {
	w           io.Writer
	config      WriterConfig
	serial      uint32 // Random bitstream serial number
	pageSeq     uint32 // Page sequence counter
	granulePos  uint64 // Sample position (at 48kHz)
	headersDone bool   // Headers written?
	closed      bool   // Stream closed?

	// pageScratch is reused across writePage calls so steady-state writing
	// allocates nothing; it is part of the Writer's own allocation.
	pageScratch [oggPageScratchSize]byte
}

// NewWriter returns a Writer with the default mapping family for mono or
// stereo Opus. sampleRate is the original input rate in hertz and is
// informational; channels must be 1 or 2. It returns ErrNilWriter for a nil
// writer and ErrInvalidHeader for an unsupported channel count.
func NewWriter(w io.Writer, sampleRate uint32, channels uint8) (*Writer, error) {
	if w == nil {
		return nil, ErrNilWriter
	}
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

// NewWriterWithConfig returns a Writer configured by config and writes its
// OpusHead and OpusTags pages before returning. It supports mapping families 0,
// 1, 2, 3, and 255. Nonzero mapping families require a nonzero stream count,
// coupled streams no greater than streams, and at most 255 decoded stream
// channels. A family-3 demixing matrix must have 2*Channels*(StreamCount+
// CoupledCount) bytes when supplied.
func NewWriterWithConfig(w io.Writer, config WriterConfig) (*Writer, error) {
	if w == nil {
		return nil, ErrNilWriter
	}

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
		decodedChannels := decodedChannelCount(config.StreamCount, config.CoupledCount)
		if decodedChannels > maxDecodedChannelCount {
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
			maxStream := decodedChannels
			for _, m := range config.ChannelMapping {
				if int(m) >= maxStream && m != 255 { // 255 = silence
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
	// Lacing: ceil-style segment table for the payload. An EOS page with no
	// payload is emitted packetless (zero segments) — a zero-length lacing entry
	// would encode an empty packet, which strict demuxers reject for the EOS
	// marker page.
	numSegments := len(payload)/255 + 1
	if headerType&PageFlagEOS != 0 && len(payload) == 0 {
		numSegments = 0
	}

	total := pageHeaderSize + numSegments + len(payload)
	var buf []byte
	if total <= len(ow.pageScratch) {
		buf = ow.pageScratch[:total]
	} else {
		buf = make([]byte, total)
	}

	// Header pages (BOS flag set or before headersDone) have granule = 0.
	granulePos := ow.granulePos
	if headerType&PageFlagBOS != 0 || !ow.headersDone {
		granulePos = 0
	}

	copy(buf[0:4], oggMagic)
	buf[4] = 0 // stream structure version
	buf[5] = headerType
	binary.LittleEndian.PutUint64(buf[6:14], granulePos)
	binary.LittleEndian.PutUint32(buf[14:18], ow.serial)
	binary.LittleEndian.PutUint32(buf[18:22], ow.pageSeq)
	buf[22], buf[23], buf[24], buf[25] = 0, 0, 0, 0 // CRC, computed below
	buf[26] = byte(numSegments)

	si := pageHeaderSize
	for i := 0; i < numSegments-1; i++ {
		buf[si] = 255
		si++
	}
	if numSegments > 0 {
		buf[si] = byte(len(payload) % 255)
		si++
	}
	copy(buf[si:], payload)

	binary.LittleEndian.PutUint32(buf[22:26], oggCRC(buf))

	n, err := ow.w.Write(buf)
	if err != nil {
		return err
	}
	if n != len(buf) {
		return io.ErrShortWrite
	}

	ow.pageSeq++
	return nil
}

// WritePacket writes packet and advances the granule position by samples, the
// packet duration in samples per channel at 48 kHz (960 for a 20 ms frame).
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
	prevGranule := ow.granulePos
	ow.granulePos += uint64(samples)

	// Write audio page.
	// One packet per page (simple approach per RFC 7845 recommendation).
	if err := ow.writePage(packet, 0); err != nil {
		ow.granulePos = prevGranule
		return err
	}
	return nil
}

// Close writes an end-of-stream page and marks the Writer closed. It does not
// close the underlying io.Writer. A closed Writer cannot write more packets.
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
