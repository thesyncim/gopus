//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math"
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var silkUsePitchXcorrAVX2FMA = archsimd.X86.AVX2() && archsimd.X86.FMA()

func xcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !silkUsePitchXcorrAVX2FMA {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	if length >= 120 && length <= 240 {
		xcorrKernelAVX8OnePass(x, y, sum, length)
		return
	}

	// Run four correlations at a time. Keeping eight vector accumulators live
	// alongside the x/y vectors spills them in the sample loop on amd64.
	xcorrKernelAVX4(x, y, (*[4]float32)(sum[:4]), length)
	xcorrKernelAVX4(x, y[4:], (*[4]float32)(sum[4:]), length)
}

// xcorrKernelAVX8OnePass keeps all eight correlation accumulators live in one
// loop. Its FMA and lane-reduction order matches xcorrKernelAVX8.
func xcorrKernelAVX8OnePass(x, y []float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !silkUsePitchXcorrAVX2FMA {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	xcorrKernelAVX8OnePassVector(x, y, sum, length)
}

//go:noinline
func xcorrKernelAVX8OnePassVector(x, y []float32, sum *[8]float32, length int) {
	var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 archsimd.Float32x8
	x = x[:length]
	y = y[:length+7]
	i := 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yb := y[i : i+15]
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), acc3)
		// The loop bound implies i < length; this guard keeps the second four
		// y vectors out of the first group's register live range.
		if i < length {
			acc4 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[4:])), acc4)
			acc5 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[5:])), acc5)
			acc6 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[6:])), acc6)
			acc7 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[7:])), acc7)
		}
	}
	if i < length {
		remaining := length - i
		xTail := loadXcorrTail8(x[i:], remaining)
		yb := y[i:]
		acc0 = xTail.MulAdd(loadXcorrTail8(yb[0:], remaining), acc0)
		acc1 = xTail.MulAdd(loadXcorrTail8(yb[1:], remaining), acc1)
		acc2 = xTail.MulAdd(loadXcorrTail8(yb[2:], remaining), acc2)
		acc3 = xTail.MulAdd(loadXcorrTail8(yb[3:], remaining), acc3)
		acc4 = xTail.MulAdd(loadXcorrTail8(yb[4:], remaining), acc4)
		acc5 = xTail.MulAdd(loadXcorrTail8(yb[5:], remaining), acc5)
		acc6 = xTail.MulAdd(loadXcorrTail8(yb[6:], remaining), acc6)
		acc7 = xTail.MulAdd(loadXcorrTail8(yb[7:], remaining), acc7)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray((*[4]float32)(sum[:4]))
	reduceXcorrAVX8Four(acc4, acc5, acc6, acc7).StoreArray((*[4]float32)(sum[4:]))
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(x, y[corr:], length)
		}
	}
}

//go:noinline
func xcorrKernelAVX4(x, y []float32, sum *[4]float32, length int) {
	x = x[:length]
	y = y[:length+3]
	var acc0, acc1, acc2, acc3 archsimd.Float32x8
	i := 0
	for ; i+16 <= length; i += 16 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yb := y[i : i+19]
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), acc3)
		xv = archsimd.LoadFloat32x8Array((*[8]float32)(x[i+8:]))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[8:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[9:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[10:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[11:])), acc3)
	}
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yb := y[i : i+11]
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), acc3)
	}
	if i < length {
		remaining := length - i
		xTail := loadXcorrTail8(x[i:], remaining)
		yb := y[i:]
		acc0 = xTail.MulAdd(loadXcorrTail8(yb[0:], remaining), acc0)
		acc1 = xTail.MulAdd(loadXcorrTail8(yb[1:], remaining), acc1)
		acc2 = xTail.MulAdd(loadXcorrTail8(yb[2:], remaining), acc2)
		acc3 = xTail.MulAdd(loadXcorrTail8(yb[3:], remaining), acc3)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray(sum)
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(x, y[corr:], length)
		}
	}
}

func loadXcorrTail8(s []float32, remaining int) archsimd.Float32x8 {
	// Read only valid tail lanes before loading the stack vector, so a tail at a
	// page boundary never turns into a full-width memory read. Integer bits keep
	// NaN payloads exact; the AVX zero store also avoids legacy SSE while
	// correlation accumulators are live.
	var lanes [8]uint32
	var zero archsimd.Uint32x8
	zero.StoreArray(&lanes)
	s = s[:remaining]
	for lane := range remaining {
		lanes[lane] = math.Float32bits(s[lane])
	}
	return archsimd.LoadUint32x8Array(&lanes).BitsToFloat32()
}

// reduceXcorrAVX8Lanes leaves the exact horizontal sum broadcast in all lanes.
func reduceXcorrAVX8Lanes(v archsimd.Float32x8) archsimd.Float32x8 {
	v = v.Add(v.ConcatPermute128Scalars(1, 0, v))
	v = v.ConcatAddPairsGrouped(v)
	v = v.ConcatAddPairsGrouped(v)
	return v
}

// reduceXcorrAVX8Four packs four lane-zero sums with three vector shuffles.
func reduceXcorrAVX8Four(v0, v1, v2, v3 archsimd.Float32x8) archsimd.Float32x4 {
	r0 := reduceXcorrAVX8Lanes(v0)
	r1 := reduceXcorrAVX8Lanes(v1)
	r2 := reduceXcorrAVX8Lanes(v2)
	r3 := reduceXcorrAVX8Lanes(v3)
	p01 := r0.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r1)
	p23 := r2.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r3)
	return p01.ConcatPermuteScalarsGrouped(0, 2, 4, 6, p23).GetLo()
}

func xcorrKernelAVX8ScalarGo(x, y []float32, sum *[8]float32, length int) {
	xs := x[:length]
	ys := y[:length+7]
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			yv := ys[i+corr]
			if i < 8 {
				// The AVX2 lane starts at +0. Its first fused multiply-add is
				// the product rounded to float32. Keep the FMA for zero or
				// non-finite inputs to preserve its signed-zero and NaN rules.
				product := xv * yv
				if xv != 0 && yv != 0 && product == product {
					lanes[corr][lane] = product
				} else {
					lanes[corr][lane] = opusmath.FMA32(xv, yv, 0)
				}
			} else {
				lanes[corr][lane] = opusmath.FMA32(xv, yv, lanes[corr][lane])
			}
		}
	}
	for corr := range 8 {
		v := lanes[corr]
		s04 := v[0] + v[4]
		s15 := v[1] + v[5]
		s26 := v[2] + v[6]
		s37 := v[3] + v[7]
		sum[corr] = (s04 + s15) + (s26 + s37)
	}
}
