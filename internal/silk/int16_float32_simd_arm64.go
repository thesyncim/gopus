//go:build arm64 && goexperiment.simd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

// writeInt16AsFloat32Core writes dst[i] = float32(src[i]) * (1/32768) for the
// first n elements with archsimd. Each int16 is sign-extended to int32, converted
// to float32, then multiplied by the exact-in-float32 scale 1/32768 — the same
// per-element op sequence as the scalar reference (int16_float32_default.go) and
// the hand NEON kernel (SSHLL/SCVTF/FMUL), so it is bit-exact on every config.
//
// Each 8-wide block loads one Int16x8, widens the low 4 lanes with ExtendLo4ToInt32
// and the high 4 lanes with HiToLo().ExtendLo4ToInt32() (HiToLo moves the upper 4
// int16 into the low position so ExtendLo4 reaches them — arm64 has no ExtendHi4).
// Loads/stores go through raw advancing pointers to drop the per-access slice
// bounds check; the remainder runs the scalar loop.
func writeInt16AsFloat32Core(dst []float32, src []int16, n int) {
	_ = dst[n-1]
	_ = src[n-1]
	const inv32768 = 1.0 / 32768.0
	scale := archsimd.BroadcastFloat32x4(inv32768)
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	i := 0
	for ; i+8 <= n; i += 8 {
		v := loadInt16x8(sp)
		lo := v.ExtendLo4ToInt32().ConvertToFloat32().Mul(scale)
		hi := v.HiToLo().ExtendLo4ToInt32().ConvertToFloat32().Mul(scale)
		storeF32x4(dp, lo)
		storeF32x4(unsafe.Add(dp, 16), hi)
		sp = unsafe.Add(sp, 16)
		dp = unsafe.Add(dp, 32)
	}
	for ; i < n; i++ {
		*(*float32)(dp) = float32(*(*int16)(sp)) * inv32768
		sp = unsafe.Add(sp, 2)
		dp = unsafe.Add(dp, 4)
	}
}
