//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// combUsesNeon selects the archsimd constant-gain comb filter on the arm64
// experiment build.
const combUsesNeon = true

// combFilterConstNeon applies the 5-tap symmetric comb filter
//
//	dst[j] += g10*delay[j+2] + g11*(delay[j+1]+delay[j+3]) + g12*(delay[j]+delay[j+4])
//
// four lanes at a time. The two tap sums round as plain FADDs and the three
// accumulates are fused FMLAs (MulAdd), matching the scalar combFilterConstValue
// and the hand asm bit-for-bit. Loads go through raw pointers (loadF32x4) to skip
// the per-access slice bounds check; n = 4*blocks is a multiple of four.
func combFilterConstNeon(dst, delay []float32, g10, g11, g12 float32, blocks int) {
	n := 4 * blocks
	g10v := archsimd.BroadcastFloat32x4(g10)
	g11v := archsimd.BroadcastFloat32x4(g11)
	g12v := archsimd.BroadcastFloat32x4(g12)
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	yp := unsafe.Pointer(unsafe.SliceData(delay))
	for j := 0; j+4 <= n; j += 4 {
		base := loadF32x4(unsafe.Add(dp, j*4))
		center := loadF32x4(unsafe.Add(yp, (j+2)*4))
		s1 := loadF32x4(unsafe.Add(yp, (j+3)*4)).Add(loadF32x4(unsafe.Add(yp, (j+1)*4)))
		s2 := loadF32x4(unsafe.Add(yp, (j+4)*4)).Add(loadF32x4(unsafe.Add(yp, j*4)))
		sum := center.MulAdd(g10v, base)
		sum = s1.MulAdd(g11v, sum)
		sum = s2.MulAdd(g12v, sum)
		storeF32x4(unsafe.Add(dp, j*4), sum)
	}
}
