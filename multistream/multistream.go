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

func applyChannelMapping32(decodedStreams [][]float32, mapping []byte, coupledStreams, frameSize, outputChannels int) []float32 {
	output := make([]float32, frameSize*outputChannels)

	for outCh := 0; outCh < outputChannels; outCh++ {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= len(decodedStreams) {
			continue
		}

		src := decodedStreams[streamIdx]
		srcChannels := streamChannels(streamIdx, coupledStreams)
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

func (d *Decoder) decodeStreamToFloat32(stream int, packet []byte, frameSize int) ([]float32, error) {
	if st, ok := d.decoders[stream].(*streamState); ok {
		return st.decodePacketToFloat32(packet, frameSize)
	}
	out, err := d.decodeStream(stream, packet, frameSize)
	if err != nil {
		return nil, err
	}
	return float64ToFloat32(out), nil
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

	maxChunk := d.sampleRate / 50
	if maxChunk > 0 && frameSize > maxChunk {
		output := make([]float64, totalSamples)
		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := maxChunk
			if remaining < chunk {
				chunk = remaining
			}
			decoded, err := d.decodePLCChunk(chunk)
			if err != nil {
				return nil, err
			}
			total := chunk * d.outputChannels
			if len(decoded) < total {
				return nil, ErrBufferTooSmall
			}
			copy(output[offset:offset+total], decoded[:total])
			offset += total
			remaining -= chunk
		}
		return output, nil
	}

	return d.decodePLCChunk(frameSize)
}

func (d *Decoder) decodePLCChunk(frameSize int) ([]float64, error) {
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
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		samples, err := d.Decode(data, frameSize)
		if err != nil {
			return nil, err
		}
		output := make([]int16, len(samples))
		d.applyProjectionDemixingInt16(output, samples, len(samples)/d.outputChannels)
		return output, nil
	}

	samples, err := d.DecodeToFloat32(data, frameSize)
	if err != nil {
		return nil, err
	}

	if data == nil || len(data) == 0 {
		return float32ToInt16(samples), nil
	}
	return float32ToInt16SoftClip(samples, len(samples)/d.outputChannels, d.outputChannels, d.softClipMem), nil
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
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return d.decodeToFloat32ViaFloat64(data, frameSize)
	}
	if extsupport.DREDRuntime && data != nil && len(data) > 0 && d.dredSidecarActive() {
		d.invalidateDREDPayloadState()
	}

	if data == nil {
		output, err := d.decodePLCToFloat32(frameSize)
		if err == nil && extsupport.DREDRuntime && d.dredSidecarActive() {
			d.markDREDConcealedAll()
		}
		return output, err
	}

	packets, err := parseMultistreamPacket(data, d.streams)
	if err != nil {
		return nil, fmt.Errorf("multistream: parse error: %w", err)
	}

	duration, err := validateStreamDurationsAtRate(packets, d.sampleRate)
	if err != nil {
		return nil, err
	}
	if duration > frameSize {
		return nil, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	decodedStreams := make([][]float32, d.streams)
	for i := 0; i < d.streams; i++ {
		var endDREDCapture func()
		if extsupport.DREDRuntime && d.dredPayloadScannerActive() {
			if st, ok := d.decoders[i].(*streamState); ok && len(packets[i]) > 0 {
				toc := parseStreamTOC(packets[i][0])
				endDREDCapture = d.beginDREDRawMonoGoodFrameCapture(i, st, toc.mode, packets[i])
			}
		}
		decoded, decodeErr := d.decodeStreamToFloat32(i, packets[i], decodeFrameSize)
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

	output := applyChannelMapping32(decodedStreams, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return output, nil
}

func (d *Decoder) decodePLCToFloat32(frameSize int) ([]float32, error) {
	fadeFactor := d.plcState.RecordLoss()
	totalSamples := frameSize * d.outputChannels
	if fadeFactor < 0.001 {
		return make([]float32, totalSamples), nil
	}

	maxChunk := d.sampleRate / 50
	if maxChunk > 0 && frameSize > maxChunk {
		output := make([]float32, totalSamples)
		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := maxChunk
			if remaining < chunk {
				chunk = remaining
			}
			decoded, err := d.decodePLCChunkToFloat32(chunk)
			if err != nil {
				return nil, err
			}
			total := chunk * d.outputChannels
			if len(decoded) < total {
				return nil, ErrBufferTooSmall
			}
			copy(output[offset:offset+total], decoded[:total])
			offset += total
			remaining -= chunk
		}
		return output, nil
	}

	return d.decodePLCChunkToFloat32(frameSize)
}

func (d *Decoder) decodePLCChunkToFloat32(frameSize int) ([]float32, error) {
	decodedStreams := make([][]float32, d.streams)
	for i := 0; i < d.streams; i++ {
		if extsupport.DREDRuntime {
			if decoded, ok, err := d.decodeDREDPLCStream(i, frameSize); err != nil {
				return nil, err
			} else if ok {
				decodedStreams[i] = float64ToFloat32(decoded)
				continue
			}
		}
		decoded, err := d.decodeStreamToFloat32(i, nil, frameSize)
		if err != nil {
			channels := streamChannels(i, d.coupledStreams)
			decoded = make([]float32, frameSize*channels)
		}
		decodedStreams[i] = decoded
	}

	return applyChannelMapping32(decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels), nil
}

func float32ToInt16(samples []float32) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(s)
	}
	return output
}

func float32ToInt16SoftClip(samples []float32, n, channels int, declipMem []float32) []int16 {
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

	tmp := append([]float32(nil), samples[:total]...)
	opusmath.PCMSoftClip(tmp, n, channels, declipMem)
	for i := 0; i < total; i++ {
		output[i] = opusmath.Float32ToInt16(tmp[i])
	}
	return output
}

func (d *Decoder) decodeToFloat32ViaFloat64(data []byte, frameSize int) ([]float32, error) {
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
	opusmath.PCMSoftClip(tmp, n, channels, declipMem)
	for i := 0; i < total; i++ {
		output[i] = opusmath.Float32ToInt16(tmp[i])
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
