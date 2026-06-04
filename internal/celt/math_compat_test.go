package celt

func celtAtan2pNorm(y, x float64) float64 {
	return float64(celtAtan2pNormF32(float32(y), float32(x)))
}

func celtAtanNorm(x float64) float64 {
	return float64(celtAtanNormF32(float32(x)))
}

func celtCosNorm2(x float64) float64 {
	return float64(celtCosNorm2F32(float32(x)))
}
