//go:build gopus_fixed_point

package multistream

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// DecodeToResFixed decodes a multistream packet and returns the libopus
// FIXED_POINT per-output-channel opus_res samples, interleaved by output
// channel ([ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]). It mirrors
// opus_multistream_decode_native built FIXED_POINT: each elementary stream is
// decoded to opus_res, then the surround channel mapping
// (copy_channel_out_short / copy_channel_out_int24) routes each stream channel
// to its output channel(s) in the integer domain.
//
// The second return value reports whether every stream frame was produced by
// the integer path or is integer-exact through the SILK round-trip. When it is
// false the packet contains a frame the integer path does not cover (for
// example projection or unsupported Hybrid redundancy); the caller falls back
// to the float conversion for that packet. CELT loss frames use the integer
// PLC path when all streams have matching CELT history.
//
// The output opus_res values feed RES2INT16 (int16) or RES2INT24==identity
// (int24) per the libopus copy_channel_out routines.
func (d *Decoder) DecodeToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	if len(data) == 0 {
		return d.DecodePLCToResFixed(frameSize)
	}
	if frameSize <= 0 {
		return nil, false, nil
	}
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		return nil, false, nil
	}

	packets, err := parseMultistreamPacketScratch(d.packetsScratch, &d.packetParser, &d.reframeArena, data, d.streams)
	if err != nil {
		return nil, false, err
	}
	d.packetsScratch = packets
	duration, err := validateStreamDurationsAtRateScratch(&d.packetParser, packets, int(d.sampleRate))
	if err != nil {
		return nil, false, err
	}
	if duration > frameSize {
		return nil, false, ErrBufferTooSmall
	}
	decodeFrameSize := duration

	// Classify every stream up front without decoding so a packet containing a
	// frame the integer path does not cover (Hybrid, multi-frame, DTX/PLC) is
	// declined before any decode runs. This avoids double-decoding (which would
	// corrupt the shared float cross-frame state) when the caller falls back to
	// the float conversion.
	for i := 0; i < d.streams; i++ {
		st, ok := d.decoders[i].(*streamState)
		if !ok {
			return nil, false, nil
		}
		if !fixedHandleableStreamPacket(packets[i], int(d.sampleRate), &st.packetParser) {
			return nil, false, nil
		}
	}

	// Every stream passed the pre-check, so each decode advances the shared
	// float state exactly once and yields bit-exact opus_res. A post-check
	// decline would mean state was advanced but the result is unusable, so it is
	// surfaced as an error rather than silently re-decoded by the caller.
	if cap(d.fixedStreamRes) < d.streams {
		d.fixedStreamRes = make([][]int32, d.streams)
	}
	streamRes := d.fixedStreamRes[:d.streams]
	for i := 0; i < d.streams; i++ {
		st := d.decoders[i].(*streamState)
		res, handled, derr := st.decodePacketToResFixed(packets[i], decodeFrameSize)
		if derr != nil {
			return nil, false, derr
		}
		if !handled {
			return nil, false, ErrInvalidPacket
		}
		streamRes[i] = res
	}

	// Reset the float PLC bookkeeping the same way the float decode does so a
	// following concealment frame behaves identically.
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		for i := 0; i < d.streams; i++ {
			d.markDREDUpdated(i)
		}
	}

	d.fixedOutput = applyChannelMappingResInto(d.fixedOutput, streamRes, d.mapping, d.coupledStreams, decodeFrameSize, d.outputChannels)

	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, decodeFrameSize, d.outputChannels)

	return d.fixedOutput, true, nil
}

