// stream.go implements streaming io.Reader and io.Writer wrappers for Opus encoding/decoding.

package gopus

import (
	"encoding/binary"
	"io"
	"math"
)

// Streaming API
//
// The Reader and Writer types provide io.Reader and io.Writer interfaces
// for streaming Opus encode/decode operations. They handle frame boundaries
// internally, allowing integration with Go's standard io patterns.
//
// # Streaming Decode
//
// To decode a stream of Opus packets:
//
//	source := &MyPacketSource{} // implements PacketSource
//	reader, err := gopus.NewReader(48000, 2, source, gopus.FormatFloat32LE)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Read decoded PCM bytes
//	buf := make([]byte, 4096)
//	for {
//	    n, err := reader.Read(buf)
//	    if err == io.EOF {
//	        break
//	    }
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	    processAudio(buf[:n])
//	}
//
// # Streaming Encode
//
// To encode PCM audio to a stream of Opus packets:
//
//	sink := &MyPacketSink{} // implements PacketSink
//	writer, err := gopus.NewWriter(48000, 2, sink, gopus.FormatFloat32LE, gopus.ApplicationAudio)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Write PCM bytes
//	pcmBytes := getPCMData() // float32 little-endian bytes
//	_, err = writer.Write(pcmBytes)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Flush remaining buffered samples
//	if err := writer.Flush(); err != nil {
//	    log.Fatal(err)
//	}
//
// # Sample Format
//
// Both Reader and Writer support two sample formats:
//   - FormatFloat32LE: 32-bit float, little-endian (4 bytes per sample)
//   - FormatInt16LE: 16-bit signed integer, little-endian (2 bytes per sample)
//
// Samples are interleaved for stereo: [L0, R0, L1, R1, ...]

// SampleFormat specifies the PCM sample format for streaming.
type SampleFormat int

const (
	// FormatFloat32LE is 32-bit float, little-endian (4 bytes per sample).
	FormatFloat32LE SampleFormat = iota
	// FormatInt16LE is 16-bit signed integer, little-endian (2 bytes per sample).
	FormatInt16LE
)

// BytesPerSample returns the number of bytes per sample for the format.
func (f SampleFormat) BytesPerSample() int {
	switch f {
	case FormatFloat32LE:
		return 4
	case FormatInt16LE:
		return 2
	default:
		return 4
	}
}

// PacketSource provides Opus packets for streaming decode.
// Implementations should return io.EOF when no more packets are available.
type PacketSource interface {
	// NextPacket returns the next Opus packet.
	// Returns io.EOF when stream ends.
	// Returns nil packet for PLC (packet loss).
	NextPacket() ([]byte, error)
}

// PacketSink receives encoded Opus packets from streaming encode.
type PacketSink interface {
	// WritePacket writes an encoded Opus packet.
	// Returns number of bytes written and any error.
	WritePacket(packet []byte) (int, error)
}

// Reader decodes an Opus stream, implementing io.Reader.
// Output is PCM samples in the configured format.
//
// The Reader handles frame boundaries internally, buffering decoded
// PCM samples and serving byte-oriented reads.
//
// Example:
//
//	reader, err := gopus.NewReader(48000, 2, source, gopus.FormatFloat32LE)
//	io.Copy(audioOutput, reader)
type Reader struct {
	dec    *Decoder
	source PacketSource
	format SampleFormat // Output sample format

	pcmBuf  []float32 // Decoded PCM samples
	byteBuf []byte    // PCM as bytes
	offset  int       // Current read position in byteBuf

	eof bool // Source exhausted
}

// NewReader creates a streaming decoder.
//
// Parameters:
//   - sampleRate: output sample rate (8000, 12000, 16000, 24000, or 48000)
//   - channels: number of audio channels (1 or 2)
//   - source: provides Opus packets for decoding
//   - format: output sample format (FormatFloat32LE or FormatInt16LE)
func NewReader(sampleRate, channels int, source PacketSource, format SampleFormat) (*Reader, error) {
	dec, err := NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &Reader{
		dec:    dec,
		source: source,
		format: format,
		pcmBuf: make([]float32, 0),
		offset: 0,
		eof:    false,
	}, nil
}

