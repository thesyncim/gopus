//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// stereoSplitInto runs stereo_split() over x and y of equal length. Four
// lanes form the same rounded products, sum and difference as the scalar
// loop, as the auto-vectorized libopus loop does; the remainder runs scalar.
func stereoSplitInto(x, y []celtNorm) {
	y = y[:len(x)]
	if !archsimd.X86.AVX() {
		stereoSplitScalar(x, y)
		return
	}
	stereoSplitIntoAVX(x, y)
}

//go:noinline
func stereoSplitIntoAVX(x, y []celtNorm) {
	blocks := len(x) &^ 3
	if blocks > 0 {
		c := broadcastF32x4Arch(stereoSplitInvSqrt2)
		xp := unsafe.Pointer(unsafe.SliceData(x))
		yp := unsafe.Pointer(unsafe.SliceData(y))
		for j := 0; j < blocks; j += 4 {
			off := uintptr(j) * 4
			l := loadF32x4(unsafe.Add(xp, off)).Mul(c)
			r := loadF32x4(unsafe.Add(yp, off)).Mul(c)
			storeF32x4(unsafe.Add(xp, off), l.Add(r))
			storeF32x4(unsafe.Add(yp, off), r.Sub(l))
		}
	}
	stereoSplitScalar(x[blocks:], y[blocks:])
}
