package celt

import (
	"math"

	"github.com/thesyncim/gopus/rangecoding"
)

// opPVQSearch approximates libopus op_pvq_search() (float path).
// It finds the signed pulse vector iy (sum abs = K) that best matches X.
// Returns the pulse vector and the computed energy yy (sum of squares of pulses).
// This matches libopus which returns yy from op_pvq_search_c() for use in normalization.
// NOTE: This function does NOT modify the input x slice.
func opPVQSearch(x []float64, k int) ([]int, float64) {
	n := len(x)
	iy := make([]int, n)
	if n == 0 || k <= 0 {
		return iy, 0
	}

	signx := make([]int, n)
	y := make([]float64, n)

	// Make a local copy of absolute values for the search.
	// We must NOT modify the input x slice.
	absX := make([]float64, n)
	for j := 0; j < n; j++ {
		if x[j] < 0 {
			signx[j] = 1
			absX[j] = -x[j]
		} else {
			absX[j] = x[j]
		}
	}

	xy := 0.0
	yy := 0.0
	pulsesLeft := k

	// Pre-search by projecting on the pyramid for large K.
	if k > (n >> 1) {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += absX[j]
		}

		// Guard against tiny/huge/invalid sums.
		if !(sum > 1e-15 && sum < 64.0) {
			absX[0] = 1.0
			for j := 1; j < n; j++ {
				absX[j] = 0.0
			}
			sum = 1.0
		}

		rcp := (float64(k) + 0.8) / sum
		for j := 0; j < n; j++ {
			iy[j] = int(math.Floor(rcp * absX[j]))
			y[j] = float64(iy[j])
			yy += y[j] * y[j]
			xy += absX[j] * y[j]
			y[j] *= 2
			pulsesLeft -= iy[j]
		}
	}

	if pulsesLeft > n+3 {
		tmp := float64(pulsesLeft)
		yy += tmp * tmp
		yy += tmp * y[0]
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	for i := 0; i < pulsesLeft; i++ {
		bestID := 0
		yy += 1

		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy

		for j := 1; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}

	for j := 0; j < n; j++ {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
	}

	return iy, yy
}

func opPVQSearchN2(x []float64, k, up int) (iy []int, upIy []int, refine int) {
	iy = make([]int, 2)
	upIy = make([]int, 2)
	if len(x) < 2 || k <= 0 || up <= 0 {
		if k > 0 {
			iy[0] = k
			upIy[0] = up * k
		}
		return iy, upIy, 0
	}

	sum := math.Abs(x[0]) + math.Abs(x[1])
	if sum < 1e-15 {
		iy[0] = k
		upIy[0] = up * k
		return iy, upIy, 0
	}

	rcp := 1.0 / sum
	iy[0] = int(math.Floor(0.5 + float64(k)*x[0]*rcp))
	upIy[0] = int(math.Floor(0.5 + float64(up*k)*x[0]*rcp))

	low := up*iy[0] - (up-1)/2
	high := up*iy[0] + (up-1)/2
	if upIy[0] < low {
		upIy[0] = low
	} else if upIy[0] > high {
		upIy[0] = high
	}

	offset := upIy[0] - up*iy[0]
	iy[1] = k - absInt(iy[0])
	upIy[1] = up*k - absInt(upIy[0])
	if x[1] < 0 {
		iy[1] = -iy[1]
		upIy[1] = -upIy[1]
		offset = -offset
	}
	refine = offset

	return iy, upIy, refine
}

func opPVQRefine(xn []float64, iy []int, iy0 []int, k, up, margin int, same bool) bool {
	n := len(xn)
	if n == 0 {
		return true
	}
	rounding := make([]float64, n)
	iysum := 0
	for i := 0; i < n; i++ {
		tmp := float64(k) * xn[i]
		iy[i] = int(math.Floor(0.5 + tmp))
		rounding[i] = tmp - float64(iy[i])
	}
	if !same {
		for i := 0; i < n; i++ {
			lo := up*iy0[i] - up + 1
			hi := up*iy0[i] + up - 1
			if iy[i] < lo {
				iy[i] = lo
			} else if iy[i] > hi {
				iy[i] = hi
			}
		}
	}
	for i := 0; i < n; i++ {
		iysum += iy[i]
	}
	if absInt(iysum-k) > 32 {
		return true
	}
	dir := -1
	if iysum < k {
		dir = 1
	}
	for iysum != k {
		roundVal := -1000000.0 * float64(dir)
		roundPos := 0
		for i := 0; i < n; i++ {
			if (rounding[i]-roundVal)*float64(dir) > 0 &&
				absInt(iy[i]-up*iy0[i]) < (margin-1) &&
				!(dir == -1 && iy[i] == 0) {
				roundVal = rounding[i]
				roundPos = i
			}
		}
		iy[roundPos] += dir
		rounding[roundPos] -= float64(dir)
		iysum += dir
	}
	return false
}

func opPVQSearchExtra(x []float64, k, up int) (iy []int, upIy []int, refine []int) {
	n := len(x)
	iy = make([]int, n)
	upIy = make([]int, n)
	refine = make([]int, n)

	sum := 0.0
	for i := 0; i < n; i++ {
		sum += math.Abs(x[i])
	}
	if sum < 1e-15 {
		iy[0] = k
		upIy[0] = up * k
		return iy, upIy, refine
	}

	xn := make([]float64, n)
	rcp := 1.0 / sum
	for i := 0; i < n; i++ {
		xn[i] = math.Abs(x[i]) * rcp
	}

	failed := opPVQRefine(xn, iy, iy, k, 1, k+1, true)
	failed = failed || opPVQRefine(xn, upIy, iy, up*k, up, up, false)
	if failed {
		iy[0] = k
		for i := 1; i < n; i++ {
			iy[i] = 0
		}
		upIy[0] = up * k
		for i := 1; i < n; i++ {
			upIy[i] = 0
		}
	}

	for i := 0; i < n; i++ {
		if x[i] < 0 {
			iy[i] = -iy[i]
			upIy[i] = -upIy[i]
		}
		refine[i] = upIy[i] - up*iy[i]
	}

	return iy, upIy, refine
}

func ecEncRefine(enc *rangecoding.Encoder, refine int, up int, extraBits int, useEntropy bool) {
	if enc == nil || extraBits <= 0 {
		return
	}
	large := absInt(refine) > up/2
	logp := uint(1)
	if useEntropy {
		logp = 3
	}
	if large {
		enc.EncodeBit(1, logp)
		sign := 0
		if refine < 0 {
			sign = 1
		}
		enc.EncodeRawBits(uint32(sign), 1)
		enc.EncodeRawBits(uint32(absInt(refine)-up/2-1), uint(extraBits-1))
	} else {
		enc.EncodeBit(0, logp)
		enc.EncodeRawBits(uint32(refine+up/2), uint(extraBits))
	}
}
