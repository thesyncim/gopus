package multistream

// routeChannelsToStreams routes interleaved input to stream buffers.
// This is the inverse of applyChannelMapping in multistream.go.
//
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
// Output: slice of buffers, one per stream (stereo streams interleaved)
func routeChannelsToStreams(
	input []float32,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float32 {
	// Create buffer for each stream
	streamBuffers := make([][]float32, numStreams)
	for i := 0; i < numStreams; i++ {
		chans := streamChannels(i, coupledStreams)
		streamBuffers[i] = make([]float32, frameSize*chans)
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

func makeStreamBuffers(frameSize, coupledStreams, numStreams int) [][]float32 {
	streamBuffers := make([][]float32, numStreams)
	for i := 0; i < numStreams; i++ {
		chans := streamChannels(i, coupledStreams)
		streamBuffers[i] = make([]float32, frameSize*chans)
	}
	return streamBuffers
}

func (e *Encoder) routeInputToStreams(pcm []float32, frameSize int) [][]float32 {
	if e.mappingFamily == 3 {
		return e.routeProjectionMixingToStreams(pcm, frameSize)
	}
	return routeChannelsToStreams(pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)
}

func (e *Encoder) routeProjectionMixingToStreams(pcm []float32, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.projectionCols
	if len(e.projectionMixing) == 0 || rows <= 0 || cols <= 0 {
		return routeChannelsToStreams(pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)
	}
	if cap(e.projectionFrame) < cols {
		e.projectionFrame = make([]float32, cols)
	}
	frame := e.projectionFrame[:cols]
	streamBuffers := makeStreamBuffers(frameSize, e.coupledStreams, e.streams)

	for s := 0; s < frameSize; s++ {
		inBase := s * cols
		for col := 0; col < cols; col++ {
			frame[col] = float32(pcm[inBase+col])
		}
		for row := 0; row < rows && row < e.inputChannels; row++ {
			mappingIdx := e.mapping[row]
			if mappingIdx == 255 {
				continue
			}
			streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
			if streamIdx < 0 || streamIdx >= e.streams {
				continue
			}

			var sum float32
			for col := 0; col < cols; col++ {
				sum += float32(e.projectionMixing[col*rows+row]) * frame[col]
			}
			sample := (1.0 / 32768.0) * sum
			srcChannels := streamChannels(streamIdx, e.coupledStreams)
			streamBuffers[streamIdx][s*srcChannels+chanInStream] = sample
		}
	}

	return streamBuffers
}

// assembleMultistreamPacket combines individual stream packets into a multistream packet.
// Per RFC 6716 Appendix B:
//   - First N-1 packets use self-delimited packet framing
//   - Last packet uses standard framing
func assembleMultistreamPacket(streamPackets [][]byte) ([]byte, error) {
	if len(streamPackets) == 0 {
		return nil, nil
	}

	encoded := make([][]byte, len(streamPackets))
	totalSize := 0
	for i, packet := range streamPackets {
		if len(packet) == 0 {
			return nil, ErrInvalidPacket
		}

		if i < len(streamPackets)-1 {
			var err error
			packet, err = makeSelfDelimitedPacket(packet)
			if err != nil {
				return nil, err
			}
		}
		encoded[i] = packet
		totalSize += len(packet)
	}

	output := make([]byte, totalSize)
	offset := 0
	for _, packet := range encoded {
		copy(output[offset:], packet)
		offset += len(packet)
	}
	return output, nil
}
