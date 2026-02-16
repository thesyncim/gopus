// Multistream decode implementation for Opus surround sound.
// This file contains the Decode methods that parse multistream packets,
// decode each elementary stream, and apply channel mapping to produce
// the final interleaved output.

package multistream

import (
	"fmt"
	"math"

	"github.com/thesyncim/gopus/plc"
)

// applyChannelMapping routes decoded stream audio to output channels according to the mapping table.
//
// Parameters:
//   - decodedStreams: slice of decoded audio for each stream (interleaved if stereo)
//   - mapping: channel mapping table (mapping[outCh] = sourceIndex)
//   - coupledStreams: number of coupled (stereo) streams
//   - frameSize: samples per channel
//   - outputChannels: total output channels
//
// Returns sample-interleaved output: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func applyChannelMapping(decodedStreams [][]float64, mapping []byte, coupledStreams, frameSize, outputChannels int) []float64 {
	output := make([]float64, frameSize*outputChannels)

	for outCh := 0; outCh < outputChannels; outCh++ {
		mappingIdx := mapping[outCh]

		// Silent channel
		if mappingIdx == 255 {
			// Leave zeros in output for this channel
			continue
		}

		// Resolve mapping to stream index and channel within stream
		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= len(decodedStreams) {
			// Invalid stream index (shouldn't happen if validation passed)
			continue
		}

		src := decodedStreams[streamIdx]
		srcChannels := streamChannels(streamIdx, coupledStreams)

		// Copy samples from source stream to output channel
		for s := 0; s < frameSize; s++ {
			srcIdx := s*srcChannels + chanInStream
			if srcIdx < len(src) {
				output[s*outputChannels+outCh] = src[srcIdx]
			}
		}
	}

	return output
}

func (d *Decoder) applyProjectionDemixing(output []float64, frameSize int) {
	rows := d.outputChannels
	cols := d.projectionCols
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 || cols > rows {
		return
	}

	if cap(d.projectionScratch) < cols {
		d.projectionScratch = make([]float64, cols)
	}
	tmp := d.projectionScratch[:cols]

	for s := 0; s < frameSize; s++ {
		frame := output[s*rows : (s+1)*rows]
		copy(tmp, frame[:cols])
		for row := 0; row < rows; row++ {
			sum := 0.0
			for col := 0; col < cols; col++ {
				sum += d.projectionDemixing[col*rows+row] * tmp[col]
			}
			frame[row] = sum
		}
	}
}

// Decode decodes a multistream Opus packet and returns PCM samples.
//
// If data is nil, performs Packet Loss Concealment (PLC) by generating
// concealment audio based on the previous frames' state.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at 48kHz per channel (e.g., 960 for 20ms)
//
// Returns sample-interleaved float64 samples: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
// where N is the number of output channels.
//
// The frameSize parameter specifies samples per channel at 48kHz. For example:
//   - 120 samples = 2.5ms
//   - 240 samples = 5ms
//   - 480 samples = 10ms
//   - 960 samples = 20ms
//   - 1920 samples = 40ms
//   - 2880 samples = 60ms
//
// All elementary streams within the packet must have the same frame duration.
// If durations differ, ErrDurationMismatch is returned.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float64, error) {
	// Handle PLC for nil data (lost packet)
	if data == nil {
		return d.decodePLC(frameSize)
	}

	// Parse multistream packet into individual stream packets
	packets, err := parseMultistreamPacket(data, d.streams)
	if err != nil {
		return nil, fmt.Errorf("multistream: parse error: %w", err)
	}

	// Validate that all streams have consistent frame durations
	_, err = validateStreamDurations(packets)
	if err != nil {
		return nil, err
	}

	// Decode each stream
	decodedStreams := make([][]float64, d.streams)
	for i := 0; i < d.streams; i++ {
		var decoded []float64
		var decodeErr error

		if i < d.coupledStreams {
			// Coupled stream: decode as stereo
			decoded, decodeErr = d.decoders[i].DecodeStereo(packets[i], frameSize)
		} else {
			// Uncoupled stream: decode as mono
			decoded, decodeErr = d.decoders[i].Decode(packets[i], frameSize)
		}

		if decodeErr != nil {
			return nil, fmt.Errorf("multistream: stream %d decode error: %w", i, decodeErr)
		}
		decodedStreams[i] = decoded
	}

	// Apply channel mapping to produce final output
	output := applyChannelMapping(decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels)
	d.applyProjectionDemixing(output, frameSize)

	// Reset PLC state after successful decode
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, d.outputChannels)

	return output, nil
}

// decodePLC generates concealment audio for a lost multistream packet.
// Each stream generates its own PLC output, then channel mapping is applied.
func (d *Decoder) decodePLC(frameSize int) ([]float64, error) {
	// Record loss and get fade factor
	fadeFactor := d.plcState.RecordLoss()

	// Total output samples
	totalSamples := frameSize * d.outputChannels

	// If fade is exhausted, return silence
	if fadeFactor < 0.001 {
		return make([]float64, totalSamples), nil
	}

	// Decode PLC for each stream
	decodedStreams := make([][]float64, d.streams)
	for i := 0; i < d.streams; i++ {
		var decoded []float64
		var err error

		if i < d.coupledStreams {
			// Coupled stream: decode stereo PLC
			decoded, err = d.decoders[i].DecodeStereo(nil, frameSize)
		} else {
			// Uncoupled stream: decode mono PLC
			decoded, err = d.decoders[i].Decode(nil, frameSize)
		}

		if err != nil {
			// On PLC error, use silence for this stream
			channels := streamChannels(i, d.coupledStreams)
			decoded = make([]float64, frameSize*channels)
		}
		decodedStreams[i] = decoded
	}

	// Apply channel mapping to produce final output
	output := applyChannelMapping(decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels)
	d.applyProjectionDemixing(output, frameSize)

	return output, nil
}

// DecodeToInt16 decodes a multistream packet and converts to int16 PCM.
// This is a convenience wrapper for common audio output formats.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at 48kHz per channel
//
// Returns sample-interleaved int16 samples in range [-32768, 32767].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	samples, err := d.Decode(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToInt16(samples), nil
}

// DecodeToFloat32 decodes a multistream packet and converts to float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at 48kHz per channel
//
// Returns sample-interleaved float32 samples in approximate range [-1, 1].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	samples, err := d.Decode(data, frameSize)
	if err != nil {
		return nil, err
	}

	return float64ToFloat32(samples), nil
}

// float64ToInt16 converts float64 samples to int16.
// Clamps values to [-32768, 32767].
func float64ToInt16(samples []float64) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		scaled := s * 32768.0
		if scaled > 32767.0 {
			output[i] = 32767
			continue
		}
		if scaled < -32768.0 {
			output[i] = -32768
			continue
		}
		output[i] = int16(math.RoundToEven(scaled))
	}
	return output
}

// float64ToFloat32 converts float64 samples to float32.
func float64ToFloat32(samples []float64) []float32 {
	output := make([]float32, len(samples))
	for i, s := range samples {
		output[i] = float32(s)
	}
	return output
}