// DecodePLCToResFixed conceals one public multistream loss request in the
// fixed-point domain when every elementary stream is in CELT mode. Hybrid and
// SILK PLC retain their existing paths until their multistream composition is
// available in integer form. The request is split into the same 20 ms chunks
// as the public wrappers, and each stream's float decoder advances once per
// chunk so switching back to float output preserves its history. Per-mode
// CELT PLC state advances even when the outer bookkeeping fade reaches zero.
func (d *Decoder) DecodePLCToResFixed(frameSize int) ([]int32, bool, error) {
	f2_5 := int(d.sampleRate) / 400
	if frameSize <= 0 || f2_5 <= 0 || frameSize%f2_5 != 0 || frameSize > int(d.sampleRate)*3/25 {
		return nil, false, nil
	}
	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		return nil, false, nil
	}
	if extsupport.DREDRuntime && d.dredSidecarActive() {
		return nil, false, nil
	}
	for _, decoder := range d.decoders {
		st, ok := decoder.(*streamState)
		if !ok || !st.haveDecoded || st.lastMode != streamModeCELT || !st.canDecodeLostFixed() {
			return nil, false, nil
		}
	}

	needed := frameSize * d.outputChannels
	if cap(d.fixedOutput) < needed {
		d.fixedOutput = make([]int32, needed)
	} else {
		d.fixedOutput = d.fixedOutput[:needed]
		clear(d.fixedOutput)
	}
	if cap(d.fixedStreamRes) < d.streams {
		d.fixedStreamRes = make([][]int32, d.streams)
	}
	streamRes := d.fixedStreamRes[:d.streams]
	maxChunk := int(d.sampleRate) / 50
	if maxChunk <= 0 {
		return nil, false, nil
	}
	d.plcState.RecordLoss()

	for offset := 0; offset < frameSize; {
		chunk := nextCELTPLCChunk(frameSize-offset, maxChunk, maxChunk)
		outOffset := offset * d.outputChannels
		outEnd := outOffset + chunk*d.outputChannels
		for i := 0; i < d.streams; i++ {
			st := d.decoders[i].(*streamState)
			if _, err := st.decodePacketToFloat32Unscaled(nil, chunk); err != nil {
				return nil, false, err
			}
			res, err := st.decodeLostFixed(chunk)
			if err != nil {
				return nil, false, err
			}
			if st.decodeGainQ8 != 0 {
				fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(int(st.decodeGainQ8)))
			}
			streamRes[i] = res
		}
		applyChannelMappingResInto(d.fixedOutput[outOffset:outEnd], streamRes, d.mapping, d.coupledStreams, chunk, d.outputChannels)
		offset += chunk
	}
	return d.fixedOutput, true, nil
}

func fixedCELTCodedChannels(packetStereo bool) int {
	if packetStereo {
		return 2
	}
	return 1
}

// applyChannelMappingRes routes per-stream opus_res samples to output channels,
// mirroring the copy_channel_out routing in opus_multistream_decode_native:
// each stream channel feeds the output channel(s) selected by the mapping, and
// muted channels (mapping value 255) stay zero.
func applyChannelMappingRes(streamRes [][]int32, mapping []byte, coupledStreams, frameSize, outputChannels int) []int32 {
	return applyChannelMappingResInto(nil, streamRes, mapping, coupledStreams, frameSize, outputChannels)
}

func applyChannelMappingResInto(out []int32, streamRes [][]int32, mapping []byte, coupledStreams, frameSize, outputChannels int) []int32 {
	needed := frameSize * outputChannels
	if cap(out) < needed {
		out = make([]int32, needed)
	} else {
		out = out[:needed]
		clear(out)
	}
	for outCh := 0; outCh < outputChannels; outCh++ {
		mappingIdx := mapping[outCh]
		if mappingIdx == 255 {
			continue
		}
		streamIdx, chanInStream := resolveMapping(mappingIdx, coupledStreams)
		if streamIdx < 0 || streamIdx >= len(streamRes) {
			continue
		}
		src := streamRes[streamIdx]
		srcChannels := streamChannels(streamIdx, coupledStreams)
		for s := 0; s < frameSize; s++ {
			srcIdx := s*srcChannels + chanInStream
			if srcIdx < len(src) {
				out[s*outputChannels+outCh] = src[srcIdx]
			}
		}
	}
	return out
}

