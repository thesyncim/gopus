package opusmath

func Float32ToInt16(x float32) int16 {
	y := x * 32768.0
	return Float32ToInt16Raw(y)
}

// Float32ToInt24 converts a float32 PCM sample to a signed 24-bit integer
// stored in int32, matching libopus RES2INT24 for float builds:
//
//	RES2INT24(a) = float2int(32768.f * 256.f * (a))  (celt/arch.h)
//
// Full scale is ±8388608 (= 2^23). No explicit saturation is applied;
// libopus does not soft-clip before this conversion in the 24-bit path.
func Float32ToInt24(x float32) int32 {
	return roundFloat32ToInt32Even(x * 8388608.0)
}

func Float32ToInt16Raw(y float32) int16 {
	if y > 32767.0 {
		return 32767
	}
	if y < -32768.0 {
		return -32768
	}
	return int16(roundFloat32ToInt32Even(y))
}

// Float32ToInt16OSCEOutputScale mirrors libopus OSCE SCALE_OUTPUT quantization.
// Unlike the generic PCM helper, OSCE clamps the negative rail to -32767.
func Float32ToInt16OSCEOutputScale(x float32) int16 {
	y := x * 32768.0
	if y > 32767.0 {
		return 32767
	}
	if y < -32767.0 {
		return -32767
	}
	return int16(roundFloat32ToInt32Even(y))
}

func roundFloat32ToInt32Even(y float32) int32 {
	i := int32(y)
	frac := y - float32(i)
	if frac > 0.5 || (frac == 0.5 && (i&1) != 0) {
		i++
	} else if frac < -0.5 || (frac == -0.5 && (i&1) != 0) {
		i--
	}
	return i
}
