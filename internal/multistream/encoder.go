// Multistream encoder implementation for Opus surround sound.
// This file contains the Encoder struct and NewEncoder function for encoding
// multi-channel audio into multistream Opus packets.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1

package multistream

import (
	"errors"
	"fmt"

	"gopus/internal/encoder"
)

// ErrInvalidInput indicates the input samples have incorrect length.
var ErrInvalidInput = errors.New("multistream: invalid input length")

// Encoder encodes multi-channel audio into Opus multistream packets.
// Each elementary stream is encoded independently using a Phase 8 unified Encoder,
// then combined with self-delimiting framing per RFC 6716 Appendix B.
//
// Multistream packets are used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 7845 Section 5.1.1
type Encoder struct {
	// sampleRate is the input sample rate (8000, 12000, 16000, 24000, or 48000 Hz).
	sampleRate int

	// inputChannels is the total number of input channels (1-255).
	inputChannels int

	// streams is the total number of elementary streams (N).
	streams int

	// coupledStreams is the number of coupled (stereo) streams (M).
	// The first M encoders produce stereo output, the remaining N-M produce mono.
	coupledStreams int

	// mapping is the channel mapping table.
	// mapping[i] indicates which stream channel receives input channel i.
	// Values 0 to 2*M-1 are for coupled streams (even=left, odd=right).
	// Values 2*M to N+M-1 are for uncoupled streams.
	// Value 255 indicates a silent input channel (ignored).
	mapping []byte

	// encoders contains one encoder per stream.
	// First M encoders are stereo (for coupled streams).
	// Remaining N-M encoders are mono (for uncoupled streams).
	encoders []*encoder.Encoder

	// bitrate is the total bitrate in bits per second, distributed across streams.
	bitrate int
}

// NewEncoder creates a new multistream encoder.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how input audio is routed to stream encoders:
//   - Values 0 to 2*M-1: to coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: to uncoupled (mono) streams
//   - Value 255: silent channel (input ignored)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Input 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Input 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Input 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Input 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Input 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Input 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*Encoder, error) {
	// Validation exactly mirrors decoder
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if streams+coupledStreams > 255 {
		return nil, ErrTooManyChannels
	}
	if len(mapping) != channels {
		return nil, ErrInvalidMapping
	}

	// Validate each mapping entry
	maxMappingValue := streams + coupledStreams
	for i, m := range mapping {
		if m != 255 && int(m) >= maxMappingValue {
			return nil, fmt.Errorf("%w: mapping[%d]=%d exceeds maximum %d", ErrInvalidMapping, i, m, maxMappingValue-1)
		}
	}

	// Create stream encoders
	// First M encoders are stereo (coupled), remaining N-M are mono
	encoders := make([]*encoder.Encoder, streams)
	for i := 0; i < streams; i++ {
		var chans int
		if i < coupledStreams {
			chans = 2 // Coupled stream = stereo
		} else {
			chans = 1 // Uncoupled stream = mono
		}
		encoders[i] = encoder.NewEncoder(sampleRate, chans)
	}

	// Copy mapping to avoid external mutation
	mappingCopy := make([]byte, len(mapping))
	copy(mappingCopy, mapping)

	return &Encoder{
		sampleRate:     sampleRate,
		inputChannels:  channels,
		streams:        streams,
		coupledStreams: coupledStreams,
		mapping:        mappingCopy,
		encoders:       encoders,
		bitrate:        256000, // Default 256 kbps total
	}, nil
}

// NewEncoderDefault creates a multistream encoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (5 streams, 2 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewEncoderDefault(sampleRate, channels int) (*Encoder, error) {
	streams, coupledStreams, mapping, err := DefaultMapping(channels)
	if err != nil {
		return nil, err
	}
	return NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
}

// Reset clears all encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	for _, enc := range e.encoders {
		enc.Reset()
	}
}

// Channels returns the total number of input channels.
func (e *Encoder) Channels() int {
	return e.inputChannels
}

