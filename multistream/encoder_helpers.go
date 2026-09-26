package multistream

// ensureStreamBuffers grows dst to hold numStreams per-stream buffers of the
// required width (frameSize*channels). The backing slices are reused across
// calls to keep the encode hot path allocation-free; only growth past previous
// capacity allocates. Every sample is written by the routing below.
func ensureStreamBuffers(dst [][]float32, frameSize, coupledStreams, numStreams int) [][]float32 {
	if cap(dst) < numStreams {
		dst = make([][]float32, numStreams)
	}
	dst = dst[:numStreams]
	for i := range numStreams {
		need := frameSize * streamChannels(i, coupledStreams)
		if cap(dst[i]) < need {
			dst[i] = make([]float32, need)
		}
		dst[i] = dst[i][:need]
	}
	return dst
}

// streamSourceChannels returns the input channels stream s reads, as libopus
// get_left_channel/get_right_channel (coupled) or get_mono_channel (uncoupled)
// with prev = -1 return them: the first input channel whose mapping entry
// selects the stream channel. c2 is -1 for a mono stream.
func streamSourceChannels(mapping []byte, coupledStreams, s int) (c1, c2 int) {
	first := func(id int) int {
		for i, v := range mapping {
			if int(v) == id {
				return i
			}
		}
		return -1
	}
	if s < coupledStreams {
		return first(2 * s), first(2*s + 1)
	}
	return first(coupledStreams + s), -1
}

// routeChannelsToStreams is copy_channel_in over every stream of
// opus_multistream_encode_native(): each stream reads its source input
// channels from the interleaved input into its own (interleaved) buffer.
func routeChannelsToStreams(
	scratch [][]float32,
	input []float32,
	mapping []byte,
	coupledStreams int,
	frameSize int,
	inputChannels int,
	numStreams int,
) [][]float32 {
	streamBuffers := ensureStreamBuffers(scratch, frameSize, coupledStreams, numStreams)
	for s := range numStreams {
		c1, c2 := streamSourceChannels(mapping, coupledStreams, s)
		buf := streamBuffers[s]
		if s < coupledStreams {
			for i := range frameSize {
				buf[2*i] = input[i*inputChannels+c1]
				buf[2*i+1] = input[i*inputChannels+c2]
			}
		} else {
			for i := range frameSize {
				buf[i] = input[i*inputChannels+c1]
			}
		}
	}
	return streamBuffers
}

// routeChannels routes the caller frame to the per-stream buffers without
// projection mixing, as the tonality analysis downmix reads it.
func (e *Encoder) routeChannels(scratch [][]float32, pcm []float32, frameSize int) [][]float32 {
	return routeChannelsToStreams(scratch, pcm, e.mapping, e.coupledStreams, frameSize, e.inputChannels, e.streams)
}

// routeInputToStreams is the encoder's copy_channel_in: the projection encoder
// mixes the input through its mixing matrix, every other layout copies
// channels.
func (e *Encoder) routeInputToStreams(scratch [][]float32, in encodeInput, frameSize int) [][]float32 {
	if e.mappingFamily == 3 && len(e.projectionMixing) > 0 {
		if in.i16 != nil {
			return e.routeProjectionMixingShortToStreams(scratch, in.i16, frameSize)
		}
		return e.routeProjectionMixingToStreams(scratch, in.f32, frameSize)
	}
	return e.routeChannels(scratch, in.f32, frameSize)
}

// routeProjectionMixingToStreams ports the projection encoder's float
// copy_channel_in (mapping_matrix_multiply_channel_in_float): output row r of
// the column-major S16 mixing matrix feeds the stream channel whose source input
// channel is r, as tmp += matrix*input; out = (1/32768.f)*tmp. 16-bit input
// takes routeProjectionMixingShortToStreams.
func (e *Encoder) routeProjectionMixingToStreams(scratch [][]float32, pcm []float32, frameSize int) [][]float32 {
	rows := e.projectionRows
	cols := e.inputChannels
	matrix := e.projectionMixing
	streamBuffers := ensureStreamBuffers(scratch, frameSize, e.coupledStreams, e.streams)
	for s := range e.streams {
		stride := streamChannels(s, e.coupledStreams)
		c1, c2 := streamSourceChannels(e.mapping, e.coupledStreams, s)
		for ch, row := range [2]int{c1, c2} {
			if row < 0 || ch >= stride {
				continue
			}
			dst := streamBuffers[s][ch:]
			for i := range frameSize {
				var tmp float32
				for col := range cols {
					tmp += float32(matrix[rows*col+row]) * pcm[cols*i+col]
				}
				dst[stride*i] = (1.0 / 32768.0) * tmp
			}
		}
	}
	return streamBuffers
}

// writeStreamPacket writes one elementary stream packet at the head of dst,
// as opus_multistream_encode_native()'s repacketizer does: self-delimited
// (RFC 6716 Appendix B) for every stream but the last, which keeps standard
// framing. It returns the number of bytes written.
func writeStreamPacket(p *packetScratch, dst, packet []byte, last bool) (int, error) {
	if len(packet) == 0 {
		return 0, ErrInvalidPacket
	}
	if !last {
		return makeSelfDelimitedPacketInto(p, dst, packet)
	}
	if len(packet) > len(dst) {
		return 0, ErrBufferTooSmall
	}
	return copy(dst, packet), nil
}
