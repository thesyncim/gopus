package gopus

// Encode encodes float32 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes per stream.
//
// Returns the number of bytes written to data, or an error. Like libopus
// opus_multistream_encode_float(), len(data) is the packet budget every
// stream's allocation is carved from. The steady-state path is allocation-free.
func (e *MultistreamEncoder) Encode(pcm []float32, data []byte) (int, error) {
	frameSize, err := e.codedFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	n, err := e.enc.EncodeWithAnalysis(pcm[:frameSize*int(e.channels)], frameSize, pcm, data)
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true
	return n, nil
}

// codedFrameSize validates the caller frame length and returns the frame size
// frame_size_select() codes for it.
func (e *MultistreamEncoder) codedFrameSize(samples int) (int, error) {
	frameSizeArg := int(e.frameSize)
	if samples != frameSizeArg*int(e.channels) {
		return 0, ErrInvalidFrameSize
	}
	return selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, int(e.sampleRate))
}

// EncodeInt16 encodes int16 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error. It matches
// libopus opus_multistream_encode(): the samples are scaled by 1/32768 and the
// streams code them with a 16-bit LSB depth.
func (e *MultistreamEncoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	frameSize, err := e.codedFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	n, err := e.enc.EncodeInt16WithAnalysis(pcm[:frameSize*int(e.channels)], frameSize, pcm, data)
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true
	return n, nil
}

// EncodeInt24 encodes 24-bit PCM samples stored in int32 values into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The input values are interpreted as right-justified signed 24-bit PCM
// carried in int32 containers with numeric range [-8388608, 8388607].
// Left-shifted 24-in-32 input will be mis-scaled. INT24TORES scales exactly by
// 1/8388608 and opus_multistream_encode24() codes at the float path's 24-bit
// LSB depth, so the samples take the float path.
func (e *MultistreamEncoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	expected := int(e.frameSize) * int(e.channels)
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 8388608.0
	}
	return e.Encode(pcm32, data)
}

// EncodeFloat32 encodes float32 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use Encode with a pre-allocated buffer.
//
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes int16 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt16 with a pre-allocated buffer.
//
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes 24-bit PCM samples stored in int32 values and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
func (e *MultistreamEncoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream*e.enc.Streams(), func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}
