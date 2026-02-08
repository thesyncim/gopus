package celt

import (
	"math"

	"github.com/thesyncim/gopus/util"
	"github.com/thesyncim/gopus/rangecoding"
)

// EPSILON is the minimum value used to prevent division by zero and similar issues.
// This matches libopus celt/mathops.h EPSILON definition.
const pvqEPSILON = 1e-15

// opPVQSearch implements libopus op_pvq_search_c() (float path) with high precision.
// It finds the signed pulse vector iy (sum abs = K) that best matches X.
// Returns the pulse vector and the computed energy yy (sum of squares of pulses).
//
// This implementation closely follows libopus celt/vq.c op_pvq_search_c() lines 205-374:
// 1. Pre-search phase: Projects X onto the pyramid for large K
// 2. Greedy phase: Places remaining pulses one at a time using rate-distortion criterion
// 3. Sign restoration: Applies original signs to the pulse vector
//
// The rate-distortion criterion maximizes Rxy/sqrt(Ryy), which is equivalent to
// maximizing Rxy^2/Ryy (avoiding the sqrt). We compare (Rxy_new)^2 * Ryy_old > (Rxy_old)^2 * Ryy_new.
//
// NOTE: This function does NOT modify the input x slice.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/vq.c op_pvq_search_c()
func opPVQSearch(x []float64, k int) ([]int, float64) {
	return opPVQSearchScratch(x, k, nil, nil, nil, nil)
}

// opPVQSearchScratch is the scratch-aware version of opPVQSearch.
// It uses pre-allocated buffers to avoid allocations in the hot path.
func opPVQSearchScratch(x []float64, k int, iyBuf *[]int, signxBuf *[]int, yBuf *[]float32, absXBuf *[]float32) ([]int, float64) {
	n := len(x)

	// Ensure output buffer
	var iy []int
	if iyBuf != nil {
		iy = ensureIntSlice(iyBuf, n)
	} else {
		iy = make([]int, n)
	}

	if n == 0 || k <= 0 {
		for i := range iy {
			iy[i] = 0
		}
		return iy, 0
	}

	// Ensure scratch buffers
	var signx []int
	var y []float32
	var absX []float32

	if signxBuf != nil {
		signx = ensureIntSlice(signxBuf, n)
	} else {
		signx = make([]int, n)
	}

	if yBuf != nil {
		y = ensureFloat32Slice(yBuf, n)
	} else {
		y = make([]float32, n)
	}

	if absXBuf != nil {
		absX = ensureFloat32Slice(absXBuf, n)
	} else {
		absX = make([]float32, n)
	}

	// Initialize buffers
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		if x[j] < 0 {
			signx[j] = 1
			absX[j] = float32(-x[j])
		} else {
			absX[j] = float32(x[j])
		}
	}

	var xy float32
	var yy float32
	pulsesLeft := k

	// Pre-search by projecting on the pyramid for large K.
	// Reference: libopus vq.c lines 241-282
	if k > (n >> 1) {
		var sum float32
		for j := 0; j < n; j++ {
			sum += absX[j]
		}

		// If X is too small or invalid, replace with a pulse at position 0.
		// Reference: libopus vq.c lines 252-262
		// Prevents infinities and NaNs from causing too many pulses to be allocated.
		// The check "sum < 64" is an approximation of infinity.
		if !(sum > pvqEPSILON && sum < 64.0) {
			absX[0] = 1.0
			for j := 1; j < n; j++ {
				absX[j] = 0.0
			}
			sum = 1.0
		}

		// Using K+0.8 guarantees we cannot get more than K pulses.
		// Reference: libopus vq.c lines 266-267
		rcp := (float32(k) + 0.8) / sum
		for j := 0; j < n; j++ {
			// It's important to round towards zero here (floor for positive values)
			// Reference: libopus vq.c line 274
			iy[j] = int(rcp * absX[j]) // rcp >= 0, absX >= 0: truncation == floor
			y[j] = float32(iy[j])
			yy += y[j] * y[j]
			xy += absX[j] * y[j]
			// We multiply y[j] by 2 so we don't have to do it in the main loop
			// Reference: libopus vq.c line 279
			y[j] *= 2
			pulsesLeft -= iy[j]
		}
	}

	// Safety check: if pulsesLeft is way too large, dump them in first bin.
	// This should never happen except on silence or corrupt data.
	// Reference: libopus vq.c lines 290-297
	if pulsesLeft > n+3 {
		tmp := float32(pulsesLeft)
		yy += tmp * tmp
		yy += tmp * y[0]
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	// Main greedy search loop: place remaining pulses one at a time.
	// For each pulse, find the position that maximizes Rxy/sqrt(Ryy).
	// Reference: libopus vq.c lines 299-362
	for i := 0; i < pulsesLeft; i++ {
		bestID := 0
		// The squared magnitude term gets added anyway, so we add it outside the loop
		// Reference: libopus vq.c line 314
		yy += 1

		// Calculations for position 0 are out of the loop to reduce branch mispredictions
		// Reference: libopus vq.c lines 318-328
		rxy := xy + absX[0]
		ryy := yy + y[0] // y[j] is pre-multiplied by 2
		// Approximate score: we maximise Rxy/sqrt(Ryy)
		// Rxy is guaranteed positive because signs are pre-computed
		bestNum := rxy * rxy
		bestDen := ryy

		// Search remaining positions
		// Reference: libopus vq.c lines 329-351
		for j := 1; j < n; j++ {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			// Compare num/den vs bestNum/bestDen without division:
			// num/den > bestNum/bestDen  <=>  den*num > bestDen*bestNum (for positive den, bestDen)
			// Reference: libopus vq.c line 345
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		// Update running sums for the chosen position
		// Reference: libopus vq.c lines 353-361
		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2 // Keep y[j] = 2*iy[j] invariant
		iy[bestID]++
	}

	// Put the original signs back
	// Reference: libopus vq.c lines 364-371
	// The XOR trick: (iy[j]^-signx[j]) + signx[j] negates iy[j] if signx[j]=1
	for j := 0; j < n; j++ {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
	}

	return iy, float64(yy)
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
	iy[1] = k - util.Abs(iy[0])
	upIy[1] = up*k - util.Abs(upIy[0])
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
	if util.Abs(iysum-k) > 32 {
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
				util.Abs(iy[i]-up*iy0[i]) < (margin-1) &&
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
	large := util.Abs(refine) > up/2
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
		enc.EncodeRawBits(uint32(util.Abs(refine)-up/2-1), uint(extraBits-1))
	} else {
		enc.EncodeBit(0, logp)
		enc.EncodeRawBits(uint32(refine+up/2), uint(extraBits))
	}
}
