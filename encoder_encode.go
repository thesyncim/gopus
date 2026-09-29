package gopus

import (
	"errors"

	"github.com/thesyncim/gopus/internal/encoder"
)

func translateEncoderError(err error) error {
	if errors.Is(err, encoder.ErrBufferTooSmall) {
		return ErrBufferTooSmall
	}
	return err
}

// Encode encodes interleaved float32 PCM into data. pcm must contain the
// configured frame size times Channels samples. len(data) is the packet byte
// budget; Encode returns the number of bytes written or an error. The returned
// packet may be a one-byte DTX packet during silence.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	if e.is96kHz() {
		return e.encode96k(pcm, data, encoder.EncodeInputFloat32)
	}
	frameSizeArg := int(e.frameSize)
	channels := int(e.channels)
	expected := frameSizeArg * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputFloat32, frameSize)
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	if err != nil {
		return 0, err
	}
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm[:inputSamples], frameSize, pcm, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}

	return copyEncodedPacket(packet, data)
}

// encode96k handles Encode for a 96 kHz API-rate Encoder. The selected QEXT
// build routes native-rate PCM through the shared mode and history driver.
func (e *Encoder) encode96k(pcm []float32, data []byte, input encoder.EncodeInputFormat) (int, error) {
	if n, handled, err := e.tryEncodeNative96k(pcm, data, input); handled {
		return n, translateEncoderError(err)
	}
	return 0, ErrInvalidSampleRate
}

// EncodeInt16 encodes interleaved signed 16-bit PCM into data. pcm must contain
// the configured frame size times Channels samples. len(data) is the packet
// byte budget. Input samples are scaled by 1/32768.
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
		return e.encode96k(pcm32, data, encoder.EncodeInputInt16)
	}
	frameSize, err := selectExpertFrameSize(int(e.frameSize), e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputInt16, frameSize)
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	if err != nil {
		return 0, err
	}
	packet, err := e.enc.EncodeShortMixedWithAnalysisMaxBytes(pcm32[:frameSize*int(e.channels)], frameSize, pcm32, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}
	return copyEncodedPacket(packet, data)
}

// EncodeInt24 encodes interleaved signed 24-bit PCM into data. Each int32 in
// pcm must be right-justified in the range [-8388608, 8388607], and pcm must
// contain the configured frame size times Channels samples. len(data) is the
// packet byte budget.
func (e *Encoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	channels := int(e.channels)
	expected := e.apiFrameSize() * channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	if e.is96kHz() {
		pcm32 := e.convertInt24ToFloat32(pcm)
		return e.encode96k(pcm32, data, encoder.EncodeInputInt24)
	}

	frameSizeArg := int(e.frameSize)
	frameSize, err := selectExpertFrameSize(frameSizeArg, e.expertFrameDuration, e.application, e.internalSampleRate())
	e.enc.BeginEncodeCall(encoder.EncodeInputInt24, frameSize)
	if len(data) == 0 {
		return 0, ErrBufferTooSmall
	}
	if err != nil {
		return 0, err
	}
	pcm32 := e.convertInt24ToFloat32(pcm)
	inputSamples := frameSize * channels

	packet, err := e.enc.EncodeFloat32WithAnalysisMaxBytes(pcm32[:inputSamples], frameSize, pcm32, len(data))
	if err != nil {
		return 0, translateEncoderError(err)
	}

	return copyEncodedPacket(packet, data)
}

func (e *Encoder) convertInt24ToFloat32(pcm []int32) []float32 {
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 8388608.0
	}
	return pcm32
}

// EncodeFloat32 encodes interleaved float32 PCM and returns an owned packet
// slice.
func (e *Encoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.Encode(pcm, data)
	})
}

// EncodeInt16Slice encodes interleaved signed 16-bit PCM and returns an owned
// packet slice.
func (e *Encoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt16(pcm, data)
	})
}

// EncodeInt24Slice encodes interleaved right-justified signed 24-bit PCM stored
// in int32 values and returns an owned packet slice.
func (e *Encoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	return encodeToOwnedPacket(maxPacketBytesPerStream, func(data []byte) (int, error) {
		return e.EncodeInt24(pcm, data)
	})
}
