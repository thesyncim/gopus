//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"unsafe"

	"simd/archsimd"
)

var useX86PVQSearchSSE2 = archsimd.X86.AVX()

func x86RcpApprox4(dst, src *[4]float32) {
	archsimd.LoadFloat32x4Array(src).Reciprocal().StoreArray(dst)
}

func x86PVQSearchBestIDSSE2(absX, y []float32, xy, yy float32, n int) int {
	if n <= 0 {
		return 0
	}

	xy4 := archsimd.BroadcastFloat32x4(xy)
	yy4 := archsimd.BroadcastFloat32x4(yy)
	laneMax := archsimd.BroadcastFloat32x4(0)
	laneID := archsimd.BroadcastInt32x4(0)
	ids := archsimd.LoadInt32x4Array(&[4]int32{0, 1, 2, 3})
	four := archsimd.BroadcastInt32x4(4)
	for i := 0; i < n; i += 4 {
		x4 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&absX[i])))
		y4 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&y[i])))
		scores := x4.Add(xy4).Mul(y4.Add(yy4).ReciprocalSqrt())
		improved := scores.Greater(laneMax).ToInt32x4()
		laneID = ids.And(improved).Or(laneID.AndNot(improved))
		laneMax = laneMax.Max(scores)
		ids = ids.Add(four)
	}

	var maxValues [4]float32
	var idValues [4]int32
	laneMax.StoreArray(&maxValues)
	laneID.StoreArray(&idValues)
	max02 := x86MaxPS32(maxValues[0], maxValues[2])
	max13 := x86MaxPS32(maxValues[1], maxValues[3])
	bestScore := x86MaxPS32(max02, max13)
	bestID := 0
	for lane := range 4 {
		if maxValues[lane] == bestScore && int(idValues[lane]) > bestID {
			bestID = int(idValues[lane])
		}
	}
	return bestID
}

func x86MaxPS32(a, b float32) float32 {
	if a != a || b != b || a == b || b > a {
		return b
	}
	return a
}

// opPVQSearchScratchNormX86SSE2 mirrors libopus 1.6.1
// celt/x86/vq_sse2.c:op_pvq_search_sse2. This Go SIMD path uses AVX-enabled
// archsimd reciprocal and reciprocal-square-root operations while preserving
// libopus's SSE2 lane order and approximation points.
func opPVQSearchScratchNormX86SSE2(x []celtNorm, k int, iyBuf *[]int32, signxBuf *[]byte, yBuf *[]float32, absXBuf *[]float32, absInput bool) ([]int32, opusVal16) {
	n := len(x)
	var iy []int32
	if iyBuf != nil {
		iy = ensureInt32Slice(iyBuf, n)
	} else {
		iy = make([]int32, n)
	}
	if n == 0 || k <= 0 {
		for i := range iy {
			iy[i] = 0
		}
		return iy, 0
	}

	workN := n + 3
	var signx []byte
	var y []float32
	var absX []float32
	if signxBuf != nil {
		signx = ensureByteSlice(signxBuf, n)
	} else {
		signx = make([]byte, n)
	}
	if yBuf != nil {
		y = ensureFloat32Slice(yBuf, workN)
	} else {
		y = make([]float32, workN)
	}
	if absXBuf != nil {
		absX = ensureFloat32Slice(absXBuf, workN)
	} else {
		absX = make([]float32, workN)
	}

	var sums [4]float32
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := float32(x[j])
		if xj < 0 {
			signx[j] = 1
		}
		ax := x86PVQAbs32(xj)
		absX[j] = ax
		sums[j&3] = noFMA32Add(sums[j&3], ax)
	}

	xy := float32(0)
	yy := float32(0)
	pulsesLeft := k
	sum := x86LaneSum4(sums)
	sums = [4]float32{sum, sum, sum, sum}
	if k > (n >> 1) {
		if !(sum > pvqEPSILON && sum < 64) {
			absX[0] = 1
			for j := 1; j < n; j++ {
				absX[j] = 0
			}
			sums = [4]float32{1, 1, 1, 1}
		}

		var rcp4 [4]float32
		x86RcpApprox4(&rcp4, &sums)
		for lane := 0; lane < 4; lane++ {
			rcp4[lane] = noFMA32Mul(float32(k)+0.8, rcp4[lane])
		}
		var xy4, yy4 [4]float32
		var pulseSums [4]int32
		for j := 0; j < n; j++ {
			lane := j & 3
			iyj := int32(noFMA32Mul(absX[j], rcp4[lane]))
			iy[j] = iyj
			yj := float32(iyj)
			xy4[lane] = noFMA32Add(xy4[lane], noFMA32Mul(absX[j], yj))
			yy4[lane] = noFMA32Add(yy4[lane], noFMA32Mul(yj, yj))
			pulseSums[lane] += iyj
			y[j] = noFMA32Add(yj, yj)
		}
		pulsesLeft -= int(x86LaneSum4Int32(pulseSums))
		xy = x86LaneSum4(xy4)
		yy = x86LaneSum4(yy4)
	}

	for j := n; j < workN; j++ {
		absX[j] = -100
		y[j] = 100
	}

	// The generic C helper abs-mutates X, but the x86 SSE2 helper searches a
	// local copy and leaves the caller's vector untouched.
	_ = absInput

	if pulsesLeft > n+3 {
		tmp := float32(pulsesLeft)
		yy = noFMA32Add(yy, noFMA32Mul(tmp, tmp))
		yy = noFMA32Add(yy, noFMA32Mul(tmp, y[0]))
		iy[0] += int32(pulsesLeft)
		pulsesLeft = 0
	}

	for i := 0; i < pulsesLeft; i++ {
		yy = noFMA32Add(yy, 1)
		bestID := x86PVQSearchBestID(absX, y, xy, yy, n)
		xy = noFMA32Add(xy, absX[bestID])
		yy = noFMA32Add(yy, y[bestID])
		y[bestID] = noFMA32Add(y[bestID], 2)
		iy[bestID]++
	}

	for j := 0; j < n; j++ {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
	}
	return iy, opusVal16(yy)
}

func x86PVQSearchBestID(absX, y []float32, xy, yy float32, n int) int {
	return x86PVQSearchBestIDSSE2(absX, y, xy, yy, n)
}

func x86LaneSum4(v [4]float32) float32 {
	return noFMA32Add(noFMA32Add(v[0], v[2]), noFMA32Add(v[1], v[3]))
}

func x86LaneSum4Int32(v [4]int32) int32 {
	return (v[0] + v[2]) + (v[1] + v[3])
}

func x86PVQAbs32(x float32) float32 {
	return math.Float32frombits(math.Float32bits(x) &^ (uint32(1) << 31))
}