// Read implements io.Reader, reading decoded PCM bytes.
//
// The Reader handles frame boundaries internally, fetching and decoding
// packets as needed to fill the buffer.
func (r *Reader) Read(p []byte) (int, error) {
	// If buffer is exhausted, try to get more data
	if r.offset >= len(r.byteBuf) {
		if r.eof {
			return 0, io.EOF
		}

		// Fetch next packet from source
		packet, err := r.source.NextPacket()
		if err == io.EOF {
			r.eof = true
			return 0, io.EOF
		}
		if err != nil {
			return 0, err
		}

		// Decode the packet (nil packet triggers PLC)
		samples, decErr := r.dec.DecodeFloat32(packet)
		if decErr != nil {
			return 0, decErr
		}

		// Convert PCM to bytes based on format
		r.byteBuf = r.pcmToBytes(samples)
		r.offset = 0
	}

	// Copy available bytes to p
	n := copy(p, r.byteBuf[r.offset:])
	r.offset += n

	return n, nil
}

// pcmToBytes converts float32 PCM samples to bytes based on the format.
func (r *Reader) pcmToBytes(samples []float32) []byte {
	switch r.format {
	case FormatFloat32LE:
		buf := make([]byte, len(samples)*4)
		for i, s := range samples {
			bits := math.Float32bits(s)
			binary.LittleEndian.PutUint32(buf[i*4:], bits)
		}
		return buf
	case FormatInt16LE:
		buf := make([]byte, len(samples)*2)
		for i, s := range samples {
			// Clamp and convert to int16
			scaled := s * 32767.0
			var v int16
			if scaled > 32767 {
				v = 32767
			} else if scaled < -32768 {
				v = -32768
			} else {
				v = int16(scaled)
			}
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
		}
		return buf
	default:
		// Default to float32
		buf := make([]byte, len(samples)*4)
		for i, s := range samples {
			bits := math.Float32bits(s)
			binary.LittleEndian.PutUint32(buf[i*4:], bits)
		}
		return buf
	}
}

