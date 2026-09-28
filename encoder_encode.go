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
// The output buffer length is the packet byte budget. Longer packets and
// optional extensions can require more than the recommended 4000 bytes.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	if e.is96kHz() {
		return e.encode96k(pcm, data)
	}
	frameSizeArg := int(e.frameSize)
	channels := int(e.channels)
	expected := frameSizeArg * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	if err != nil {
		return 0, err
	}
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm[:inputSamples], frameSize, pcm, len(data))
	if err != nil {
		return 0, err
	}

	return copyEncodedPacket(packet, data)
}

// encode96k handles Encode for a 96 kHz API-rate Encoder. The selected QEXT
// build routes native-rate PCM through the shared mode and history driver.
func (e *Encoder) encode96k(pcm []float32, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	if n, handled, err := e.tryEncodeNative96k(pcm, data); handled {
		return n, err
	}
	return 0, ErrInvalidSampleRate
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
	expected := e.apiFrameSize() * int(e.channels)
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.encodeInt16Packet(pcm32, data)
}

// encodeInt16Packet uses opus_encode_native's short-input policy, including
// the per-call 16-bit LSB-depth cap and short-input analysis callback.
func (e *Encoder) encodeInt16Packet(pcm32 []float32, data []byte) (int, error) {
	if e.is96kHz() {
		// opus_encode_native caps the configured LSB depth at 16 bits for the
		// short API before selecting the native 96 kHz CELT path.
		configuredDepth := e.enc.LSBDepth()
		if configuredDepth > 16 {
			e.enc.SetLSBDepth(16)
		}
		defer e.enc.SetLSBDepth(configuredDepth)
		return e.Encode(pcm32, data)
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	frameSize, err := selectExpertFrameSize(int(e.frameSize), e.expertFrameDuration, e.application, e.internalSampleRate())
	if err != nil {
		return 0, err
	}
	packet, err := e.enc.EncodeShortMixedWithAnalysisMaxBytes(pcm32[:frameSize*int(e.channels)], frameSize, pcm32, len(data))
	if err != nil {
		return 0, err
	}
	return copyEncodedPacket(packet, data)
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
	channels := int(e.channels)
	expected := e.apiFrameSize() * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}

	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 8388608.0
	}

	if e.is96kHz() {
		return e.encode96k(pcm32, data)
	}

	frameSizeArg := int(e.frameSize)
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	if err != nil {
		return 0, err
	}
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm32[:inputSamples], frameSize, pcm32, len(data))
	if err != nil {
		return 0, err
	}

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
