package opusmath

import "math"

// CeltExp2 matches libopus' celt_exp2() FLOAT_APPROX polynomial.
func CeltExp2(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)

	res := celtExp2CoeffA0 + frac*(celtExp2CoeffA1+
		frac*(celtExp2CoeffA2+
			frac*(celtExp2CoeffA3+
				frac*(celtExp2CoeffA4+
					frac*celtExp2CoeffA5))))

	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

const (
	celtExp2CoeffA0 float32 = 9.999999403953552246093750000000e-01
	celtExp2CoeffA1 float32 = 6.931530833244323730468750000000e-01
	celtExp2CoeffA2 float32 = 2.401536107063293457031250000000e-01
	celtExp2CoeffA3 float32 = 5.582631751894950866699218750000e-02
	celtExp2CoeffA4 float32 = 8.989339694380760192871093750000e-03
	celtExp2CoeffA5 float32 = 1.877576694823801517486572265625e-03
)
