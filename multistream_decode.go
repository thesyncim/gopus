package gopus

import "github.com/thesyncim/gopus/multistream"

func (d *MultistreamDecoder) requestedOutputFrameSize(sampleCount int) (int, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	if channels <= 0 {
		return 0, ErrInvalidChannels
	}
	if sampleCount < channels {
		return 0, ErrBufferTooSmall
	}
	if sampleCount%channels != 0 {
		return 0, ErrInvalidFrameSize
	}
	frameSize := sampleCount / channels
	if frameSize <= 0 {
		return 0, ErrBufferTooSmall
	}
	maxPLCFrameSize := sampleRate / 25 * 3
	if maxPLCFrameSize <= 0 {
		return 0, ErrInvalidFrameSize
	}
	if frameSize > maxPLCFrameSize {
		return maxPLCFrameSize, nil
	}
	quantum := sampleRate / 400
	if quantum <= 0 || frameSize%quantum != 0 {
		return 0, ErrInvalidFrameSize
	}
	return frameSize, nil
}

func (d *MultistreamDecoder) decodeFrameSize(data []byte, sampleCount int) (int, error) {
	if data == nil {
		return d.requestedOutputFrameSize(sampleCount)
	}
	if len(data) == 0 {
		return 0, multistream.ErrPacketTooShort
	}
	return multistream.PacketDurationAtRate(data, d.dec.Streams(), int(d.sampleRate))
}

func (d *MultistreamDecoder) nextPLCChunkSamples(remaining int) int {
	chunk := int(d.sampleRate) / 50
	if chunk <= 0 || remaining < chunk {
		return remaining
	}
	return chunk
}

func (d *MultistreamDecoder) decodePLCFloat32Into(pcm []float32, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		samples, err := d.dec.DecodeToFloat32(nil, chunk)
		if err != nil {
			return err
		}
		total := chunk * channels
		if len(samples) < total || offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		copy(pcm[offset:offset+total], samples[:total])
		offset += total
		remaining -= chunk
	}
	return nil
}

func (d *MultistreamDecoder) decodePLCInt16Into(pcm []int16, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		samples, err := d.dec.DecodeToFloat32(nil, chunk)
		if err != nil {
			return err
		}
		total := chunk * channels
		if len(samples) < total || offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		float32ToInt16NoSoftClipScalar(pcm[offset:offset+total], samples[:total], chunk, channels)
		offset += total
		remaining -= chunk
	}
	return nil
}

// Decode decodes an Opus multistream packet into float32 PCM samples.
//
// data: Opus multistream packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Must be large enough to hold
// frameSize * channels samples.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters.
func (d *MultistreamDecoder) Decode(data []byte, pcm []float32) (int, error) {
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if data == nil {
		if err := d.decodePLCFloat32Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	samples, err := d.dec.DecodeToFloat32(data, frameSize)
	if err != nil {
		return 0, err
	}

	copy(pcm, samples)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return len(samples) / channels, nil
}

// DecodeInt16 decodes an Opus multistream packet into int16 PCM samples.
//
// data: Opus multistream packet data, or nil for PLC.
// pcm: Output buffer for decoded samples.
//
// Returns the number of samples per channel decoded, or an error.
func (d *MultistreamDecoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if data == nil {
		if err := d.decodePLCInt16Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	samples, err := d.dec.DecodeToFloat32(data, frameSize)
	if err != nil {
		return 0, err
	}

	total := frameSize * channels
	softClipAndFloat32ToInt16Scalar(pcm, samples, frameSize, channels, d.softClipMem)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return total / channels, nil
}
