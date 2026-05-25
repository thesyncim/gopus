package opusmath

import "math"

// CeltLog2 matches libopus' celt_log2() FLOAT_APPROX polynomial.
func CeltLog2(x float32) float32 {
	bits := math.Float32bits(x)
	integer := int32(bits>>23) - 127
	bits = uint32(int32(bits) - int32(uint32(integer)<<23))

	rangeIdx := (bits >> 20) & 0x7
	f := math.Float32frombits(bits)
	f = f*celtLog2XNormCoeff[rangeIdx] - 1.0625
	f = celtLog2CoeffA0 + f*(celtLog2CoeffA1+f*(celtLog2CoeffA2+f*(celtLog2CoeffA3+f*celtLog2CoeffA4)))
	return float32(integer) + f + celtLog2YNormCoeff[rangeIdx]
}

// CeltExp2 matches libopus' celt_exp2() FLOAT_APPROX polynomial.
func CeltExp2(x float32) float32 {
	integer := int32(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)

	res := fma32(frac, fma32(frac, fma32(frac, fma32(frac, fma32(frac,
		celtExp2CoeffA5, celtExp2CoeffA4), celtExp2CoeffA3), celtExp2CoeffA2),
		celtExp2CoeffA1), celtExp2CoeffA0)

	bits := math.Float32bits(res)
	bits = uint32(int32(bits)+int32(uint32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

// CeltSinNormArg matches libopus' celt_sin() argument reduction expression.
func CeltSinNormArg(x float32) float32 {
	return float32((0.5 * 3.1415926535897931 * float64(x)) - 1)
}

// ISqrt32 returns floor(sqrt(x)) for libopus CELT integer math.
func ISqrt32(x uint32) uint32 {
	r := uint32(math.Sqrt(float64(x)))
	for uint64(r+1)*uint64(r+1) <= uint64(x) {
		r++
	}
	for uint64(r)*uint64(r) > uint64(x) {
		r--
	}
	return r
}

const (
	celtLog2CoeffA0 float32 = 8.74628424644470214843750000e-02
	celtLog2CoeffA1 float32 = 1.357829570770263671875000000000
	celtLog2CoeffA2 float32 = -6.3897705078125000000000000e-01
	celtLog2CoeffA3 float32 = 4.01971250772476196289062500e-01
	celtLog2CoeffA4 float32 = -2.8415444493293762207031250e-01

	celtExp2CoeffA0 float32 = 9.999999403953552246093750000000e-01
	celtExp2CoeffA1 float32 = 6.931530833244323730468750000000e-01
	celtExp2CoeffA2 float32 = 2.401536107063293457031250000000e-01
	celtExp2CoeffA3 float32 = 5.582631751894950866699218750000e-02
	celtExp2CoeffA4 float32 = 8.989339694380760192871093750000e-03
	celtExp2CoeffA5 float32 = 1.877576694823801517486572265625e-03
)

var celtLog2XNormCoeff = [8]float32{
	1.0000000000000000000000000000,
	8.88888895511627197265625e-01,
	8.00000000000000000000000e-01,
	7.27272748947143554687500e-01,
	6.66666686534881591796875e-01,
	6.15384638309478759765625e-01,
	5.71428596973419189453125e-01,
	5.33333361148834228515625e-01,
}

var celtLog2YNormCoeff = [8]float32{
	0.0000000000000000000000000000,
	1.699250042438507080078125e-01,
	3.219280838966369628906250e-01,
	4.594316184520721435546875e-01,
	5.849624872207641601562500e-01,
	7.004396915435791015625000e-01,
	8.073549270629882812500000e-01,
	9.068905711174011230468750e-01,
}
