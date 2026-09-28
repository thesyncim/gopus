package multistream

import "github.com/thesyncim/gopus/internal/opusmath"

// DecodeToInt24 decodes a multistream frame into right-justified signed
// 24-bit PCM. Projection decoders demix each decoded stream channel in the
// same integer domain used by libopus opus_projection_decode24.
func (d *Decoder) DecodeToInt24(data []byte, frameSize int) ([]int32, error) {
	if frameSize <= 0 {
		return nil, ErrInvalidPacket
	}
	frameSize = min(frameSize, int(d.sampleRate)*3/25)

	if len(d.projectionDemixing) != 0 && d.projectionCols > 0 {
		if pcm, handled, err := d.decodeFixedProjectionInt24(data, frameSize); err != nil {
			return nil, err
		} else if handled {
			return pcm, nil
		}
		return d.decodeProjectionInt24FromFloat(data, frameSize)
	}

	if pcm, handled, err := d.decodeFixedOutputInt24(data, frameSize); err != nil {
		return nil, err
	} else if handled {
		return pcm, nil
	}

	channels := d.outputChannels
	samples := d.outputScratchFor(frameSize * channels)
	n, err := d.decodeToFloat32Into(data, frameSize, false, false, samples)
	if err != nil {
		return nil, err
	}
	pcm := make([]int32, n*channels)
	for i, sample := range samples[:n*channels] {
		pcm[i] = opusmath.Float32ToInt24(sample)
	}
	return pcm, nil
}

func (d *Decoder) decodeProjectionInt24FromFloat(data []byte, frameSize int) ([]int32, error) {
	rows := d.outputChannels
	cols := d.projectionCols
	if rows <= 0 || cols <= 0 || cols > rows {
		return nil, ErrInvalidProjectionMatrix
	}
	source := d.outputScratchFor(frameSize * rows)
	n, err := d.decodeToFloat32Into(data, frameSize, false, false, source)
	if err != nil {
		return nil, err
	}
	needed := n * rows
	if cap(d.projectionInt24Scratch) < needed {
		d.projectionInt24Scratch = make([]int32, needed)
	}
	input := d.projectionInt24Scratch[:needed]
	for i, sample := range source[:needed] {
		input[i] = opusmath.Float32ToInt24(sample)
	}
	output := make([]int32, needed)
	applyProjectionDemixingResInt24(output, input, d.projectionDemixing, n, rows, cols)
	return output, nil
}
