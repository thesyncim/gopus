package gopus

type plcDecodeState struct {
	packetFrameSize    int
	mode               Mode
	bandwidth          Bandwidth
	packetStereo       bool
	useDecoderPLCState bool
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
		frameSize = 960
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
	for remaining > 0 {
		chunk := nextPLCChunkSamples(d.sampleRate, state.mode, remaining)
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

func (d *Decoder) decodeDRED48kNeuralPLCInto(out []float32, frameSize int, state plcDecodeState) (int, bool, error) {
	if d == nil {
		return 0, false, ErrInvalidArgument
	}
	if frameSize <= 0 {
		frameSize = state.packetFrameSize
	}
	if frameSize <= 0 {
		frameSize = 960
	}
	if frameSize > d.maxPacketSamples {
		return 0, false, ErrPacketTooLarge
	}

	needed := frameSize * d.channels
	if len(out) < needed {
		return 0, false, ErrBufferTooSmall
	}
	if d.sampleRate != 48000 || d.channels != 1 || state.mode != ModeCELT {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	d.prepareDRED48kNeuralEntry(frameSize, state.mode)
	if !d.applyDREDNeuralConcealment(out[:needed], frameSize) {
		n, err := d.decodePLCChunksInto(out, frameSize, state)
		return n, false, err
	}
	return frameSize, true, nil
}