// SampleRate returns the sample rate in Hz.
func (r *Reader) SampleRate() int {
	return r.dec.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (r *Reader) Channels() int {
	return r.dec.Channels()
}

// Reset clears buffers and decoder state for a new stream.
func (r *Reader) Reset() {
	r.dec.Reset()
	r.byteBuf = nil
	r.offset = 0
	r.eof = false
}

// Writer encodes PCM samples to an Opus stream, implementing io.Writer.
// Input is PCM samples in the configured format.
//
// The Writer buffers input samples until a complete frame is accumulated,
// then encodes and sends the packet to the sink.
//
// Example:
//
//	writer, err := gopus.NewWriter(48000, 2, sink, gopus.FormatFloat32LE, gopus.ApplicationAudio)
//	io.Copy(writer, audioInput)
//	writer.Flush() // encode any remaining buffered samples
type Writer struct {
	enc    *Encoder
	sink   PacketSink
	format SampleFormat // Input sample format

	sampleBuf  []byte // Buffered input bytes
	frameBytes int    // Bytes needed for one frame

	packetBuf []byte // Buffer for encoded packet (4000 bytes)
}

// NewWriter creates a streaming encoder.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000)
//   - channels: number of audio channels (1 or 2)
//   - sink: receives encoded Opus packets
//   - format: input sample format (FormatFloat32LE or FormatInt16LE)
//   - application: encoder application hint
func NewWriter(sampleRate, channels int, sink PacketSink, format SampleFormat, application Application) (*Writer, error) {
	enc, err := NewEncoder(sampleRate, channels, application)
	if err != nil {
		return nil, err
	}

	// Default frame size is 960 samples (20ms at 48kHz)
	frameSize := enc.FrameSize()
	bytesPerSample := format.BytesPerSample()
	frameBytes := frameSize * channels * bytesPerSample

	return &Writer{
		enc:        enc,
		sink:       sink,
		format:     format,
		sampleBuf:  make([]byte, 0, frameBytes*2), // Pre-allocate for 2 frames
		frameBytes: frameBytes,
		packetBuf:  make([]byte, 4000),
	}, nil
}

// Write implements io.Writer, encoding PCM bytes to Opus packets.
//
// The Writer buffers input samples until a complete frame is accumulated,
// then encodes and sends the packet to the sink.
func (w *Writer) Write(p []byte) (int, error) {
	// Append input to buffer
	w.sampleBuf = append(w.sampleBuf, p...)

	// Process complete frames
	for len(w.sampleBuf) >= w.frameBytes {
		// Extract one frame of bytes
		frameData := w.sampleBuf[:w.frameBytes]

		// Convert bytes to float32 PCM
		pcm := w.bytesToPCM(frameData)

		// Encode the frame
		n, err := w.enc.Encode(pcm, w.packetBuf)
		if err != nil {
			return 0, err
		}

		// If n > 0, send packet to sink (n == 0 means DTX suppressed)
		if n > 0 {
			_, err = w.sink.WritePacket(w.packetBuf[:n])
			if err != nil {
				return 0, err
			}
		}

		// Remove consumed bytes from buffer
		w.sampleBuf = w.sampleBuf[w.frameBytes:]
	}

	return len(p), nil
}

// bytesToPCM converts bytes to float32 PCM samples based on the format.
func (w *Writer) bytesToPCM(data []byte) []float32 {
	switch w.format {
	case FormatFloat32LE:
		numSamples := len(data) / 4
		pcm := make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			bits := binary.LittleEndian.Uint32(data[i*4:])
			pcm[i] = math.Float32frombits(bits)
		}
		return pcm
	case FormatInt16LE:
		numSamples := len(data) / 2
		pcm := make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			sample := int16(binary.LittleEndian.Uint16(data[i*2:]))
			pcm[i] = float32(sample) / 32768.0
		}
		return pcm
	default:
		// Default to float32
		numSamples := len(data) / 4
		pcm := make([]float32, numSamples)
		for i := 0; i < numSamples; i++ {
			bits := binary.LittleEndian.Uint32(data[i*4:])
			pcm[i] = math.Float32frombits(bits)
		}
		return pcm
	}
}

// Flush encodes any buffered samples.
// If samples don't fill a complete frame, they are zero-padded.
// Call Flush before closing the stream to ensure all audio is encoded.
func (w *Writer) Flush() error {
	if len(w.sampleBuf) == 0 {
		return nil
	}

	// Zero-pad to complete frame
	padded := make([]byte, w.frameBytes)
	copy(padded, w.sampleBuf)
	// Rest of padded is already zero

	// Convert bytes to float32 PCM
	pcm := w.bytesToPCM(padded)

	// Encode the frame
	n, err := w.enc.Encode(pcm, w.packetBuf)
	if err != nil {
		return err
	}

	// If n > 0, send packet to sink
	if n > 0 {
		_, err = w.sink.WritePacket(w.packetBuf[:n])
		if err != nil {
			return err
		}
	}

	// Clear buffer
	w.sampleBuf = w.sampleBuf[:0]

	return nil
}

// SetBitrate sets the target bitrate in bits per second.
// Valid range is 6000 to 510000 (6 kbps to 510 kbps).
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

// Reset clears buffers and encoder state for a new stream.
func (w *Writer) Reset() {
	w.enc.Reset()
	w.sampleBuf = w.sampleBuf[:0]
}

// SampleRate returns the sample rate in Hz.
func (w *Writer) SampleRate() int {
	return w.enc.SampleRate()
}

// Channels returns the number of audio channels (1 or 2).
func (w *Writer) Channels() int {
	return w.enc.Channels()
}
