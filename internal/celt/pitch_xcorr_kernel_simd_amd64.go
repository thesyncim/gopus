//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !archsimd.X86.FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX8Tail(x, y, sum, length, &tail)
}

// xcorrKernelAVX8Tail is celt_pitch_xcorr_avx2's xcorr_kernel_avx for eight
// lags starting at y, with the masked tail block taken from tail.
func xcorrKernelAVX8Tail(x, y *float32, sum *[8]float32, length int, tail *xcorrTail8) {
	if length >= 120 && length <= 240 {
		xcorrKernelAVX8OnePassTail(x, y, sum, length, tail)
		return
	}

	// Run four correlations at a time. Keeping eight vector accumulators live
	// alongside the x/y vectors spills them in the sample loop on amd64.
	xcorrKernelAVX4Tail(x, y, (*[4]float32)(unsafe.Pointer(&sum[0])), length, tail)
	xcorrKernelAVX4Tail(x, (*float32)(unsafe.Add(unsafe.Pointer(y), 16)), (*[4]float32)(unsafe.Pointer(&sum[4])), length, tail)
}

// xcorrTail8 is the masked final block of xcorr_kernel_avx: the rem = length%8
// valid lanes of x, zero elsewhere, and the lane mask _mm256_maskload_ps
// applies to y. One pitch cross-correlation shares it across all lags. When
// yFull is set the caller guarantees eight readable floats at every masked y
// load, so y loads full width and masks with an AND; otherwise it gathers only
// the valid lanes.
type xcorrTail8 struct {
	x     archsimd.Float32x8
	mask  archsimd.Uint32x8
	rem   int
	yFull bool
}

// xcorrTailMaskTable holds eight set lanes followed by eight clear ones;
// xcorrTailMaskTable[8-rem:] starts the mask of the first rem lanes.
var xcorrTailMaskTable = [16]uint32{
	^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0),
}

func newXcorrTail8(x unsafe.Pointer, length int) xcorrTail8 {
	rem := length & 7
	if rem == 0 {
		return xcorrTail8{}
	}
	return xcorrTail8{
		x:    loadXcorrTail8(unsafe.Add(x, 4*(length-rem)), rem),
		mask: archsimd.LoadUint32x8Array((*[8]uint32)(xcorrTailMaskTable[8-rem:])),
		rem:  rem,
	}
}

func (t *xcorrTail8) loadY(p unsafe.Pointer) archsimd.Float32x8 {
	if t.yFull {
		return archsimd.LoadFloat32x8Array((*[8]float32)(p)).ToBits().And(t.mask).BitsToFloat32()
	}
	return loadXcorrTail8(p, t.rem)
}

// xcorrKernelAVX8OnePass keeps all eight correlation accumulators live in one
// loop. Its FMA and lane-reduction order matches xcorrKernelAVX8.
func xcorrKernelAVX8OnePass(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !archsimd.X86.FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX8OnePassTail(x, y, sum, length, &tail)
}

func xcorrKernelAVX8OnePassTail(x, y *float32, sum *[8]float32, length int, tail *xcorrTail8) {

	var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 archsimd.Float32x8
	xp, yp := unsafe.Pointer(x), unsafe.Pointer(y)
	i := 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		// The loop bound implies i < length; this guard keeps the second four
		// y vectors out of the first group's register live range.
		if i < length {
			acc4 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 16))), acc4)
			acc5 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 20))), acc5)
			acc6 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 24))), acc6)
			acc7 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 28))), acc7)
		}
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		xTail := tail.x
		acc0 = xTail.MulAdd(tail.loadY(yp), acc0)
		acc1 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 4)), acc1)
		acc2 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 8)), acc2)
		acc3 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 12)), acc3)
		acc4 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 16)), acc4)
		acc5 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 20)), acc5)
		acc6 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 24)), acc6)
		acc7 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 28)), acc7)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray((*[4]float32)(unsafe.Pointer(&sum[0])))
	reduceXcorrAVX8Four(acc4, acc5, acc6, acc7).StoreArray((*[4]float32)(unsafe.Pointer(&sum[4])))
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(
				unsafe.Slice(x, length), unsafe.Slice(y, length+7)[corr:], length)
		}
	}
}

func xcorrKernelAVX4(x, y *float32, sum *[4]float32, length int) {
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX4Tail(x, y, sum, length, &tail)
}

func xcorrKernelAVX4Tail(x, y *float32, sum *[4]float32, length int, tail *xcorrTail8) {
	var acc0, acc1, acc2, acc3 archsimd.Float32x8
	xp, yp := unsafe.Pointer(x), unsafe.Pointer(y)
	i := 0
	for ; i+16 <= length; i += 16 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		xp1 := unsafe.Add(xp, 32)
		yp1 := unsafe.Add(yp, 32)
		xv = archsimd.LoadFloat32x8Array((*[8]float32)(xp1))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp1)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 12))), acc3)
		xp = unsafe.Add(xp, 64)
		yp = unsafe.Add(yp, 64)
	}
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		xTail := tail.x
		acc0 = xTail.MulAdd(tail.loadY(yp), acc0)
		acc1 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 4)), acc1)
		acc2 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 8)), acc2)
		acc3 = xTail.MulAdd(tail.loadY(unsafe.Add(yp, 12)), acc3)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray(sum)
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(
				unsafe.Slice(x, length), unsafe.Slice(y, length+3)[corr:], length)
		}
	}
}

func loadXcorrTail8(p unsafe.Pointer, remaining int) archsimd.Float32x8 {
	// Read only valid tail lanes before loading the stack vector, so a tail at a
	// page boundary never turns into a full-width memory read. Integer loads keep
	// the bit patterns exact; the AVX zero store also avoids legacy SSE while
	// correlation accumulators are live.
	var lanes [8]uint32
	var zero archsimd.Uint32x8
	zero.StoreArray(&lanes)
	for lane := range 8 {
		if lane < remaining {
			lanes[lane] = *(*uint32)(unsafe.Add(p, uintptr(lane*4)))
		}
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

func xcorrKernelAVX8ScalarGo(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
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
		sum[corr] = reduceAVX2PitchSum(lanes[corr])
	}
}

func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
	// Clear the upper register halves the 256-bit kernel leaves dirty, so
	// the caller's scalar SSE code runs without false dependencies.
	archsimd.ClearAVXUpperBits()
}

// pitchXCorrAVX2Blocks runs celt_pitch_xcorr_avx2's eight-lag blocks for
// xcorr[0:maxPitch&^7] and returns the first lag it leaves to the scalar
// tail. The x tail block is prepared once, and a y tail block loads full
// width wherever y's capacity covers it.
func pitchXCorrAVX2Blocks(x, y, xcorr []float32, length, maxPitch int) int {
	if maxPitch < 8 || !archsimd.X86.FMA() {
		return 0
	}
	x = x[:length]
	blocks := maxPitch &^ 7
	_ = xcorr[blocks-1]
	_ = y[blocks+length-2]
	tail := newXcorrTail8(unsafe.Pointer(unsafe.SliceData(x)), length)
	// The widest masked load of lag i+7 reads y[i+7+length-rem : i+length-rem+15].
	fullFrom := cap(y) - (length - tail.rem + 15)
	yp := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	for ; i < blocks; i += 8 {
		tail.yFull = i <= fullFrom
		sum := (*[8]float32)(xcorr[i : i+8])
		xcorrKernelAVX8Tail(&x[0], (*float32)(unsafe.Add(yp, 4*i)), sum, length, &tail)
	}
	archsimd.ClearAVXUpperBits()
	return i
}
