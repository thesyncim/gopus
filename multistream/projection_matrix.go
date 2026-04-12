package multistream

func applyProjectionMatrix(dst, src, matrix, frame []float64, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		in := src[s*cols : (s+1)*cols]
		out := dst[s*rows : (s+1)*rows]
		copy(frame, in)
		for row := 0; row < rows; row++ {
			sum := 0.0
			for col := 0; col < cols; col++ {
				sum += matrix[col*rows+row] * frame[col]
			}
			out[row] = sum
		}
	}
}
