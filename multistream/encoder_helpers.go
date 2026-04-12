package multistream

// routeChannelsToStreams routes interleaved input to stream buffers.
// This is the inverse of applyChannelMapping in multistream.go.
//
// Input format: sample-interleaved [ch0_s0, ch1_s0, ..., chN_s0, ch0_s1, ...]
// Output: slice of buffers, one per stream (stereo streams interleaved)
func routeChannelsToStreams(
	input []float64,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float64 {
	// Create buffer for each stream
	streamBuffers := make([][]float64, numStreams)
	for i := 0; i < numStreams; i++ {
		chans := streamChannels(i, coupledStreams)
		streamBuffers[i] = make([]float64, frameSize*chans)
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

func (e *Encoder) applyProjectionMixing(pcm []float64, frameSize int) []float64 {
	rows := e.projectionRows
	cols := e.projectionCols
	if len(e.projectionMixing) == 0 || rows <= 0 || cols <= 0 {
		return pcm
	}

	if cap(e.projectionScratch) < len(pcm) {
		e.projectionScratch = make([]float64, len(pcm))
	}
	mixed := e.projectionScratch[:len(pcm)]

	if cap(e.projectionFrame) < cols {
		e.projectionFrame = make([]float64, cols)
	}
	applyProjectionMatrix(mixed, pcm, e.projectionMixing, e.projectionFrame[:cols], frameSize, rows, cols)
	return mixed
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
