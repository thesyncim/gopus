package opusmath

func Float32ToInt16(x float32) int16 {
	y := x * 32768.0
	if y > 32767.0 {
		return 32767
	}
	if y < -32768.0 {
		return -32768
	}

	i := int32(y)
	frac := y - float32(i)
	if frac > 0.5 || (frac == 0.5 && (i&1) != 0) {
		i++
	} else if frac < -0.5 || (frac == -0.5 && (i&1) != 0) {
		i--
	}
	return int16(i)
}
