// Multistream decode implementation for Opus surround sound.
// This file contains the Decode methods that parse multistream packets,
// decode each elementary stream, and apply channel mapping to produce
// the final interleaved output.

package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
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

func (d *Decoder) decodeStream(stream int, packet []byte, frameSize int) ([]float64, error) {
	if stream < d.coupledStreams {
		return d.decoders[stream].DecodeStereo(packet, frameSize)
	}
	return d.decoders[stream].Decode(packet, frameSize)
}

// Decode decodes a multistream Opus packet and returns PCM samples.
//
// If data is nil, performs Packet Loss Concealment (PLC) by generating
// concealment audio based on the previous frames' state.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved float64 samples: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
// where N is the number of output channels.
//
// All elementary streams within the packet must have the same frame duration.
// If durations differ, ErrDurationMismatch is returned.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float64, error) {
	if extsupport.DREDRuntime && data != nil && len(data) > 0 && d.dredSidecarActive() {
		d.invalidateDREDPayloadState()
	}

	// Handle PLC for nil data (lost packet)
	if data == nil {
		output, err := d.decodePLC(frameSize)
		if err == nil && extsupport.DREDRuntime && d.dredSidecarActive() {
			d.markDREDConcealedAll()
		}
		return output, err
	}

	// Parse multistream packet into individual stream packets
	packets, err := parseMultistreamPacket(data, d.streams)
	if err != nil {
		return nil, fmt.Errorf("multistream: parse error: %w", err)
	}

	// Validate that all streams have consistent frame durations
	duration, err := validateStreamDurationsAtRate(packets, d.sampleRate)
	if err != nil {
		return nil, err
	}
	if duration > frameSize {
		return nil, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	// Decode each stream
	decodedStreams := make([][]float64, d.streams)
	for i := 0; i < d.streams; i++ {
		var endDREDCapture func()
		if extsupport.DREDRuntime && d.dredPayloadScannerActive() {
			if st, ok := d.decoders[i].(*streamState); ok && len(packets[i]) > 0 {
				toc := parseStreamTOC(packets[i][0])
				endDREDCapture = d.beginDREDRawMonoGoodFrameCapture(i, st, toc.mode, packets[i])
			}
		}
		decoded, decodeErr := d.decodeStream(i, packets[i], decodeFrameSize)
		if endDREDCapture != nil {
			endDREDCapture()
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("multistream: stream %d decode error: %w", i, decodeErr)
		}
		decodedStreams[i] = decoded
	}
	if extsupport.DREDRuntime && d.dredPayloadScannerActive() {
		for i := 0; i < d.streams; i++ {
			d.maybeCacheDREDPayload(i, packets[i])
		}
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		for i := 0; i < d.streams; i++ {
			d.markDREDUpdated(i)
		}
	}

	// Apply channel mapping to produce final output
	output := applyChannelMapping(decodedStreams, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)
	d.applyProjectionDemixing(output, decodeFrameSize)

	// Reset PLC state after successful decode
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

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
		if extsupport.DREDRuntime {
			if decoded, ok, err := d.decodeDREDPLCStream(i, frameSize); err != nil {
				return nil, err
			} else if ok {
				decodedStreams[i] = decoded
				continue
			}
		}
		decoded, err := d.decodeStream(i, nil, frameSize)
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
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved int16 samples in range [-32768, 32767].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	samples, err := d.Decode(data, frameSize)
	if err != nil {
		return nil, err
	}

	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		output := make([]int16, len(samples))
		d.applyProjectionDemixingInt16(output, samples, len(samples)/d.outputChannels)
		return output, nil
	}
	if data == nil || len(data) == 0 {
		return float64ToInt16(samples), nil
	}
	return float64ToInt16SoftClip(samples, len(samples)/d.outputChannels, d.outputChannels, d.softClipMem), nil
}

// DecodeToFloat32 decodes a multistream packet and converts to float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
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

func float64ToInt16(samples []float64) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(float32(s))
	}
	return output
}

