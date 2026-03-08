//go:build amd64

package celt

import (
	"math"

	"golang.org/x/sys/cpu"
)

var (
	amd64HasAVX     = cpu.X86.HasAVX
	amd64HasAVXFMA  = cpu.X86.HasAVX && cpu.X86.HasFMA
	amd64HasAVX2FMA = cpu.X86.HasAVX2 && cpu.X86.HasFMA
)

//go:noescape
func absSumAVX(x []float64) float64

func absSum(x []float64) float64 {
	if amd64HasAVX {
		return absSumAVX(x)
	}
	return absSumGeneric(x)
}

func absSumGeneric(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
}

//go:noescape
func roundFloat64ToFloat32AVX(x []float64)

func roundFloat64ToFloat32(x []float64) {
	if amd64HasAVX {
		roundFloat64ToFloat32AVX(x)
		return
	}
	roundFloat64ToFloat32Generic(x)
}

func roundFloat64ToFloat32Generic(x []float64) {
	for i, v := range x {
		x[i] = float64(float32(v))
	}
}

//go:noescape
func celtPitchXcorrAVX2FMA(x []float64, y []float64, xcorr []float64, length, maxPitch int)

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if amd64HasAVX2FMA {
		celtPitchXcorrAVX2FMA(x, y, xcorr, length, maxPitch)
		return
	}
	celtPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}

func celtPitchXcorrGeneric(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	x = x[:length:length]
	xcorr = xcorr[:maxPitch:maxPitch]
	yLen := maxPitch + length - 1
	if yLen > len(y) {
		return
	}
	y = y[:yLen:yLen]
	i := 0
	for ; i+3 < maxPitch; i += 4 {
		yy0 := y[i : i+length : i+length]
		yy1 := y[i+1 : i+1+length : i+1+length]
		yy2 := y[i+2 : i+2+length : i+2+length]
		yy3 := y[i+3 : i+3+length : i+3+length]
		var s0, s1, s2, s3 float64
		for j := 0; j < len(x); j++ {
			xj := x[j]
			s0 += xj * yy0[j]
			s1 += xj * yy1[j]
			s2 += xj * yy2[j]
			s3 += xj * yy3[j]
		}
		xcorr[i] = s0
		xcorr[i+1] = s1
		xcorr[i+2] = s2
		xcorr[i+3] = s3
	}
	for ; i < maxPitch; i++ {
		yy := y[i : i+length : i+length]
		sum := 0.0
		for j := 0; j < len(x); j++ {
			sum += x[j] * yy[j]
		}
		xcorr[i] = sum
	}
}

//go:noescape
func prefilterInnerProdAVXFMA(x, y []float64, length int) float64

//go:noescape
func prefilterDualInnerProdAVXFMA(x, y1, y2 []float64, length int) (float64, float64)

func prefilterInnerProd(x, y []float64, length int) float64 {
	if amd64HasAVXFMA {
		return prefilterInnerProdAVXFMA(x, y, length)
	}
	return prefilterInnerProdGeneric(x, y, length)
}

func prefilterInnerProdGeneric(x, y []float64, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = x[length-1]
	_ = y[length-1]
	sum := float32(0)
	for i := 0; i < length; i++ {
		sum += float32(x[i]) * float32(y[i])
	}
	return float64(sum)
}

func prefilterDualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if amd64HasAVXFMA {
		return prefilterDualInnerProdAVXFMA(x, y1, y2, length)
	}
	return prefilterDualInnerProdGeneric(x, y1, y2, length)
}

func prefilterDualInnerProdGeneric(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]
	_ = y1[length-1]
	_ = y2[length-1]
	sum1 := float32(0)
	sum2 := float32(0)
	for i := 0; i < length; i++ {
		xi := float32(x[i])
		sum1 += xi * float32(y1[i])
		sum2 += xi * float32(y2[i])
	}
	return float64(sum1), float64(sum2)
}

//go:noescape
func pvqSearchBestPosAVX(absX, y []float32, xy, yy float64, n int) int

//go:noescape
func pvqSearchPulseLoopAVX(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64)

//go:noescape
func pvqExtractAbsSignAVX(x []float64, absX []float32, y []float32, signx []int, iy []int, n int)

func pvqSearchBestPos(absX, y []float32, xy, yy float64, n int) int {
	if amd64HasAVX {
		return pvqSearchBestPosAVX(absX, y, xy, yy, n)
	}
	return pvqSearchBestPosGeneric(absX, y, xy, yy, n)
}

func pvqSearchBestPosGeneric(absX, y []float32, xy, yy float64, n int) int {
	if n <= 0 {
		return 0
	}
	xyf := float32(xy)
	yyf := float32(yy)
	bestID := 0
	rxy := xyf + absX[0]
	ryy := yyf + y[0]
	bestNum := rxy * rxy
	bestDen := ryy
	for j := 1; j < n; j++ {
		rxy = xyf + absX[j]
		ryy = yyf + y[j]
		num := rxy * rxy
		if bestDen*num > ryy*bestNum {
			bestDen = ryy
			bestNum = num
			bestID = j
		}
	}
	return bestID
}

