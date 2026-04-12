package multistream

// SetProjectionDemixingMatrix sets optional projection demixing coefficients.
// Matrix data is S16LE, column-major, with dimensions:
//
//	rows = output channels
//	cols = streams + coupledStreams
//
// This method is intended for mapping-family-3 projection flows where
// decoded stream channels are routed with trivial mapping and then demixed
// to output channels.
func (d *Decoder) SetProjectionDemixingMatrix(matrix []byte) error {
	if len(matrix) == 0 {
		d.projectionDemixing = nil
		d.projectionCols = 0
		return nil
	}

	rows := d.outputChannels
	cols := d.streams + d.coupledStreams
	if rows <= 0 || cols <= 0 {
		return ErrInvalidProjectionMatrix
	}
	if len(matrix) != 2*rows*cols {
		return ErrInvalidProjectionMatrix
	}

	// Projection family decoders use trivial channel mapping.
	for i := 0; i < rows; i++ {
		if d.mapping[i] != byte(i) {
			return ErrInvalidProjectionMatrix
		}
	}

	needed := rows * cols
	if cap(d.projectionDemixing) < needed {
		d.projectionDemixing = make([]float64, needed)
	}
	coeffs := d.projectionDemixing[:needed]
	for i := 0; i < needed; i++ {
		v := int16(uint16(matrix[2*i]) | (uint16(matrix[2*i+1]) << 8))
		coeffs[i] = float64(v) / 32768.0
	}
	d.projectionCols = cols
	return nil
}

func (d *Decoder) applyProjectionDemixing(output []float64, frameSize int) {
	rows := d.outputChannels
	cols := d.projectionCols
	if len(d.projectionDemixing) == 0 || cols <= 0 || rows <= 0 || cols > rows {
		return
	}

	if cap(d.projectionScratch) < cols {
		d.projectionScratch = make([]float64, cols)
	}
	tmp := d.projectionScratch[:cols]

	for s := 0; s < frameSize; s++ {
		frame := output[s*rows : (s+1)*rows]
		copy(tmp, frame[:cols])
		for row := 0; row < rows; row++ {
			sum := 0.0
			for col := 0; col < cols; col++ {
				sum += d.projectionDemixing[col*rows+row] * tmp[col]
			}
			frame[row] = sum
		}
	}
}