// fixedHandleableStreamPacket reports whether the integer multistream decode
// can reproduce a stream packet bit-exactly: a single received frame that is
// CELT-only (decoded by the integer CELT decoder), SILK-only (integer-exact
// through the lossless float->int16 round-trip), or Hybrid (integer SILK
// opus_res lowband plus integer CELT highband, start band 17, celt_accum).
// CELT code-3 packets decode each frame sequentially, and SILK packets are
// integer-exact through their float32/int16 round-trip. Hybrid packets with
// multiple frames and degenerate (DTX/PLC) frames are declined before state is
// advanced. A Hybrid stream at an API rate below 16 kHz is also declined: its
// wideband SILK lowband is produced by the float downsampling resampler, which
// has no integer int16 output for INT16TORES, so the integer hybrid path cannot
// reproduce it (the float conversion is bit-exact with the FIXED_POINT
// reference for those rates).
func fixedHandleableStreamPacket(data []byte, sampleRate int, scratch *packetScratch) bool {
	if len(data) <= 1 {
		return false
	}
	toc := parseStreamTOC(data[0])
	if toc.mode != streamModeCELT && toc.mode != streamModeSILK && toc.mode != streamModeHybrid {
		return false
	}
	if toc.mode == streamModeHybrid && sampleRate < 16000 {
		return false
	}
	parsed, err := parseOpusPacketInto(scratch, data, false)
	if err != nil || len(parsed.frames) == 0 {
		return false
	}
	if toc.mode == streamModeHybrid && len(parsed.frames) != 1 {
		return false
	}
	for _, frame := range parsed.frames {
		if len(frame) <= 1 {
			return false
		}
	}
	return true
}

// decodePacketToResFixed decodes one elementary-stream packet to interleaved
// opus_res samples (stride = stream channel count). The packet must already be
// classified handleable by fixedHandleableStreamPacket.
//
// It runs the float decode first to advance the float cross-frame state (so a
// following float Decode or PLC frame is unaffected), then captures
// integer-exact opus_res:
//
//   - CELT-only: the fixed-point integer CELT decoder selected by the build.
//   - SILK-only: opus_res = INT16TORES(int16) where the int16 is the lossless
//     float->int16 of the SILK output, matching libopus' FIXED_POINT SILK
//     opus_res (the SILK output is integer-native and round-trips through
//     float32 without loss).
func (d *streamState) decodePacketToResFixed(data []byte, frameSize int) ([]int32, bool, error) {
	channels := int(d.channels)

	toc := parseStreamTOC(data[0])
	parsed, err := parseOpusPacketInto(&d.packetParser, data, false)
	if err != nil || len(parsed.frames) == 0 || frameSize%len(parsed.frames) != 0 {
		return nil, false, nil
	}
	if toc.mode == streamModeCELT {
		if err := d.prepareFixedCELTFrame(streamModeCELT, parsed, toc); err != nil {
			return nil, false, err
		}
	}
	d.beginFixedCELTTransition(toc.mode, d.decodeGainQ8)
	defer d.endFixedCELTTransition()

	// A Hybrid frame must arm the integer highband hook on the stream's hybrid
	// decoder before the float decode runs, so the float hybrid decode also drives
	// the integer CELT highband (start band 17, celt_accum) onto the integer SILK
	// opus_res lowband. The hook stashes the combined opus_res output in
	// fixedHybridRes; the CELT-only / SILK paths capture their integer output after
	// the float decode instead.
	hybridArmed := false
	if toc.mode == streamModeHybrid {
		var err error
		hybridArmed, err = d.prepareFixedHybridStream(toc)
		if err != nil {
			return nil, false, err
		}
	}

	floatOut, err := d.decodePacketToFloat32Unscaled(data, frameSize)
	if hybridArmed {
		d.finishFixedHybridStream()
	}
	if err != nil {
		return nil, false, err
	}

	needed := frameSize * channels
	if cap(d.fixedRes) < needed {
		d.fixedRes = make([]int32, needed)
	}
	res := d.fixedRes[:needed]

	var handled bool
	switch toc.mode {
	case streamModeSILK:
		floatToRes(res, floatOut)
		d.applyFixedCELTTransition(res, frameSize)
		handled = true
	case streamModeCELT:
		handled = d.celtFixedRes(parsed, frameSize, toc, res)
		if handled {
			d.applyFixedCELTTransition(res, frameSize)
		}
	case streamModeHybrid:
		if hybridArmed && d.fixedHybridHandled && len(d.fixedHybridRes) >= needed {
			copy(res, d.fixedHybridRes[:needed])
			handled = true
		}
	}
	if !handled {
		return nil, false, nil
	}
	if d.decodeGainQ8 != 0 {
		fixedpoint.ApplyDecodeGainRes(res, fixedpoint.DecodeGainQ16(int(d.decodeGainQ8)))
	}
	return res, true, nil
}

