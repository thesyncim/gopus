package multistream

import "github.com/thesyncim/gopus/internal/opusmath"

func applyProjectionDemixingMatrix32(dst, src []float32, matrix []int16, frame []float32, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		inBase := s * rows
		outBase := s * rows
		for col := 0; col < cols; col++ {
			frame[col] = src[inBase+col]
		}
		for row := 0; row < rows; row++ {
			dst[outBase+row] = 0
		}
		for col := 0; col < cols; col++ {
			inputSample := frame[col]
			for row := 0; row < rows; row++ {
				tmp := float32(matrix[col*rows+row]) * inputSample * (1.0 / 32768.0)
				idx := outBase + row
				dst[idx] += tmp
			}
		}
	}
}

func applyProjectionDemixingMatrixInt16(dst []int16, src []float32, matrix []int16, frame []float32, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		inBase := s * rows
		outBase := s * rows
		for col := 0; col < cols; col++ {
			frame[col] = src[inBase+col]
		}
		for col := 0; col < cols; col++ {
			inputSample := int32(opusmath.Float32ToInt16(frame[col]))
			for row := 0; row < rows; row++ {
				tmp := int32(matrix[col*rows+row]) * inputSample
				idx := outBase + row
				dst[idx] = int16(int32(dst[idx]) + ((tmp + 16384) >> 15))
			}
		}
	}
}
