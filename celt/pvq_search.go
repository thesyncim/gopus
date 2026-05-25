package celt

import (
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/util"
)

// EPSILON is the minimum value used to prevent division by zero and similar issues.
// This matches libopus celt/mathops.h EPSILON definition.
const pvqEPSILON = 1e-15

// opPVQSearch implements libopus op_pvq_search_c() (float path).
// It finds the signed pulse vector iy (sum abs = K) that best matches X.
// Runtime CELT normalized vectors use celt_norm width, matching libopus float builds.
func opPVQSearch(x []celtNorm, k int) ([]int, opusVal16) {
	iy, yy := opPVQSearchNorm(x, k)
	return iy, yy
}

func opPVQSearchScratch(x []celtNorm, k int, iyBuf *[]int, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32) ([]int, opusVal16) {
	iy, yy := opPVQSearchScratchNorm(x, k, iyBuf, signxBuf, yBuf, absXBuf)
	return iy, yy
}

func opPVQSearchScratchWithInputMutation(x []celtNorm, k int, iyBuf *[]int, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32, absInput bool) ([]int, opusVal16) {
	iy, yy := opPVQSearchScratchNormWithInputMutation(x, k, iyBuf, signxBuf, yBuf, absXBuf, absInput)
	return iy, yy
}

func opPVQSearchNorm(x []celtNorm, k int) ([]int, opusVal16) {
	return opPVQSearchScratchNorm(x, k, nil, nil, nil, nil)
}

func opPVQSearchScratchNorm(x []celtNorm, k int, iyBuf *[]int, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32) ([]int, opusVal16) {
	return opPVQSearchScratchNormWithInputMutation(x, k, iyBuf, signxBuf, yBuf, absXBuf, false)
}

