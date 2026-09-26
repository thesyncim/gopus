package multistream

// routeProjectionMixingShortToStreams applies the projection encoder's short
// input callback (mapping_matrix_multiply_channel_in_short). The products are
// signed integer products converted to float32, accumulated in column order,
// and scaled once after the sum. The caller retains the original int16 PCM for
// tonality analysis; this routing produces only the coding PCM.
func (e *Encoder) routeProjectionMixingShortToStreams(scratch [][]float32, pcm []int16, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.projectionCols
	if e.mappingFamily != 3 || len(e.projectionMixing) < rows*cols || rows <= 0 || cols <= 0 {
		return nil
	}
	streamBuffers := ensureStreamBuffers(scratch, frameSize, e.coupledStreams, e.streams)
	const shortScale = float32(1.0 / (32768.0 * 32768.0))
	for sample := range frameSize {
		inputBase := sample * cols
		for row := 0; row < rows; row++ {
			mappingIdx := e.mapping[row]
			if mappingIdx == 255 {
				continue
			}
			streamIdx, chanInStream := resolveMapping(mappingIdx, e.coupledStreams)
			if streamIdx < 0 || streamIdx >= e.streams {
				continue
			}
			var sum float32
			for col := range cols {
				product := int32(e.projectionMixing[col*rows+row]) * int32(pcm[inputBase+col])
				sum += float32(product)
			}
			srcChannels := streamChannels(streamIdx, e.coupledStreams)
			streamBuffers[streamIdx][sample*srcChannels+chanInStream] = shortScale * sum
		}
	}
	return streamBuffers
}