// SampleRate returns the input sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// Streams returns the total number of elementary streams.
func (e *Encoder) Streams() int {
	return e.streams
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (e *Encoder) CoupledStreams() int {
	return e.coupledStreams
}

// SetBitrate sets the total bitrate in bits per second.
// The bitrate is distributed across streams with coupled streams getting
// proportionally more bits than mono streams.
//
// Distribution formula:
//   - Coupled streams: 3 units (e.g., 96 kbps at typical settings)
//   - Mono streams: 2 units (e.g., 64 kbps at typical settings)
func (e *Encoder) SetBitrate(totalBitrate int) {
	e.bitrate = totalBitrate

	// Calculate per-stream allocation
	// Coupled streams get more bits (stereo benefit)
	monoStreams := e.streams - e.coupledStreams
	totalUnits := e.coupledStreams*3 + monoStreams*2

	if totalUnits == 0 {
		return
	}

	unitBitrate := totalBitrate / totalUnits

	for i := 0; i < e.streams; i++ {
		if i < e.coupledStreams {
			e.encoders[i].SetBitrate(unitBitrate * 3) // ~1.5x for stereo
		} else {
			e.encoders[i].SetBitrate(unitBitrate * 2)
		}
	}
}

// Bitrate returns the total bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

// routeChannelsToStreams routes interleaved input to stream buffers.
// This is the inverse of applyChannelMapping in multistream.go.
//
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
// Output: slice of buffers, one per stream (stereo streams interleaved)
func routeChannelsToStreams(
	input []float64,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float64 {
	// Create buffer for each stream
	streamBuffers := make([][]float64, numStreams)
	for i := 0; i < numStreams; i++ {
		chans := streamChannels(i, coupledStreams)
		streamBuffers[i] = make([]float64, frameSize*chans)
	}

	// Route input channels to appropriate streams
	// Key insight: mapping[outCh] tells us which stream channel feeds outCh
	// For encoding, we use the same mapping direction: input channel outCh
	// routes to the stream/channel specified by mapping[outCh]
	for outCh := 0; outCh < inputChannels; outCh++ {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue // Silent channel, skip
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= numStreams {
			continue
		}

		srcChannels := streamChannels(streamIdx, coupledStreams)

		// Copy samples from input channel to stream buffer
		for s := 0; s < frameSize; s++ {
			streamBuffers[streamIdx][s*srcChannels+chanInStream] = input[s*inputChannels+outCh]
		}
	}

	return streamBuffers
}

// writeSelfDelimitedLength writes a self-delimiting packet length.
// Per RFC 6716 Section 3.2.1:
//   - If length < 252: single byte encoding
//   - If length >= 252: two-byte encoding where length = 4*secondByte + firstByte
//
// This is the inverse of parseSelfDelimitedLength in stream.go.
//
// Returns the number of bytes written (1 or 2).
func writeSelfDelimitedLength(dst []byte, length int) int {
	if length < 252 {
		dst[0] = byte(length)
		return 1
	}
	// Two-byte encoding: length = 4*secondByte + firstByte
	// firstByte in range [252, 255], so use 252 + (length % 4)
	// secondByte = (length - firstByte) / 4
	firstByte := 252 + (length % 4)
	secondByte := (length - firstByte) / 4
	dst[0] = byte(firstByte)
	dst[1] = byte(secondByte)
	return 2
}

// selfDelimitedLengthBytes returns the number of bytes needed to encode a length.
func selfDelimitedLengthBytes(length int) int {
	if length < 252 {
		return 1
	}
	return 2
}

// assembleMultistreamPacket combines individual stream packets into a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 packets use self-delimiting framing (length prefix before each packet)
//   - Last packet uses standard framing (no length prefix, consumes remaining bytes)
func assembleMultistreamPacket(streamPackets [][]byte) []byte {
	if len(streamPackets) == 0 {
		return nil
	}

	// Calculate total size
	totalSize := 0
	for i, packet := range streamPackets {
		if i < len(streamPackets)-1 {
			// First N-1 packets need length prefix
			totalSize += selfDelimitedLengthBytes(len(packet))
		}
		totalSize += len(packet)
	}

	output := make([]byte, totalSize)
	offset := 0

	// Write first N-1 packets with self-delimiting framing
	for i := 0; i < len(streamPackets)-1; i++ {
		n := writeSelfDelimitedLength(output[offset:], len(streamPackets[i]))
		offset += n
		copy(output[offset:], streamPackets[i])
		offset += len(streamPackets[i])
	}

	// Last packet uses remaining data (standard framing, no length prefix)
	copy(output[offset:], streamPackets[len(streamPackets)-1])

	return output
}

// Encode encodes multi-channel PCM samples to an Opus multistream packet.
//
// Parameters:
//   - pcm: input samples as float64, sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
//   - frameSize: number of samples per channel (must be valid for Opus: 120, 240, 480, 960, 1920, 2880)
//
// Returns:
//   - The encoded multistream packet (N-1 length-prefixed streams + 1 standard stream)
//   - nil, nil if DTX suppresses all frames (silence detected in all streams)
//   - error if encoding fails
//
// The encoding process:
//  1. Routes input channels to stream buffers via mapping table
//  2. Encodes each stream independently using the unified encoder
//  3. Assembles packets with self-delimiting framing per RFC 6716 Appendix B
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
	// Validate input length
	expectedLen := frameSize * e.inputChannels
	if len(pcm) != expectedLen {
		return nil, fmt.Errorf("%w: got %d samples, expected %d (frameSize=%d, channels=%d)",
			ErrInvalidInput, len(pcm), expectedLen, frameSize, e.inputChannels)
	}

	// Route input channels to stream buffers
	streamBuffers := routeChannelsToStreams(pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)

	// Encode each stream
	streamPackets := make([][]byte, e.streams)
	allNil := true

	for i := 0; i < e.streams; i++ {
		chans := streamChannels(i, e.coupledStreams)
		packet, err := e.encoders[i].Encode(streamBuffers[i], frameSize)
		if err != nil {
			return nil, fmt.Errorf("stream %d encode failed: %w", i, err)
		}

		// Handle DTX case (nil packet means silence suppressed)
		if packet == nil {
			// For DTX, we need to signal silence with a minimal packet
			// Use a zero-length indicator or skip based on RFC 6716
			// For now, treat as empty packet - decoder handles this
			streamPackets[i] = []byte{}
		} else {
			streamPackets[i] = packet
			allNil = false
		}

		_ = chans // Used only for buffer sizing in routeChannelsToStreams
	}

	// If all streams returned nil (DTX), return nil to signal silence
	if allNil {
		return nil, nil
	}

	// Assemble multistream packet with self-delimiting framing
	return assembleMultistreamPacket(streamPackets), nil
}

// SetComplexity sets encoder complexity (0-10) for all stream encoders.
// Higher values use more CPU for better quality.
// Default is 10 (maximum quality).
func (e *Encoder) SetComplexity(complexity int) {
	for _, enc := range e.encoders {
		enc.SetComplexity(complexity)
	}
}

// Complexity returns the complexity setting of the first encoder.
// All stream encoders use the same complexity.
func (e *Encoder) Complexity() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].Complexity()
	}
	return 10 // Default
}

