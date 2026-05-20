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

func (e *Encoder) routeFloatInputToStreams(frameSize int) [][]float32 {
	if e == nil || len(e.floatInputFrame) < frameSize*e.inputChannels {
		return nil
	}
	if cap(e.floatInputStreams) < e.streams {
		e.floatInputStreams = make([][]float32, e.streams)
	}
	streams := e.floatInputStreams[:e.streams]

	total := 0
	for i := 0; i < e.streams; i++ {
		total += frameSize * streamChannels(i, e.coupledStreams)
	}
	if cap(e.floatInputScratch) < total {
		e.floatInputScratch = make([]float32, total)
	}
	scratch := e.floatInputScratch[:total]
	clear(scratch)

	offset := 0
	for i := 0; i < e.streams; i++ {
		count := frameSize * streamChannels(i, e.coupledStreams)
		streams[i] = scratch[offset : offset+count]
		offset += count
	}

	for outCh := 0; outCh < e.inputChannels; outCh++ {
		mappingIdx := e.mapping[outCh]
		if mappingIdx == 255 {
			continue
		}

		streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
		if streamIdx < 0 || streamIdx >= e.streams {
			continue
		}

		srcChannels := streamChannels(streamIdx, e.coupledStreams)
		dst := streams[streamIdx]
		for s := 0; s < frameSize; s++ {
			dst[s*srcChannels+chanInStream] = e.floatInputFrame[s*e.inputChannels+outCh]
		}
	}

	return streams
}

// SetFloatInputFrame exposes the current public float32 frame to stream encoders.
func (e *Encoder) SetFloatInputFrame(pcm []float32) {
	e.floatInputFrame = pcm
}

// ClearFloatInputFrame clears the per-call float32 input override.
func (e *Encoder) ClearFloatInputFrame() {
	e.floatInputFrame = nil
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
