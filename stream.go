// stream.go implements streaming io.Reader and io.WriteCloser wrappers for Opus encoding/decoding.

package gopus

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"unsafe"
)

// hostIsLittleEndian reports whether the running host stores multibyte values
// little-endian. Detected once at init via a uint16 byte view; on LE hosts the
// streaming Reader can copy decoded PCM straight to its output buffer instead
// of re-encoding each sample.
var hostIsLittleEndian = func() bool {
	probe := [2]byte{1, 0}
	return binary.NativeEndian.Uint16(probe[:]) == 1
}()

// float32SliceAsBytes returns a read-only byte view of s on little-endian
// hosts.
//
// Safety: float32 is a 4-byte scalar with no pointers or padding; the returned
// slice shares s's backing array and preserves its exact byte bounds
// (4*len(s)). Callers only copy out of it while s remains live.
func float32SliceAsBytes(s []float32) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(s))), len(s)*4)
}

// int16SliceAsBytes returns a read-only byte view of s on little-endian hosts.
//
// Safety: int16 is a 2-byte scalar with no pointers or padding; the returned
// slice shares s's backing array and preserves its exact byte bounds
// (2*len(s)). Callers only copy out of it while s remains live.
func int16SliceAsBytes(s []int16) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(s))), len(s)*2)
}

// SampleFormat specifies the PCM sample format for streaming.
type SampleFormat int

const (
	// FormatFloat32LE is 32-bit float, little-endian (4 bytes per sample).
	FormatFloat32LE SampleFormat = iota
	// FormatInt16LE is 16-bit signed integer, little-endian (2 bytes per sample).
	FormatInt16LE
)

func validSampleFormat(format SampleFormat) bool {
	switch format {
	case FormatFloat32LE, FormatInt16LE:
		return true
	default:
		return false
	}
}

// BytesPerSample returns the number of bytes per sample for the format.
func (f SampleFormat) BytesPerSample() int {
	switch f {
	case FormatFloat32LE:
		return 4
	case FormatInt16LE:
		return 2
	default:
		return 0
	}
}

// PacketReader provides Opus packets for streaming decode.
// ReadPacketInto writes the next packet into dst and returns its byte length;
// callers may reuse dst after the call. Return io.EOF when the stream ends.
type PacketReader interface {
	// ReadPacketInto writes the next packet into dst and returns its byte length.
	// The caller may reuse dst after this call returns.
	//
	// granulePos is the source position in decoded samples at 48 kHz when the
	// container provides one, as with an Ogg Opus granule position. Return 0
	// when positions are unavailable.
	//
	// Return io.EOF when the stream ends. A final complete packet may be
	// returned with n > 0 and io.EOF; Reader drains its PCM before returning EOF.
	// Return n=0, err=nil to request packet loss concealment for one frame.
	ReadPacketInto(dst []byte) (n int, granulePos uint64, err error)
}

// PacketSink receives encoded Opus packets from streaming encode.
type PacketSink interface {
	// WritePacket writes one encoded Opus packet and returns the number of bytes
	// accepted. Writer does not retry; a short count returns io.ErrShortWrite,
	// except that an error returned with zero bytes is preserved. The packet buffer
	// is reused after this method returns, so copy packet if retaining it.
	//
	// If the sink also implements io.Closer, Writer.Close calls it at most once
	// after attempting a flush, or after an earlier sink write error.
	WritePacket(packet []byte) (int, error)
}

// Reader decodes packets from a PacketReader and exposes PCM as an io.Reader.
// Read returns little-endian samples in the configured format. A Reader is not
// safe for concurrent use.
type Reader struct {
	dec    *Decoder
	source PacketReader
	format SampleFormat // Output sample format

	packetBuf      []byte
	pcmFloat       []float32 // Decoded PCM samples
	pcmInt16       []int16
	byteBuf        []byte // PCM as bytes
	offset         int    // Current read position in byteBuf
	lastGranulePos uint64 // Most recent packet position reported by the source

	eof bool // Source exhausted
}

