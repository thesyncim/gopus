package celt

import "math"

// celtExp2 approximates exp2(x) using libopus FLOAT_APPROX polynomial.
// This matches tmp_check/opus-1.6.1/celt/mathops.h (float path).
func celtExp2(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)

	res := exp2CoeffA0 + frac*(exp2CoeffA1+
		frac*(exp2CoeffA2+
			frac*(exp2CoeffA3+
				frac*(exp2CoeffA4+
					frac*exp2CoeffA5))))

	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

const (
	exp2CoeffA0 float32 = 9.999999403953552246093750000000e-01
	exp2CoeffA1 float32 = 6.931530833244323730468750000000e-01
	exp2CoeffA2 float32 = 2.401536107063293457031250000000e-01
	exp2CoeffA3 float32 = 5.582631751894950866699218750000e-02
	exp2CoeffA4 float32 = 8.989339694380760192871093750000e-03
	exp2CoeffA5 float32 = 1.877576694823801517486572265625e-03
)
