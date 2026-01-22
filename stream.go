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
