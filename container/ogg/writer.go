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

// oggPageScratchSize covers typical audio pages without a separate allocation.
const oggPageScratchSize = 4096

// maxPagePayload is the largest payload represented by 255 lacing entries.
// A packet this large needs another page for its terminating lacing entry.
const maxPagePayload = 255 * 255

// Writer writes Opus packets to an Ogg stream, retaining page and granule state.
// It writes directly to the supplied io.Writer without buffering and is not
// safe for concurrent use. Close writes the EOS page but does not flush or close
// the underlying writer.
type Writer struct {
	w           io.Writer
	config      WriterConfig
	serial      uint32 // Random bitstream serial number
	pageSeq     uint32 // Page sequence counter
	granulePos  uint64 // Sample position (at 48kHz)
	headersDone bool   // Headers written?
	closed      bool   // Stream closed?

	pageScratch [oggPageScratchSize]byte // Inline buffer for typical pages
	largePage   []byte                   // Reused storage for larger pages
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
// OpusHead and OpusTags pages before returning. Mapping family 0 permits one or
// two channels. Nonzero families require a nonzero stream count, no more coupled
// streams than streams, and at most 255 decoded stream channels. Nonzero
// families other than 3 require a channel-mapping entry per output channel.
// Family 3 accepts a demixing matrix of 2*Channels*(StreamCount+CoupledCount)
// bytes, or emits a default projection matrix when available and an identity
// matrix otherwise. The encoded OpusHead must fit on a single Ogg page;
// an oversized header returns ErrInvalidHeader.
// Errors from the underlying writer are returned; a failed write may already
// have written part of a header page.
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

// writePage writes a packet across consecutive pages. Pages without a completed
// packet have granule position -1 (RFC 7845 section 4); the final page carries
// the packet's granule position. OpusHead must fit entirely on its BOS page.
func (ow *Writer) writePage(payload []byte, headerType byte) error {
	if headerType&PageFlagBOS != 0 && len(payload) >= maxPagePayload {
		return ErrInvalidHeader
	}
	granulePos := ow.granulePos
	if headerType&PageFlagBOS != 0 || !ow.headersDone {
		granulePos = 0
	}
	for len(payload) >= maxPagePayload {
		if err := ow.writePageChunk(payload[:maxPagePayload], headerType&^PageFlagEOS, ^uint64(0), false); err != nil {
			return err
		}
		payload = payload[maxPagePayload:]
		headerType = headerType&^PageFlagBOS | PageFlagContinuation
	}
	return ow.writePageChunk(payload, headerType, granulePos, true)
}

// writePageChunk serializes one page. complete adds the packet terminator;
// an empty standalone EOS page has no packet or lacing entries.
func (ow *Writer) writePageChunk(payload []byte, headerType byte, granulePos uint64, complete bool) error {
	numSegments := len(payload) / 255
	if complete {
		numSegments++
	}
	if headerType&PageFlagEOS != 0 && headerType&PageFlagContinuation == 0 && len(payload) == 0 {
		numSegments = 0
	}

	total := pageHeaderSize + numSegments + len(payload)
	var buf []byte
	if total <= len(ow.pageScratch) {
		buf = ow.pageScratch[:total]
	} else {
		if cap(ow.largePage) < total {
			ow.largePage = make([]byte, total)
		}
		buf = ow.largePage[:total]
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
		buf[si] = 255
		if complete {
			buf[si] = byte(len(payload) % 255)
		}
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

// WritePacket writes packet across one or more audio pages and advances the
// granule position by samples, the nonnegative packet duration in samples per
// channel at 48 kHz (960 for a 20 ms frame). It does not parse packet or validate
// samples, so callers must supply a valid nonnegative duration. On a page-write
// error the granule position is restored, but the underlying writer may have
// received earlier pages or part of a page. A short write without an error is
// reported as io.ErrShortWrite. Calling WritePacket after Close or
// WriteFinalPacket returns ErrUnexpectedEOS.
func (ow *Writer) WritePacket(packet []byte, samples int) error {
	return ow.writeAudioPacket(packet, samples, 0)
}

// WriteFinalPacket writes packet across one or more audio pages, sets the EOS
// flag on its final page, and advances the granule position by samples. samples
// is the nonnegative contribution of this packet to the stream's final granule
// position in samples per channel at 48 kHz; callers use it to exclude any
// encoder padding at the end of the stream. The final granule is the previous
// granule position plus samples, as permitted for end trimming by RFC 7845
// Section 4.4. The Writer does not parse packet or validate samples.
// Intermediate pages for a large packet have an unknown granule position and
// do not carry EOS. A successful write closes the Writer, so a later Close is
// a no-op.
//
// On a page-write error the granule position is restored and the Writer remains
// open, but the underlying writer may have received earlier pages or part of a
// page. A short write without an error is reported as io.ErrShortWrite.
func (ow *Writer) WriteFinalPacket(packet []byte, samples int) error {
	return ow.writeAudioPacket(packet, samples, PageFlagEOS)
}

func (ow *Writer) writeAudioPacket(packet []byte, samples int, headerType byte) error {
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

	// Each packet starts on a fresh page; large packets use continuation pages.
	if err := ow.writePage(packet, headerType); err != nil {
		ow.granulePos = prevGranule
		return err
	}
	if headerType&PageFlagEOS != 0 {
		ow.closed = true
	}
	return nil
}

// Close writes a packetless end-of-stream page and marks the Writer closed,
// unless WriteFinalPacket has already ended the stream. It does not flush or
// close the underlying io.Writer; callers must flush or close any wrapped
// buffered writer themselves. A successful repeated Close is a no-op. If
// writing the EOS page fails, Close returns the error and leaves the Writer
// open, although the sink may already contain part of that page.
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

// PageCount returns the number of pages successfully written in full. A page
// that returns a write error is not counted, even if the underlying writer
// accepted some of its bytes.
func (ow *Writer) PageCount() uint32 {
	return ow.pageSeq
}
