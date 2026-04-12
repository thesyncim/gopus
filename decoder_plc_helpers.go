package gopus

type plcDecodeState struct {
	packetFrameSize    int
	mode               Mode
	bandwidth          Bandwidth
	packetStereo       bool
	useDecoderPLCState bool
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
		chunk := min(remaining, 48000/50)
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