// decodePacketToFloat32Unscaled advances the shared float decoder state for a
// fixed-domain public decode without applying its final output gain. The fixed
// opus_res path applies that gain with the source integer MULT32_32_Q16 and
// saturation semantics after each stream is decoded.
func (d *streamState) decodePacketToFloat32Unscaled(data []byte, frameSize int) ([]float32, error) {
	gain := d.decodeGainQ8
	d.decodeGainQ8 = 0
	out, err := d.decodePacketToFloat32(data, frameSize)
	d.decodeGainQ8 = gain
	return out, err
}

// finishFixedHybridStream disarms the integer Hybrid highband hook after the
// float Hybrid decode completes.
func (d *streamState) finishFixedHybridStream() {
	d.hybridDec.SetFixedHighband(nil)
}

// streamFixedHybridHook implements hybrid.FixedHybridHighband for one elementary
// stream, building the integer Hybrid frame's opus_res output exactly as the
// single-stream gopus.Decoder.DecodeHybridHighband does.
type streamFixedHybridHook struct {
	st *streamState
}

// DecodeHybridHighband builds the opus_res SILK lowband (INT16TORES: int16 <<
// RES_SHIFT) from the resampled int16 SILK output, then accumulates the integer
// CELT highband (start band 17) onto it from a clone of the shared range
// decoder, matching libopus celt_decode_with_ec with celt_accum=1. The combined
// opus_res output is stashed in the stream's fixedHybridRes for
// decodePacketToResFixed.
//
// rd is supplied already positioned at the CELT start band: the float Hybrid
// afterSilk callback has consumed the Opus-layer redundancy flag and shrunk the
// storage by any trailing redundancy bytes (per the FixedHybridHighband
// contract), so the highband reads from the correct bit position without
// re-parsing the flag. A redundant frame (recorded by afterSilk in
// fixedHybridRedundant) drives a distinct decode and crossfade the integer
// hybrid path does not reproduce, so it declines.
func (h *streamFixedHybridHook) DecodeHybridHighband(silkInt16 []int16, filled int, rd *rangecoding.Decoder, frameSizeAPI, frameSize48 int, packetStereo bool) {
	d := h.st
	channels := int(d.channels)
	needed := frameSizeAPI * channels

	if d.fixedHybridRedundant {
		d.fixedHybridHandled = false
		return
	}

	if cap(d.fixedHybridRes) < needed {
		d.fixedHybridRes = make([]int32, needed)
	}
	res := d.fixedHybridRes[:needed]
	// INT16TORES(a) = SHL32(EXTEND32(a), RES_SHIFT); RES_SHIFT == 8.
	for i := 0; i < needed; i++ {
		var s int16
		if i < filled && i < len(silkInt16) {
			s = silkInt16[i]
		}
		res[i] = int32(s) << 8
	}

	downsample := 48000 / int(d.sampleRate)
	if downsample <= 0 {
		downsample = 1
	}
	coreFrameSize := frameSizeAPI * downsample

	rdClone := &d.fixedHybridRangeDecoder
	*rdClone = *rd
	handled := d.decodeFixedHybridAccum(rdClone, coreFrameSize, packetStereo, res)
	*rdClone = rangecoding.Decoder{}
	if !handled {
		d.fixedHybridHandled = false
		return
	}

	d.fixedHybridRes = res
	d.fixedHybridHandled = true
}

// floatToRes converts float32 PCM to opus_res via the lossless int16
// round-trip: opus_res = INT16TORES(int16) = int16 << RES_SHIFT (RES_SHIFT=8).
// This matches libopus' FIXED_POINT opus_res for integer-native SILK output and
// provides a faithful fallback for declined frames.
func floatToRes(res []int32, samples []float32) {
	n := len(res)
	if len(samples) < n {
		n = len(samples)
	}
	for i := 0; i < n; i++ {
		res[i] = int32(opusmath.Float32ToInt16(samples[i])) << 8
	}
	for i := n; i < len(res); i++ {
		res[i] = 0
	}
}
