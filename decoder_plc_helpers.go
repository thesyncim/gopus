package gopus

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/silk"
)

type plcDecodeState struct {
	packetFrameSize    int
	mode               Mode
	bandwidth          Bandwidth
	packetStereo       bool
	useDecoderPLCState bool
}

// plcOutputFrameSize returns the per-channel frame size requested for PLC/FEC
// concealment, derived from the output buffer length (libopus frame_size arg).
func (d *Decoder) plcOutputFrameSize(pcmSampleCount int) (int, error) {
	return d.requestedOutputFrameSize(pcmSampleCount)
}

func (d *Decoder) requestedOutputFrameSize(sampleCount int) (int, error) {
	if d.channels <= 0 {
		return 0, ErrInvalidChannels
	}
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if sampleCount < channels {
		return 0, ErrBufferTooSmall
	}
	if sampleCount%channels != 0 {
		return 0, ErrInvalidFrameSize
	}
	frameSize := sampleCount / channels
	if frameSize <= 0 {
		return 0, ErrBufferTooSmall
	}
	quantum := sampleRate / 400
	if quantum <= 0 || frameSize%quantum != 0 {
		return 0, ErrInvalidFrameSize
	}
	return frameSize, nil
}

func nextPLCChunkSamples(sampleRate int, mode Mode, remaining int) int {
	if sampleRate <= 0 || remaining <= 0 {
		return 0
	}
	f20 := sampleRate / 50
	f10 := f20 / 2
	f5 := f10 / 2
	if remaining >= f20 {
		return f20
	}
	if remaining > f10 {
		return f10
	}
	if mode != ModeSILK && remaining > f5 && remaining < f10 {
		return f5
	}
	return remaining
}

func (d *Decoder) decodePLCChunksInto(out []float32, frameSize int, state plcDecodeState) (int, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = sampleRate / 50
	}
	needed := frameSize * channels
	if len(out) < needed {
		return 0, ErrBufferTooSmall
	}

	remaining := frameSize
	offset := 0
	chunkRate := 48000
	if state.mode == ModeSILK || state.mode == ModeCELT || state.mode == ModeHybrid {
		chunkRate = sampleRate
	}
	for remaining > 0 {
		chunk := nextPLCChunkSamples(chunkRate, state.mode, remaining)
		if chunk <= 0 {
			break
		}
		n, err := d.decodeOpusFrameIntoWithStatePolicy(
			out[offset*channels:],
			nil,
			chunk,
			state.packetFrameSize,
			state.mode,
			state.bandwidth,
			state.packetStereo,
			state.useDecoderPLCState,
		)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		offset += n
		remaining -= n
	}

	return frameSize, nil
}

func (d *Decoder) decodeHybridDRED48kInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	if !extsupport.DREDRuntime {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	d.primeHybridDREDEntryHistory(frameSize)
	queued := d.prepareCachedDREDNeuralConcealment(frameSize)
	var lowbandSnapshot *silk.DeepPLCLowbandSnapshot
	if d.dredNeuralConcealmentAvailable() && d.silkDecoder != nil {
		lowbandSnapshot = d.silkDecoder.SnapshotDeepPLCLowbandMono()
	}
	cleanupHook, usedHook := d.beginHybridDREDLowbandHook()
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(out, frameSize, state)
	if err != nil {
		return 0, false, err
	}
	if usedHook() {
		d.finishActiveDREDRecovery(n)
		return n, true, nil
	}
	if queued.NeededFeatureFrames > 0 || queued.RecoverableFeatureFrames > 0 || queued.MissingPositiveFrames > 0 {
		if d.advanceHybridDREDLowbandState(n, lowbandSnapshot) {
			return n, true, nil
		}
	}
	return n, false, nil
}

func (d *Decoder) decodeDREDNeuralPLCChunksInto(out []float32, frameSize int, state plcDecodeState) (int, bool) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	queued := d.prepareDRED48kNeuralEntry(frameSize, state.mode, false)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := nextPLCChunkSamples(sampleRate, state.mode, remaining)
		if chunk <= 0 {
			return offset, false
		}
		chunkStart := offset * channels
		chunkEnd := chunkStart + chunk*channels
		if chunkEnd > len(out) || !d.applyPreparedDREDNeuralConcealment(out[chunkStart:chunkEnd], chunk) {
			return offset, false
		}
		offset += chunk
		remaining -= chunk
	}
	if queued.NeededFeatureFrames > 0 || queued.RecoverableFeatureFrames > 0 || queued.MissingPositiveFrames > 0 {
		d.finishActiveDREDRecovery(frameSize)
	}
	return frameSize, true
}

func (d *Decoder) decodeDRED48kNeuralPLCInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	if d == nil {
		return 0, false, ErrInvalidArgument
	}
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if !extsupport.DREDRuntime {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = sampleRate / 50
	}
	needed := frameSize * channels
	if len(out) < needed {
		return 0, false, ErrBufferTooSmall
	}
	if !d.dredNeuralConcealmentAvailable() {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if d.channels < 1 || d.channels > 2 || (state.mode != ModeCELT && state.mode != ModeHybrid) {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if state.mode == ModeHybrid {
		return d.decodeHybridDRED48kInto(out, frameSize, state)
	}
	n, ok := d.decodeDREDNeuralPLCChunksInto(out[:needed], frameSize, state)
	if !ok {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	return n, true, nil
}
