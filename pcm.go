package gopus

import "math"

func float32ToInt16(sample float32) int16 {
	scaled := float64(sample) * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(scaled))
}

func float64ToInt16(sample float64) int16 {
	scaled := sample * 32768.0
	if scaled > 32767.0 {
		return 32767
	}
	if scaled < -32768.0 {
		return -32768
	}
	return int16(math.RoundToEven(scaled))
}
