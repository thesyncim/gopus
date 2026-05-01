//go:build !arm64 || purego

package celt

func expRotation1Stride1(x []float64, length int, c, s float64) {
	ms := -s
	end := length - 1
	i := 0
	for ; i+1 < end; i += 2 {
		x1 := x[i]
		x2 := x[i+1]
		x[i+1] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i+1]
		x4 := x[i+2]
		x[i+2] = c*x4 + s*x3
		x[i+1] = c*x3 + ms*x4
	}
	for ; i < end; i++ {
		x1 := x[i]
		x2 := x[i+1]
		x[i+1] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
	i = length - 3
	for ; i-1 >= 0; i -= 2 {
		x1 := x[i]
		x2 := x[i+1]
		x[i+1] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i-1]
		x4 := x[i]
		x[i] = c*x4 + s*x3
		x[i-1] = c*x3 + ms*x4
	}
	for ; i >= 0; i-- {
		x1 := x[i]
		x2 := x[i+1]
		x[i+1] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
}
