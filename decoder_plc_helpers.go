package gopus

import "github.com/thesyncim/gopus/internal/extsupport"

type plcDecodeState struct {
	packetFrameSize    int
	mode               Mode
	bandwidth          Bandwidth
	packetStereo       bool
	useDecoderPLCState bool
}

func (d *Decoder) plcFrameSize() int {
	if d.lastPacketDuration > 0 {
		return d.lastPacketDuration
	}
	if d.lastFrameSize > 0 {
		return d.lastFrameSize
	}
	return d.sampleRate / 50
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
	frameSize := sampleCount / d.channels
	if frameSize <= 0 {
		return 0, ErrBufferTooSmall
	}
	if frameSize > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}
	quantum := d.sampleRate / 400
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
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = d.sampleRate / 50
	}
	if frameSize > d.maxPacketSamples {
		return 0, ErrPacketTooLarge
	}

	needed := frameSize * d.channels
	if len(out) < needed {
		return 0, ErrBufferTooSmall
	}

	remaining := frameSize
	offset := 0
	chunkRate := 48000
	if state.mode == ModeSILK || state.mode == ModeCELT || state.mode == ModeHybrid {
		chunkRate = d.sampleRate
	}
	for remaining > 0 {
		chunk := nextPLCChunkSamples(chunkRate, state.mode, remaining)
		if chunk <= 0 {
			break
		}
		n, err := d.decodeOpusFrameIntoWithStatePolicy(
			out[offset*d.channels:],
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
	cleanupHook, usedHook := d.beginHybridDREDLowbandHook()
	defer cleanupHook()
	n, err := d.decodePLCChunksInto(out, frameSize, state)
	if err != nil {
		return 0, false, err
	}
	if usedHook() {
		return n, true, nil
	}
	return n, false, nil
}

func (d *Decoder) decodeDREDNeuralPLCChunksInto(out []float32, frameSize int, state plcDecodeState) (int, bool) {
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := nextPLCChunkSamples(d.sampleRate, state.mode, remaining)
		if chunk <= 0 {
			return offset, false
		}
		chunkStart := offset * d.channels
		chunkEnd := chunkStart + chunk*d.channels
		if chunkEnd > len(out) || !d.applyDREDNeuralConcealment(out[chunkStart:chunkEnd], chunk) {
			return offset, false
		}
		offset += chunk
		remaining -= chunk
	}
	return frameSize, true
}

func (d *Decoder) decodeDRED48kNeuralPLCInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	if d == nil {
		return 0, false, ErrInvalidArgument
	}
	if !extsupport.DREDRuntime {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = d.sampleRate / 50
	}
	if frameSize > d.maxPacketSamples {
		return 0, false, ErrPacketTooLarge
	}

	needed := frameSize * d.channels
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