// NewReader creates a Reader that decodes packets from source into format.
// It returns ErrNilPacketReader for a nil source, ErrInvalidSampleFormat for
// an unsupported format, or the decoder configuration error.
func NewReader(cfg DecoderConfig, source PacketReader, format SampleFormat) (*Reader, error) {
	if source == nil {
		return nil, ErrNilPacketReader
	}
	if !validSampleFormat(format) {
		return nil, ErrInvalidSampleFormat
	}

	dec, err := NewDecoder(cfg)
	if err != nil {
		return nil, err
	}

	r := &Reader{
		dec:       dec,
		source:    source,
		format:    format,
		packetBuf: make([]byte, dec.maxPacketBytes),
		offset:    0,
		eof:       false,
	}
	maxSamples := dec.maxPacketSamples * int(dec.channels)
	switch format {
	case FormatFloat32LE:
		r.pcmFloat = make([]float32, maxSamples)
	case FormatInt16LE:
		r.pcmInt16 = make([]int16, maxSamples)
	}
	return r, nil
}

// Read implements io.Reader and returns decoded PCM bytes in the format passed
// to NewReader. Each call decodes at most one packet, so a short read at a packet
// boundary is normal. It returns io.EOF after the source ends and buffered PCM
// has been consumed. An empty destination does not advance the source or
// buffered PCM; it returns io.EOF only when EOF is already known and no PCM
// remains.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		if r.eof && r.offset >= len(r.byteBuf) {
			return 0, io.EOF
		}
		return 0, nil
	}

	// If buffer is exhausted, try to get more data
	if r.offset >= len(r.byteBuf) {
		if r.eof {
			return 0, io.EOF
		}

		// Fetch next packet from source
		nPacket, granulePos, err := r.source.ReadPacketInto(r.packetBuf)
		if err == io.EOF {
			r.eof = true
			if nPacket <= 0 {
				return 0, io.EOF
			}
		} else if err != nil {
			return 0, err
		}
		r.lastGranulePos = granulePos

		var packet []byte
		if nPacket > 0 {
			packet = r.packetBuf[:nPacket]
		}

		switch r.format {
		case FormatFloat32LE:
			nSamples, decErr := r.dec.Decode(packet, r.pcmFloat)
			if decErr != nil {
				return 0, decErr
			}
			nTotal := nSamples * int(r.dec.channels)
			byteLen := nTotal * 4
			if hostIsLittleEndian && len(p) >= byteLen {
				if nTotal > 0 {
					copy(p, float32SliceAsBytes(r.pcmFloat[:nTotal]))
				}
				return byteLen, nil
			}
			if cap(r.byteBuf) < byteLen {
				r.byteBuf = make([]byte, byteLen)
			}
			r.byteBuf = r.byteBuf[:byteLen]
			if hostIsLittleEndian && nTotal > 0 {
				// On LE hosts the in-memory float32 layout already matches
				// FormatFloat32LE on the wire; copy the bytes directly.
				copy(r.byteBuf, float32SliceAsBytes(r.pcmFloat[:nTotal]))
			} else {
				for i := range nTotal {
					bits := math.Float32bits(r.pcmFloat[i])
					binary.LittleEndian.PutUint32(r.byteBuf[i*4:], bits)
				}
			}
		case FormatInt16LE:
			nSamples, decErr := r.dec.DecodeInt16(packet, r.pcmInt16)
			if decErr != nil {
				return 0, decErr
			}
			nTotal := nSamples * int(r.dec.channels)
			byteLen := nTotal * 2
			if hostIsLittleEndian && len(p) >= byteLen {
				if nTotal > 0 {
					copy(p, int16SliceAsBytes(r.pcmInt16[:nTotal]))
				}
				return byteLen, nil
			}
			if cap(r.byteBuf) < byteLen {
				r.byteBuf = make([]byte, byteLen)
			}
			r.byteBuf = r.byteBuf[:byteLen]
			if hostIsLittleEndian && nTotal > 0 {
				copy(r.byteBuf, int16SliceAsBytes(r.pcmInt16[:nTotal]))
			} else {
				for i := range nTotal {
					binary.LittleEndian.PutUint16(r.byteBuf[i*2:], uint16(r.pcmInt16[i]))
				}
			}
		default:
			return 0, ErrInvalidSampleFormat
		}

		r.offset = 0
	}

	// Copy available bytes to p
	n := copy(p, r.byteBuf[r.offset:])
	r.offset += n

	return n, nil
}