func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	if amd64HasAVX {
		return pvqSearchPulseLoopAVX(absX, y, iy, xy, yy, n, pulsesLeft)
	}
	return pvqSearchPulseLoopGeneric(absX, y, iy, xy, yy, n, pulsesLeft)
}

func pvqSearchPulseLoopGeneric(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	xyf := float32(xy)
	yyf := float32(yy)
	for i := 0; i < pulsesLeft; i++ {
		yyf += 1

		bestID := 0
		rxy := xyf + absX[0]
		ryy := yyf + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		for j := 1; j < n; j++ {
			rxy = xyf + absX[j]
			ryy = yyf + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xyf += absX[bestID]
		yyf += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return float64(xyf), float64(yyf)
}

func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []int, iy []int, n int) {
	if amd64HasAVX {
		pvqExtractAbsSignAVX(x, absX, y, signx, iy, n)
		return
	}
	pvqExtractAbsSignGeneric(x, absX, y, signx, iy, n)
}

func pvqExtractAbsSignGeneric(x []float64, absX []float32, y []float32, signx []int, iy []int, n int) {
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = float32(-xj)
		} else {
			absX[j] = float32(xj)
		}
	}
}

//go:noescape
func expRotation1Stride2AVXFMA(x []float64, length int, c, s float64)

func expRotation1Stride2(x []float64, length int, c, s float64) {
	if amd64HasAVXFMA {
		expRotation1Stride2AVXFMA(x, length, c, s)
		return
	}
	expRotation1Stride2Generic(x, length, c, s)
}

func expRotation1Stride2Generic(x []float64, length int, c, s float64) {
	ms := -s
	end := length - 2
	i := 0
	for ; i+1 < end; i += 2 {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i+1]
		x4 := x[i+3]
		x[i+3] = c*x4 + s*x3
		x[i+1] = c*x3 + ms*x4
	}
	for ; i < end; i++ {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
	i = length - 5
	for ; i-1 >= 0; i -= 2 {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2

		x3 := x[i-1]
		x4 := x[i+1]
		x[i+1] = c*x4 + s*x3
		x[i-1] = c*x3 + ms*x4
	}
	for ; i >= 0; i-- {
		x1 := x[i]
		x2 := x[i+2]
		x[i+2] = c*x2 + s*x1
		x[i] = c*x1 + ms*x2
	}
}

//go:noescape
func transientEnergyPairsAVX(tmp []float64, x2out []float32, len2 int) float64

func transientEnergyPairs(tmp []float64, x2out []float32, len2 int) float64 {
	if amd64HasAVX {
		return transientEnergyPairsAVX(tmp, x2out, len2)
	}
	return transientEnergyPairsGeneric(tmp, x2out, len2)
}

func transientEnergyPairsGeneric(tmp []float64, x2out []float32, len2 int) float64 {
	var mean float32
	for i := 0; i < len2; i++ {
		t0 := float32(tmp[2*i])
		t1 := float32(tmp[2*i+1])
		x2 := t0*t0 + t1*t1
		x2out[i] = x2
		mean += x2
	}
	return float64(mean)
}

//go:noescape
func pitchAutocorr5AVXFMA(lp []float64, length int, ac *[5]float64)

func pitchAutocorr5(lp []float64, length int, ac *[5]float64) {
	if amd64HasAVXFMA {
		pitchAutocorr5AVXFMA(lp, length, ac)
		return
	}
	pitchAutocorr5Generic(lp, length, ac)
}

func pitchAutocorr5Generic(lp []float64, length int, ac *[5]float64) {
	fastN := length - 4
	if fastN < 0 {
		fastN = 0
	}
	for lag := 0; lag <= 4; lag++ {
		sum := float32(0)
		for i := 0; i < fastN; i++ {
			sum += float32(lp[i]) * float32(lp[i+lag])
		}
		for i := lag + fastN; i < length; i++ {
			sum += float32(lp[i]) * float32(lp[i-lag])
		}
		ac[lag] = float64(sum)
	}
}

//go:noescape
func toneLPCCorrAVXFMA(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32)

func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	if amd64HasAVXFMA {
		return toneLPCCorrAVXFMA(x, cnt, delay, delay2)
	}
	return toneLPCCorrGeneric(x, cnt, delay, delay2)
}

func toneLPCCorrGeneric(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	for i := 0; i < cnt; i++ {
		xi := x[i]
		r00 += xi * xi
		r01 += xi * x[i+delay]
		r02 += xi * x[i+delay2]
	}
	return
}

//go:noescape
func prefilterPitchXcorrAVX2FMA(x, y, xcorr []float64, length, maxPitch int)

func prefilterPitchXcorr(x, y, xcorr []float64, length, maxPitch int) {
	if amd64HasAVX2FMA {
		prefilterPitchXcorrAVX2FMA(x, y, xcorr, length, maxPitch)
		return
	}
	prefilterPitchXcorrGeneric(x, y, xcorr, length, maxPitch)
}

func prefilterPitchXcorrGeneric(x, y, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]
	_ = xcorr[maxPitch-1]
	_ = y[maxPitch+length-2]
	for i := 0; i < maxPitch; i++ {
		sum := float32(0)
		for j := 0; j < length; j++ {
			sum += float32(x[j]) * float32(y[i+j])
		}
		xcorr[i] = float64(sum)
	}
}
