package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

func float32ToInt16(sample float32) int16 {
	return opusFloatToInt16(sample)
}

func float64ToInt16(sample float64) int16 {
	return opusFloatToInt16(float32(sample))
}

func opusFloatToInt16(sample float32) int16 {
	return opusmath.Float32ToInt16(sample)
}