// SampleRate returns the sample rate in Hz.
func (r *Reader) SampleRate() int {
	return r.dec.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (r *Reader) Channels() int {
	return r.dec.Channels()
}

// LastGranulePos returns the most recent packet position reported by the source.
//
// For Ogg Opus this is the computed granule position of that packet.
// Sources that do not track positions may leave this at 0.
func (r *Reader) LastGranulePos() uint64 {
	return r.lastGranulePos
}

// Reset clears buffered PCM and decoder state. It does not reset or replace the
// PacketReader, so subsequent reads continue from that source's current position.
func (r *Reader) Reset() {
	r.dec.Reset()
	if r.byteBuf != nil {
		r.byteBuf = r.byteBuf[:0]
	}
	r.offset = 0
	r.lastGranulePos = 0
	r.eof = false
}

// Writer encodes PCM bytes from io.Writer calls into Opus packets for a
// PacketSink. It buffers incomplete frames and is not safe for concurrent use.
type Writer struct {
	enc    *Encoder
	sink   PacketSink
	format SampleFormat // Input sample format

	sampleBuf    []byte // Buffered input bytes
	frameBytes   int    // Bytes needed for one frame
	frameSamples int    // Samples per frame across all channels

	packetBuf          []byte    // Buffer for encoded packet (4000 bytes)
	pcmScratch         []float32 // Reused PCM scratch for byte-to-sample conversion
	paddedBuf          []byte    // Reused zero-padded frame buffer for Flush
	closed             bool      // Write and Flush reject calls after close or a sink write error.
	sinkCloseAttempted bool
}

// NewWriter creates a Writer that encodes interleaved PCM at sampleRate with
// channels and sends packets to sink. format selects little-endian float32 or
// signed int16 input, and application selects the encoder's intended use. The
// writer uses a 20 ms frame duration by default. It returns an error for a nil
// sink, unsupported format, or invalid encoder configuration.
func NewWriter(sampleRate, channels int, sink PacketSink, format SampleFormat, application Application) (*Writer, error) {
	if sink == nil {
		return nil, ErrNilPacketSink
	}
	if !validSampleFormat(format) {
		return nil, ErrInvalidSampleFormat
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: application})
	if err != nil {
		return nil, err
	}

	// Default frame size is 960 samples (20ms at 48kHz)
	frameSize := enc.FrameSize()
	bytesPerSample := format.BytesPerSample()
	frameBytes := frameSize * channels * bytesPerSample
	frameSamples := frameSize * channels

	return &Writer{
		enc:          enc,
		sink:         sink,
		format:       format,
		sampleBuf:    make([]byte, 0, frameBytes),
		frameBytes:   frameBytes,
		frameSamples: frameSamples,
		packetBuf:    make([]byte, 4000),
		pcmScratch:   make([]float32, frameSamples),
		paddedBuf:    make([]byte, frameBytes),
	}, nil
}

// Write implements io.Writer for interleaved little-endian PCM in the format
// passed to NewWriter. It buffers incomplete frames and encodes each complete
// frame. On success it consumes all of p. On error,
// the returned count covers only input bytes in frames handled before the error;
// packets sent before an error are not rolled back. A sink error closes the
// Writer.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	initialBuffered := len(w.sampleBuf)
	processedBytes := 0
	in := p

	// Complete a buffered partial frame first without copying all of p.
	if initialBuffered > 0 {
		need := w.frameBytes - initialBuffered
		if len(in) < need {
			w.sampleBuf = append(w.sampleBuf, in...)
			return len(p), nil
		}
		w.sampleBuf = append(w.sampleBuf, in[:need]...)
		n, err := w.encodeFrame(w.sampleBuf[:w.frameBytes])
		if err != nil {
			w.sampleBuf = w.sampleBuf[:initialBuffered]
			return 0, err
		}
		if n > 0 {
			if err := w.writePacketToSink(w.packetBuf[:n]); err != nil {
				w.closed = true
				w.sampleBuf = w.sampleBuf[:initialBuffered]
				return 0, err
			}
		}
		w.sampleBuf = w.sampleBuf[:0]
		processedBytes += w.frameBytes
		in = in[need:]
	}

	// Encode complete frames directly from p so sampleBuf never grows beyond one frame.
	for len(in) >= w.frameBytes {
		n, err := w.encodeFrame(in[:w.frameBytes])
		if err != nil {
			return consumedInputBytes(initialBuffered, processedBytes, len(p)), err
		}
		if n > 0 {
			if err := w.writePacketToSink(w.packetBuf[:n]); err != nil {
				w.closed = true
				return consumedInputBytes(initialBuffered, processedBytes, len(p)), err
			}
		}
		processedBytes += w.frameBytes
		in = in[w.frameBytes:]
	}

	if len(in) > 0 {
		w.sampleBuf = append(w.sampleBuf, in...)
	}
	return len(p), nil
}

