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
	// A nil OR zero-length packet is packet loss: libopus opus_multistream_decode
	// sets do_plc=1 for len==0 (opus_multistream_decoder.c:213) and conceals the
	// requested output frame size, exactly as for a NULL packet.
	if len(data) == 0 {
		return d.requestedOutputFrameSize(sampleCount)
	}
	return d.dec.PacketDurationAtRate(data)
}

func (d *MultistreamDecoder) decodeScratchFor(n int) []float32 {
	if cap(d.decodeScratch) < n {
		d.decodeScratch = make([]float32, n)
	}
	return d.decodeScratch[:n]
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
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		n, err := d.dec.DecodeIntoFloat32(nil, pcm[offset:offset+total], chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
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
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		samples := d.decodeScratchFor(total)
		n, err := d.dec.DecodeIntoFloat32(nil, samples, chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
		float32ToInt16NoSoftClipScalar(pcm[offset:offset+total], samples, chunk, channels)
		offset += total
		remaining -= chunk
	}
	return nil
}

func (d *MultistreamDecoder) decodePLCInt24Into(pcm []int32, frameSize int) error {
	channels := int(d.channels)
	remaining := frameSize
	offset := 0
	for remaining > 0 {
		chunk := d.nextPLCChunkSamples(remaining)
		if chunk <= 0 {
			return ErrInvalidFrameSize
		}
		total := chunk * channels
		if offset+total > len(pcm) {
			return ErrBufferTooSmall
		}
		samples := d.decodeScratchFor(total)
		n, err := d.dec.DecodeIntoFloat32(nil, samples, chunk)
		if err != nil {
			return err
		}
		if n != chunk {
			return ErrBufferTooSmall
		}
		float32ToInt24Slice(pcm[offset:offset+total], samples, chunk, channels)
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
	if len(data) != 0 {
		if len(pcm) < channels {
			return 0, ErrBufferTooSmall
		}
		n, err := d.dec.DecodeIntoFloat32(data, pcm, len(pcm)/channels)
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		if err != nil {
			return 0, err
		}
		d.lastFrameSize = int32(n)
		return n, nil
	}
	frameSize, err := d.requestedOutputFrameSize(len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}
	if err := d.decodePLCFloat32Into(pcm[:needed], frameSize); err != nil {
		return 0, err
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
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if len(data) == 0 {
		if err := d.decodePLCInt16Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	if handled, err := d.fixedDecodeInt16(data, pcm, frameSize); err != nil {
		return 0, err
	} else if handled {
		d.lastFrameSize = int32(frameSize)
		return frameSize, nil
	}

	samples := d.decodeScratchFor(needed)
	n, err := d.dec.DecodeIntoFloat32(data, samples, frameSize)
	if err != nil {
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		return 0, err
	}
	total := n * channels
	softClipAndFloat32ToInt16Scalar(pcm[:total], samples[:total], n, channels, d.softClipMem)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return n, nil
}

// DecodeInt24 decodes an Opus multistream packet into 24-bit PCM samples
// stored in int32.
//
// data: Opus multistream packet data, or nil for PLC.
// pcm: Output buffer for decoded samples. Each element carries a right-justified
// signed 24-bit value in the range [-8388608, 8388607] (= ±2^23), matching
// libopus opus_multistream_decode24().
//
// Returns the number of samples per channel decoded, or an error.
func (d *MultistreamDecoder) DecodeInt24(data []byte, pcm []int32) (int, error) {
	channels := int(d.channels)
	frameSize, err := d.decodeFrameSize(data, len(pcm))
	if err != nil {
		return 0, err
	}
	needed := frameSize * channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	if len(data) == 0 {
		if err := d.decodePLCInt24Into(pcm[:needed], frameSize); err != nil {
			return 0, err
		}
		return frameSize, nil
	}

	if handled, err := d.fixedDecodeInt24(data, pcm, frameSize); err != nil {
		return 0, err
	} else if handled {
		d.lastFrameSize = int32(frameSize)
		return frameSize, nil
	}

	samples := d.decodeScratchFor(needed)
	n, err := d.dec.DecodeIntoFloat32(data, samples, frameSize)
	if err != nil {
		if err == multistream.ErrBufferTooSmall {
			return 0, ErrBufferTooSmall
		}
		return 0, err
	}
	total := n * channels
	float32ToInt24Slice(pcm[:total], samples[:total], n, channels)

	if len(data) > 0 {
		d.lastFrameSize = int32(frameSize)
	}

	return n, nil
}

// DecodeInt24Slice decodes an Opus multistream packet into 24-bit PCM samples
// and returns a new int32 slice. Each element carries a right-justified signed
// 24-bit value.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use DecodeInt24 with a pre-allocated buffer.
func (d *MultistreamDecoder) DecodeInt24Slice(data []byte) ([]int32, error) {
	channels := int(d.channels)
	sampleRate := int(d.sampleRate)
	// 60 ms is the maximum Opus frame duration; allocate a buffer large
	// enough for any valid packet, then trim to the actual decoded length.
	maxFrameSize := sampleRate * 60 / 1000
	pcm := make([]int32, maxFrameSize*channels)
	n, err := d.DecodeInt24(data, pcm)
	if err != nil {
		return nil, err
	}
	return pcm[:n*channels], nil
}
