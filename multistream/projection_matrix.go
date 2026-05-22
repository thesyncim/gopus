package multistream

import "github.com/thesyncim/gopus/internal/opusmath"

func applyProjectionDemixingMatrix(dst, src []float64, matrix []int16, frame []float32, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		inBase := s * rows
		outBase := s * rows
		for col := 0; col < cols; col++ {
			frame[col] = float32(src[inBase+col])
		}
		for row := 0; row < rows; row++ {
			dst[outBase+row] = 0
		}
		for col := 0; col < cols; col++ {
			inputSample := frame[col]
			for row := 0; row < rows; row++ {
				tmp := float32(float64(matrix[col*rows+row]) * float64(inputSample) * (1.0 / 32768.0))
				dst[outBase+row] = float64(float32(dst[outBase+row]) + tmp)
			}
		}
	}
}

func applyProjectionMixingMatrix(dst, src []float64, matrix []int16, frame []float32, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		inBase := s * cols
		outBase := s * rows
		for col := 0; col < cols; col++ {
			frame[col] = float32(src[inBase+col])
		}
		for row := 0; row < rows; row++ {
			var sum float32
			for col := 0; col < cols; col++ {
				sum += float32(matrix[col*rows+row]) * frame[col]
			}
			dst[outBase+row] = float64((1.0 / 32768.0) * sum)
		}
	}
}

func applyProjectionDemixingMatrixInt16(dst []int16, src []float64, matrix []int16, frame []float32, frameSize, rows, cols int) {
	for s := 0; s < frameSize; s++ {
		inBase := s * rows
		outBase := s * rows
		for col := 0; col < cols; col++ {
			frame[col] = float32(src[inBase+col])
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