func consumedInputBytes(initialBuffered, processedBytes, incoming int) int {
	if processedBytes <= initialBuffered {
		return 0
	}
	consumed := processedBytes - initialBuffered
	if consumed > incoming {
		return incoming
	}
	return consumed
}

func (w *Writer) writePacketToSink(packet []byte) error {
	n, err := w.sink.WritePacket(packet)
	if err != nil {
		if n > 0 {
			return io.ErrShortWrite
		}
		return err
	}
	if n != len(packet) {
		return io.ErrShortWrite
	}
	return nil
}

func (w *Writer) encodeFrame(data []byte) (int, error) {
	pcm := w.pcmScratch[:w.frameSamples]
	w.decodePCMInto(pcm, data)
	if w.format == FormatInt16LE {
		// Match opus_encode's short-input analysis and 16-bit precision cap.
		return w.enc.encodeInt16Packet(pcm, w.packetBuf)
	}
	return w.enc.Encode(pcm, w.packetBuf)
}

// decodePCMInto converts bytes to float32 PCM samples using caller-provided scratch.
func (w *Writer) decodePCMInto(dst []float32, data []byte) {
	switch w.format {
	case FormatFloat32LE:
		for i := range dst {
			bits := binary.LittleEndian.Uint32(data[i*4:])
			dst[i] = math.Float32frombits(bits)
		}
	case FormatInt16LE:
		for i := range dst {
			sample := int16(binary.LittleEndian.Uint16(data[i*2:]))
			dst[i] = float32(sample) / 32768.0
		}
	}
}

// Flush encodes buffered PCM. A partial final frame is zero-padded to the
// configured frame size. With no buffered PCM,
// Flush has no effect.
func (w *Writer) Flush() error {
	if w.closed {
		return io.ErrClosedPipe
	}
	if len(w.sampleBuf) == 0 {
		return nil
	}

	// Zero-pad to complete frame using reusable scratch.
	clear(w.paddedBuf)
	copy(w.paddedBuf, w.sampleBuf)
	n, err := w.encodeFrame(w.paddedBuf)
	if err != nil {
		return err
	}

	if n > 0 {
		if err := w.writePacketToSink(w.packetBuf[:n]); err != nil {
			w.closed = true
			return err
		}
	}

	// Clear buffer
	w.sampleBuf = w.sampleBuf[:0]

	return nil
}

// Close attempts to flush buffered samples and closes the underlying sink when
// supported, including when flushing fails. If both flushing and closing fail,
// the returned error matches both. Repeated calls return nil and do not retry
// the sink close.
func (w *Writer) Close() error {
	if w.sinkCloseAttempted {
		return nil
	}
	var flushErr error
	if !w.closed {
		flushErr = w.Flush()
	}
	w.closed = true
	w.sinkCloseAttempted = true
	var closeErr error
	if closer, ok := w.sink.(io.Closer); ok {
		closeErr = closer.Close()
	}
	if flushErr == nil {
		return closeErr
	}
	if closeErr == nil {
		return flushErr
	}
	return errors.Join(flushErr, closeErr)
}

// SetBitrate sets the target bitrate in bits per second. Positive values are
// clamped to the encoder's supported range. BitrateAuto and BitrateMax select
// automatic and output-buffer-limited bitrates; other nonpositive values return
// ErrInvalidBitrate.
func (w *Writer) SetBitrate(bitrate int) error {
	return w.enc.SetBitrate(bitrate)
}

// SetComplexity sets the encoder's computational complexity (0-10).
func (w *Writer) SetComplexity(complexity int) error {
	return w.enc.SetComplexity(complexity)
}

// SetFEC enables or disables in-band Forward Error Correction.
func (w *Writer) SetFEC(enabled bool) {
	w.enc.SetFEC(enabled)
}

// SetDTX enables or disables Discontinuous Transmission.
func (w *Writer) SetDTX(enabled bool) {
	w.enc.SetDTX(enabled)
}

// Reset clears buffered PCM and encoder state and clears the closed flag. It
// retains the PacketSink without resetting or reopening it, so the sink must be
// reusable if the Writer is reused.
func (w *Writer) Reset() {
	w.enc.Reset()
	w.sampleBuf = w.sampleBuf[:0]
	w.closed = false
	w.sinkCloseAttempted = false
}

// SampleRate returns the sample rate in Hz.
func (w *Writer) SampleRate() int {
	return w.enc.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (w *Writer) Channels() int {
	return w.enc.Channels()
}
