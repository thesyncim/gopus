// Multistream decode implementation for Opus surround sound.
// This file contains the Decode methods that parse multistream packets,
// decode each elementary stream, and apply channel mapping to produce
// the final interleaved output.

package multistream

import (
	"fmt"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
)

// ensureDecodedStreamsScratch returns a reusable [][]float32 header sized to
// d.streams, clearing any stale per-stream references so a decode failure or
// short stream cannot leak a previous frame's buffer into the channel mapping.
func (d *Decoder) ensureDecodedStreamsScratch() [][]float32 {
	s := d.decodedStreamsScratch
	if cap(s) < d.streams {
		s = make([][]float32, d.streams)
	}
	s = s[:d.streams]
	for i := range s {
		s[i] = nil
	}
	d.decodedStreamsScratch = s
	return s
}

func applyChannelMapping32(decodedStreams [][]float32, mapping []byte, coupledStreams, frameSize, outputChannels int) []float32 {
	output := make([]float32, frameSize*outputChannels)
	applyChannelMapping32Into(output, decodedStreams, mapping, coupledStreams, frameSize, outputChannels)
	return output
}

func applyChannelMapping32Into(output []float32, decodedStreams [][]float32, mapping []byte, coupledStreams, frameSize, outputChannels int) {
	clear(output[:frameSize*outputChannels])
	for outCh := range outputChannels {
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
		for s := range frameSize {
			srcIdx := s*srcChannels + chanInStream
			if srcIdx < len(src) {
				output[s*outputChannels+outCh] = src[srcIdx]
			}
		}
	}

}

func (d *Decoder) decodeStreamToFloat32(stream int, packet []byte, frameSize int) ([]float32, error) {
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
// Returns sample-interleaved float32 samples: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
// where N is the number of output channels.
//
// All elementary streams within the packet must have the same frame duration.
// If durations differ, ErrDurationMismatch is returned.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.DecodeToFloat32(data, frameSize)
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
		if pcm, handled, err := d.decodeFixedProjectionInt16(data, frameSize); err != nil {
			return nil, err
		} else if handled {
			return pcm, nil
		}
		// libopus opus_projection_decode passes OPTIONAL_CLIP, so each per-stream
		// decoded buffer is soft-clipped before the int16 mapping-matrix multiply.
		// Request that here (the float demix path in DecodeToFloat32 does not).
		samples, err := d.decodeToFloat32(data, frameSize, false, true)
		if err != nil {
			return nil, err
		}
		output := make([]int16, len(samples))
		d.applyProjectionDemixingInt16(output, samples, len(samples)/d.outputChannels)
		return output, nil
	}

	if pcm, handled, err := d.decodeFixedOutputInt16(data, frameSize); err != nil {
		return nil, err
	} else if handled {
		return pcm, nil
	}

	samples, err := d.decodeToFloat32(data, frameSize, true, true)
	if err != nil {
		return nil, err
	}

	return float32ToInt16(samples), nil
}

// DecodeToFloat32 decodes a multistream packet and returns float32 PCM.
// This is a convenience wrapper for audio APIs expecting float32.
//
// Parameters:
//   - data: raw multistream packet data, or nil for PLC
//   - frameSize: frame size in samples at the decoder sample rate
//
// Returns sample-interleaved float32 samples in approximate range [-1, 1].
// The output format is: [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ch1_s1, ...]
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	output := d.outputScratchFor(frameSize * d.outputChannels)
	n, err := d.DecodeIntoFloat32(data, output, frameSize)
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), output[:n*d.outputChannels]...), nil
}

// DecodeIntoFloat32 decodes into caller-owned PCM and returns samples per
// channel. The output buffer may be larger than the packet's actual duration.
func (d *Decoder) DecodeIntoFloat32(data []byte, output []float32, frameSize int) (int, error) {
	if n, handled, err := d.decodeFixedOutputFloat32(data, output, frameSize); err != nil {
		return 0, err
	} else if handled {
		return n, nil
	}
	return d.decodeToFloat32Into(data, frameSize, true, false, output)
}

func (d *Decoder) outputScratchFor(n int) []float32 {
	if cap(d.outputScratch) < n {
		d.outputScratch = make([]float32, n)
	}
	return d.outputScratch[:n]
}

func (d *Decoder) decodeToFloat32(data []byte, frameSize int, applyProjection, perStreamSoftClip bool) ([]float32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)
	scratch := d.outputScratchFor(frameSize * d.outputChannels)
	n, err := d.decodeToFloat32Into(data, frameSize, applyProjection, perStreamSoftClip, scratch)
	if err != nil {
		return nil, err
	}
	return append([]float32(nil), scratch[:n*d.outputChannels]...), nil
}

