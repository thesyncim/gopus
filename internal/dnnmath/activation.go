package dnnmath

import (
	"math"
	"runtime"
)

var useNEONApproxActivation = runtime.GOARCH == "arm64"

// SigmoidApprox mirrors libopus' DNN ACTIVATION_SIGMOID path.
func SigmoidApprox(x float32) float32 {
	if useNEONApproxActivation {
		return sigmoidApproxNEON(x)
	}
	return SigmoidScalarApprox(x)
}

// TanhApprox mirrors libopus' DNN ACTIVATION_TANH path.
func TanhApprox(x float32) float32 {
	if useNEONApproxActivation {
		return tanhApproxNEON(x)
	}
	return TanhScalarApprox(x)
}

// SigmoidScalarApprox mirrors libopus' generic DNN sigmoid path.
func SigmoidScalarApprox(x float32) float32 {
	return 0.5 + 0.5*TanhScalarApprox(0.5*x)
}

// TanhScalarApprox mirrors libopus' generic DNN tanh path.
func TanhScalarApprox(x float32) float32 {
	const (
		n0 = 952.52801514
		n1 = 96.39235687
		n2 = 0.60863042
		d0 = 952.72399902
		d1 = 413.36801147
		d2 = 11.88600922
	)
	x2 := x * x
	num := ((n2*x2 + n1) * x2) + n0
	den := ((d2*x2 + d1) * x2) + d0
	y := num * x / den
	if y < -1 {
		return -1
	}
	if y > 1 {
		return 1
	}
	return y
}

// ExpApprox mirrors libopus' DNN lpcnet_exp() helper used by ACTIVATION_EXP.
func ExpApprox(x float32) float32 {
	return Exp2Approx(x * 1.44269504)
}

// Exp2Approx mirrors libopus' DNN lpcnet_exp2() cubic approximation.
func Exp2Approx(x float32) float32 {
	integer := int(math.Floor(float64(x)))
	if integer < -50 {
		return 0
	}
	frac := x - float32(integer)
	res := fma32(frac, fma32(frac, fma32(float32(0.078024523), frac, float32(0.22606716)), float32(0.69583354)), float32(0.99992522))
	bits := math.Float32bits(res)
	bits = (bits + uint32(int32(integer)<<23)) & 0x7fffffff
	return math.Float32frombits(bits)
}

// SoftmaxApprox mirrors libopus' DNN ACTIVATION_SOFTMAX path.
func SoftmaxApprox(out, in []float32, n int) {
	var sum float32
	for i := 0; i < n; i++ {
		out[i] = ExpApprox(in[i])
		sum += out[i]
	}
	scale := 1 / (sum + 1e-30)
	for i := 0; i < n; i++ {
		out[i] *= scale
	}
}

func sigmoidApproxNEON(x float32) float32 {
	const (
		n0 = float32(238.13200378)
		n1 = float32(6.02452230)
		n2 = float32(0.00950985)
		d0 = float32(952.72399902)
		d1 = float32(103.34200287)
		d2 = float32(0.74287558)
	)
	x2 := x * x
	num := fma32(x2, fma32(n2, x2, n1), n0)
	den := fma32(x2, fma32(d2, x2, d1), d0)
	y := float32(0.5) + (num*x)*reciprocalEstimate32(den)
	if y < 0 {
		return 0
	}
	if y > 1 {
		return 1
	}
	return y
}

func tanhApproxNEON(x float32) float32 {
	const (
		n0 = float32(952.52801514)
		n1 = float32(96.39235687)
		n2 = float32(0.60863042)
		d0 = float32(952.72399902)
		d1 = float32(413.36801147)
		d2 = float32(11.88600922)
	)
	x2 := x * x
	num := fma32(x2, fma32(n2, x2, n1), n0)
	den := fma32(x2, fma32(d2, x2, d1), d0)
	y := (num * x) * reciprocalEstimate32(den)
	if y < -1 {
		return -1
	}
	if y > 1 {
		return 1
	}
	return y
}

func fma32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}