func float64ToInt16SoftClip(samples []float64, n, channels int, declipMem []float32) []int16 {
	output := make([]int16, len(samples))
	if channels < 1 || n < 1 || len(samples) == 0 || len(declipMem) < channels {
		return output
	}
	total := n * channels
	if total > len(samples) {
		total = len(samples)
	}
	if total <= 0 {
		return output
	}

	tmp := make([]float32, total)
	for i := 0; i < total; i++ {
		tmp[i] = float32(samples[i])
	}
	multistreamPCMSoftClip(tmp, n, channels, declipMem)
	for i := 0; i < total; i++ {
		output[i] = opusmath.Float32ToInt16(tmp[i])
	}
	return output
}

func multistreamPCMSoftClip(x []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(x) == 0 || len(declipMem) < channels {
		return
	}

	total := n * channels
	if total > len(x) {
		total = len(x)
	}

	allWithinNeg1Pos1 := true
	for i := 0; i < total; i++ {
		v := x[i]
		if v > 2 {
			x[i] = 2
			allWithinNeg1Pos1 = false
		} else if v < -2 {
			x[i] = -2
			allWithinNeg1Pos1 = false
		} else if v > 1 || v < -1 {
			allWithinNeg1Pos1 = false
		}
	}
	if allWithinNeg1Pos1 {
		for c := 0; c < channels; c++ {
			if declipMem[c] != 0 {
				goto applySoftClip
			}
		}
		return
	}

applySoftClip:
	for c := 0; c < channels; c++ {
		a := declipMem[c]
		for i := 0; i < n; i++ {
			idx := i*channels + c
			if idx >= len(x) {
				break
			}
			v := x[idx]
			if v*a >= 0 {
				break
			}
			x[idx] = v + a*v*v
		}

		curr := 0
		if c >= len(x) {
			declipMem[c] = a
			continue
		}
		x0 := x[c]

		for {
			var i int
			if allWithinNeg1Pos1 {
				i = n
			} else {
				for i = curr; i < n; i++ {
					idx := i*channels + c
					if idx >= len(x) {
						i = n
						break
					}
					v := x[idx]
					if v > 1 || v < -1 {
						break
					}
				}
			}
			if i == n {
				a = 0
				break
			}

			peakPos := i
			start := i
			end := i
			idx := i*channels + c
			if idx >= len(x) {
				a = 0
				break
			}
			vref := x[idx]
			maxval := multistreamFloat32Abs(vref)

			for start > 0 {
				idxPrev := (start-1)*channels + c
				if idxPrev >= len(x) || vref*x[idxPrev] < 0 {
					break
				}
				start--
			}
			for end < n {
				idxEnd := end*channels + c
				if idxEnd >= len(x) || vref*x[idxEnd] < 0 {
					break
				}
				val := multistreamFloat32Abs(x[idxEnd])
				if val > maxval {
					maxval = val
					peakPos = end
				}
				end++
			}

			special := start == 0 && vref*x[c] >= 0
			if maxval > 0 {
				a = (maxval - 1) / (maxval * maxval)
				a += a * 2.4e-7
				if vref > 0 {
					a = -a
				}
			} else {
				a = 0
			}

			for i = start; i < end; i++ {
				idx2 := i*channels + c
				if idx2 >= len(x) {
					break
				}
				v := x[idx2]
				x[idx2] = v + a*v*v
			}

			if special && peakPos >= 2 {
				offset := x0 - x[c]
				delta := offset / float32(peakPos)
				for i = curr; i < peakPos; i++ {
					offset -= delta
					idx2 := i*channels + c
					if idx2 >= len(x) {
						break
					}
					v := x[idx2] + offset
					if v > 1 {
						v = 1
					} else if v < -1 {
						v = -1
					}
					x[idx2] = v
				}
			}

			curr = end
			if curr == n {
				break
			}
		}
		declipMem[c] = a
	}
}

func multistreamFloat32Abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// float64ToFloat32 converts float64 samples to float32.
func float64ToFloat32(samples []float64) []float32 {
	output := make([]float32, len(samples))
	for i, s := range samples {
		output[i] = float32(s)
	}
	return output
}