func (d *Decoder) decodeToFloat32Into(data []byte, frameSize int, applyProjection, perStreamSoftClip bool, output []float32) (int, error) {
	if frameSize <= 0 {
		return 0, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)

	// A nil OR zero-length packet is packet loss: libopus opus_multistream_decode
	// sets do_plc=1 for len==0 (opus_multistream_decoder.c:213), concealing the
	// requested frame size exactly as for a NULL packet.
	if len(data) == 0 {
		n, err := d.decodePLCToFloat32Into(frameSize, applyProjection, output)
		if err == nil && extsupport.DREDRuntime && d.dredSidecarActive() {
			d.markDREDConcealedAll()
		}
		return n, err
	}

	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return 0, fmt.Errorf("multistream: parse error: %w", err)
	}
	d.packetsScratch = packets

	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return 0, err
	}
	if duration > frameSize {
		return 0, ErrBufferTooSmall
	}
	decodeFrameSize := duration
	needed := decodeFrameSize * d.outputChannels
	if len(output) < needed {
		return 0, ErrBufferTooSmall
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		d.invalidateDREDPayloadState()
	}

	decodedStreams := d.ensureDecodedStreamsScratch()
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
			return 0, fmt.Errorf("multistream: stream %d decode error: %w", i, decodeErr)
		}
		// libopus opus_decode_native soft-clips each stream's output (sized to the
		// stream's channels) when soft_clip is requested, before the copy/demix
		// callback; otherwise it clears the per-stream soft-clip memory.
		d.applyPerStreamSoftClip(i, decoded, decodeFrameSize, perStreamSoftClip)
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

	output = output[:needed]
	applyChannelMapping32Into(output, decodedStreams, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, decodeFrameSize)
	}

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return decodeFrameSize, nil
}

func (d *Decoder) decodePLCToFloat32Into(frameSize int, applyProjection bool, output []float32) (int, error) {
	totalSamples := frameSize * d.outputChannels
	if len(output) < totalSamples {
		return 0, ErrBufferTooSmall
	}
	fadeFactor := d.plcState.RecordLoss()
	if fadeFactor < 0.001 {
		clear(output[:totalSamples])
		return frameSize, nil
	}

	maxChunk := int(d.sampleRate) / 50
	if maxChunk > 0 && frameSize > maxChunk {
		remaining := frameSize
		offset := 0
		for remaining > 0 {
			chunk := min(remaining, maxChunk)
			total := chunk * d.outputChannels
			err := d.decodePLCChunkToFloat32Into(chunk, applyProjection, output[offset:offset+total])
			if err != nil {
				return 0, err
			}
			offset += total
			remaining -= chunk
		}
		return frameSize, nil
	}

	if err := d.decodePLCChunkToFloat32Into(frameSize, applyProjection, output[:totalSamples]); err != nil {
		return 0, err
	}
	return frameSize, nil
}

func (d *Decoder) decodePLCChunkToFloat32Into(frameSize int, applyProjection bool, output []float32) error {
	// opus_decode_native returns loss output before its soft-clip step. Keep
	// each stream's clipping memory intact for the next received packet.
	decodedStreams := d.ensureDecodedStreamsScratch()
	for i := 0; i < d.streams; i++ {
		if extsupport.DREDRuntime {
			if decoded, ok, err := d.decodeDREDPLCStream(i, frameSize); err != nil {
				return err
			} else if ok {
				decodedStreams[i] = decoded
				continue
			}
		}
		decoded, err := d.decodeStreamToFloat32(i, nil, frameSize)
		if err != nil {
			channels := streamChannels(i, d.coupledStreams)
			decoded = d.silenceScratchFor(frameSize * channels)
		}
		decodedStreams[i] = decoded
	}

	applyChannelMapping32Into(output, decodedStreams, d.mapping, d.coupledStreams, frameSize, d.outputChannels)
	if applyProjection {
		d.applyProjectionDemixing32(output, frameSize)
	}
	return nil
}

func (d *Decoder) silenceScratchFor(n int) []float32 {
	if cap(d.silenceScratch) < n {
		d.silenceScratch = make([]float32, n)
	}
	out := d.silenceScratch[:n]
	clear(out)
	return out
}

// applyPerStreamSoftClip soft-clips stream i's interleaved decoded buffer in
// place when enabled (the int16 OPTIONAL_CLIP path), advancing that stream's
// soft-clip memory; when disabled it clears the memory. This mirrors libopus
// opus_decode_native's per-stream soft_clip step, run before the multistream
// copy/demix callback. A no-op when the stream is not a *streamState (e.g. a
// stub/test decoder).
func (d *Decoder) applyPerStreamSoftClip(i int, decoded []float32, frameSize int, perStreamSoftClip bool) {
	st, ok := d.decoders[i].(*streamState)
	if !ok {
		return
	}
	channels := streamChannels(i, d.coupledStreams)
	if !perStreamSoftClip {
		st.clearSoftClipMem()
		return
	}
	st.softClipStreamOutput(decoded, frameSize, channels)
}

func float32ToInt16(samples []float32) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(s)
	}
	return output
}
