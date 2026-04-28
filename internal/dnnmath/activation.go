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
	return 0.5 + 0.5*TanhApprox(0.5*x)
}

// TanhApprox mirrors libopus' DNN ACTIVATION_TANH path.
func TanhApprox(x float32) float32 {
	if useNEONApproxActivation {
		return tanhApproxNEON(x)
	}
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