// SetFEC enables or disables in-band Forward Error Correction for all streams.
// When enabled, encoders include LBRR data for loss recovery.
func (e *Encoder) SetFEC(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetFEC(enabled)
	}
}

// FECEnabled returns whether FEC is enabled (from first encoder).
func (e *Encoder) FECEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].FECEnabled()
	}
	return false
}

// SetPacketLoss sets the expected packet loss percentage (0-100) for all streams.
// This affects FEC behavior and bitrate allocation.
func (e *Encoder) SetPacketLoss(lossPercent int) {
	for _, enc := range e.encoders {
		enc.SetPacketLoss(lossPercent)
	}
}

// PacketLoss returns the expected packet loss percentage (from first encoder).
func (e *Encoder) PacketLoss() int {
	if len(e.encoders) > 0 {
		return e.encoders[0].PacketLoss()
	}
	return 0
}

// SetDTX enables or disables Discontinuous Transmission for all streams.
// When enabled, packets are suppressed during silence.
func (e *Encoder) SetDTX(enabled bool) {
	for _, enc := range e.encoders {
		enc.SetDTX(enabled)
	}
}

// DTXEnabled returns whether DTX is enabled (from first encoder).
func (e *Encoder) DTXEnabled() bool {
	if len(e.encoders) > 0 {
		return e.encoders[0].DTXEnabled()
	}
	return false
}
