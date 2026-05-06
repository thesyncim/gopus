package gopus

// Encode encodes float32 PCM samples into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes.
//
// Returns the number of bytes written to data, or an error.
// When DTX is active during silence, returns a 1-byte TOC-only packet.
// Returns 0 bytes only when buffering (internal lookahead not yet filled).
//
// Buffer sizing: 4000 bytes is sufficient for any Opus packet.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	e.enc.SetFloatInputFrame(pcm)

	pcm64 := e.scratchPCM64[:len(pcm)]
	if e.enc.LSBDepth() != 24 {
		for i, v := range pcm {
			pcm64[i] = float64(v)
		}
	}

	packet, err := e.enc.Encode(pcm64, e.frameSize)
	e.enc.ClearFloatInputFrame()
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true

	return copyEncodedPacket(packet, data)
}

// EncodeInt16 encodes int16 PCM samples into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The samples are converted from int16 by dividing by 32768.
func (e *Encoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.Encode(pcm32, data)
}

// EncodeInt24 encodes 24-bit PCM samples stored in int32 values into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The input values are interpreted with the same semantics as libopus
// opus_encode24(): right-justified signed 24-bit PCM carried in int32
// containers with numeric range [-8388608, 8388607]. Left-shifted 24-in-32
// input will be mis-scaled.
func (e *Encoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	pcm64 := e.scratchPCM64[:len(pcm)]
	for i, v := range pcm {
		pcm64[i] = float64(v) / 8388608.0
	}

	packet, err := e.enc.Encode(pcm64, e.frameSize)
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true

	return copyEncodedPacket(packet, data)
}

// EncodeFloat32 encodes float32 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use Encode with a pre-allocated buffer.
//
// pcm: Input samples (interleaved if stereo).
//
// Returns the encoded packet or an error.
func (e *Encoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes int16 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt16 with a pre-allocated buffer.
//
// pcm: Input samples (interleaved if stereo).
//
// Returns the encoded packet or an error.
func (e *Encoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes 24-bit PCM samples stored in int32 values and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt24 with a pre-allocated buffer.
func (e *Encoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}