func opPVQSearchScratchNormWithInputMutation(x []celtNorm, k int, iyBuf *[]int, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32, absInput bool) ([]int, opusVal16) {
	n := len(x)
	const idxBias = float32(0)

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
	var signx []byte
	var y []float32
	var absX []float32

	if signxBuf != nil {
		signx = ensureByteSlice(signxBuf, n)
	} else {
		signx = make([]byte, n)
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

	highPulseSearch := k > (n >> 1)
	var highPulseSum float32

	// Initialize buffers: extract abs values and signs from celt_norm input.
	if idxBias == 0 {
		if highPulseSearch {
			highPulseSum = pvqExtractAbsSignOnlySum(x, absX, signx, n)
		} else {
			pvqExtractAbsSignNorm(x, absX, y, signx, iy, n)
		}
	} else {
		// Slow path with optional idx bias.
		_ = iy[n-1]
		_ = signx[n-1]
		_ = y[n-1]
		_ = absX[n-1]
		_ = x[n-1]
		for j := 0; j < n; j++ {
			iy[j] = 0
			signx[j] = 0
			y[j] = 0
			xj := x[j]
			if xj < 0 {
				signx[j] = 1
				absX[j] = -xj
			} else {
				absX[j] = xj
			}
			absX[j] -= float32(j) * idxBias
			if absX[j] < 0 {
				absX[j] = 0
			}
		}
	}

	var xy float32
	var yy float32
	pulsesLeft := k

	// Pre-search by projecting on the pyramid for large K.
	// Reference: libopus vq.c lines 241-282
	if highPulseSearch {
		sum := highPulseSum

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

	if absInput {
		for j := 0; j < n; j++ {
			x[j] = celtNorm(absX[j])
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
	//
	// The entire outer pulse loop + inner position search is merged into
	// pvqSearchPulseLoop (assembly on arm64/amd64) to eliminate per-pulse
	// Go→asm transition overhead.
	if pulsesLeft > 0 && n > 0 {
		xy, yy = pvqSearchPulseLoop(absX[:n], y[:n], iy[:n], xy, yy, n, pulsesLeft)
	}

	// Put the original signs back
	// Reference: libopus vq.c lines 364-371
	// The XOR trick: (iy[j]^-signx[j]) + signx[j] negates iy[j] if signx[j]=1
	for j := 0; j < n; j++ {
		mask := -int(signx[j])
		iy[j] = (iy[j] ^ mask) - mask
	}

	return iy, opusVal16(yy)
}

func pvqExtractAbsSignOnly(x []celtNorm, absX []float32, signx []byte, n int) {
	_ = x[n-1]
	_ = absX[n-1]
	_ = signx[n-1]
	for j := 0; j < n; j++ {
		signx[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = -xj
		} else {
			absX[j] = xj
		}
	}
}

func pvqExtractAbsSignOnlySum(x []celtNorm, absX []float32, signx []byte, n int) float32 {
	_ = x[n-1]
	_ = absX[n-1]
	_ = signx[n-1]
	var sum float32
	for j := 0; j < n; j++ {
		signx[j] = 0
		xj := x[j]
		var ax float32
		if xj < 0 {
			signx[j] = 1
			ax = -xj
		} else {
			ax = xj
		}
		absX[j] = ax
		sum += ax
	}
	return sum
}

func pvqExtractAbsSignNorm(x []celtNorm, absX []float32, y []float32, signx []byte, iy []int, n int) {
	_ = x[n-1]
	_ = absX[n-1]
	_ = y[n-1]
	_ = signx[n-1]
	_ = iy[n-1]
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = -xj
		} else {
			absX[j] = xj
		}
	}
}

func opPVQSearchN2(x []celtNorm, k, up int) (iy []int, upIy []int, refine int) {
	iy, upIy, refine, _ = opPVQSearchN2Norm(x, k, up)
	return iy, upIy, refine
}

func opPVQSearchN2Norm(x []celtNorm, k, up int) (iy []int, upIy []int, refine int, yy opusVal32) {
	iy = make([]int, 2)
	upIy = make([]int, 2)
	if len(x) < 2 || k <= 0 || up <= 0 {
		if k > 0 {
			iy[0] = k
			upIy[0] = up * k
			yy = opusVal32(float32(k) * float32(k) * float32(up) * float32(up))
		}
		return iy, upIy, 0, yy
	}

	sum := absCeltNorm(x[0]) + absCeltNorm(x[1])
	if sum < pvqEPSILON {
		iy[0] = k
		upIy[0] = up * k
		yy = opusVal32(float32(k) * float32(k) * float32(up) * float32(up))
		return iy, upIy, 0, yy
	}

	rcp := float32(1) / sum
	iy[0] = floor32ToInt(float32(0.5) + float32(k)*float32(x[0])*rcp)
	upIy[0] = floor32ToInt(float32(0.5) + float32(up*k)*float32(x[0])*rcp)

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

	yy0 := float32(upIy[0]) * float32(upIy[0])
	yy1 := float32(upIy[1]) * float32(upIy[1])
	yy = opusVal32(yy0 + yy1)

	return iy, upIy, refine, yy
}

func opPVQRefineNorm(xn []opusVal32, iy []int, iy0 []int, k, up, margin int, same bool) bool {
	n := len(xn)
	if n == 0 {
		return true
	}
	rounding := make([]opusVal32, n)
	iysum := 0
	for i := 0; i < n; i++ {
		tmp := float32(k) * float32(xn[i])
		iy[i] = floor32ToInt(float32(0.5) + tmp)
		rounding[i] = opusVal32(tmp - float32(iy[i]))
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
		roundVal := opusVal32(float32(-1000000 * dir))
		roundPos := 0
		for i := 0; i < n; i++ {
			if float32(rounding[i]-roundVal)*float32(dir) > 0 &&
				util.Abs(iy[i]-up*iy0[i]) < (margin-1) &&
				!(dir == -1 && iy[i] == 0) {
				roundVal = rounding[i]
				roundPos = i
			}
		}
		iy[roundPos] += dir
		rounding[roundPos] = opusVal32(float32(rounding[roundPos]) - float32(dir))
		iysum += dir
	}
	return false
}

func opPVQSearchExtra(x []celtNorm, k, up int) (iy []int, upIy []int, refine []int) {
	iy, upIy, refine, _ = opPVQSearchExtraNorm(x, k, up)
	return iy, upIy, refine
}

func opPVQSearchExtraNorm(x []celtNorm, k, up int) (iy []int, upIy []int, refine []int, yy opusVal32) {
	n := len(x)
	iy = make([]int, n)
	upIy = make([]int, n)
	refine = make([]int, n)
	if n == 0 || k <= 0 || up <= 0 {
		return iy, upIy, refine, 0
	}

	sum := opusVal32(0)
	for i := 0; i < n; i++ {
		sum = opusVal32(float32(sum) + absCeltNorm(x[i]))
	}
	failed := sum < pvqEPSILON
	if failed {
		iy[0] = k
		upIy[0] = up * k
	} else {
		xn := make([]opusVal32, n)
		rcp := opusVal32(float32(1) / float32(sum))
		for i := 0; i < n; i++ {
			xn[i] = opusVal32(absCeltNorm(x[i]) * float32(rcp))
		}
		failed = opPVQRefineNorm(xn, iy, iy, k, 1, k+1, true)
		failed = failed || opPVQRefineNorm(xn, upIy, iy, up*k, up, up, false)
	}

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
		yy = opusVal32(float32(yy) + float32(upIy[i])*float32(upIy[i]))
		if x[i] < 0 {
			iy[i] = -iy[i]
			upIy[i] = -upIy[i]
		}
		refine[i] = upIy[i] - up*iy[i]
	}

	return iy, upIy, refine, yy
}

func absCeltNorm(x celtNorm) float32 {
	if x < 0 {
		return float32(-x)
	}
	return float32(x)
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

func ecDecRefine(dec *rangecoding.Decoder, up int, extraBits int, useEntropy bool) int {
	if dec == nil || extraBits <= 0 {
		return 0
	}
	logp := uint(1)
	if useEntropy {
		logp = 3
	}
	large := dec.DecodeBit(logp)
	if large != 0 {
		sign := int(dec.DecodeRawBit())
		refine := int(dec.DecodeRawBits(uint(extraBits-1))) + up/2 + 1
		if sign != 0 {
			refine = -refine
		}
		return refine
	}
	return int(dec.DecodeRawBits(uint(extraBits))) - up/2
}
