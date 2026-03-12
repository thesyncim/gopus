package gopus

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
	frameSize := d.lastFrameSize
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	samples, err := d.dec.DecodeToFloat32(data, frameSize)
	if err != nil {
		return 0, err
	}

	copy(pcm, samples)

	if data != nil && len(data) > 0 {
		d.lastFrameSize = frameSize
	}

	return frameSize, nil
}

// DecodeInt16 decodes an Opus multistream packet into int16 PCM samples.
//
// data: Opus multistream packet data, or nil for PLC.
// pcm: Output buffer for decoded samples.
//
// Returns the number of samples per channel decoded, or an error.
func (d *MultistreamDecoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	frameSize := d.lastFrameSize
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	samples, err := d.dec.Decode(data, frameSize)
	if err != nil {
		return 0, err
	}

	for i := 0; i < frameSize*d.channels; i++ {
		pcm[i] = float64ToInt16(samples[i])
	}

	if data != nil && len(data) > 0 {
		d.lastFrameSize = frameSize
	}

	return frameSize, nil
}
