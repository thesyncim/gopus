package celt

func scalePulsesIntoScalar(out []celtNorm, pulses []int32, g float32) {
	out = out[:len(pulses)]
	for i, v := range pulses {
		out[i] = celtNorm(float32(v) * g)
	}
}
