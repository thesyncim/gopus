//go:build (amd64 || arm64) && goexperiment.simd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

// loadInt16x8 / loadInt32x4 / loadF32x4 read a vector through a raw pointer,
// skipping the slice length bounds check that the archsimd.Load*(s[i:]) slice
// forms emit on every call. That check machinery — not the SIMD — is what makes
// the slice form several times slower than the hand asm on load/store-bound
// kernels, so the pointer form is the difference between losing to and beating
// the asm. Each inlines to a bare vector load; callers guarantee the pointer
// addresses the in-range lanes.
func loadInt16x8(p unsafe.Pointer) archsimd.Int16x8 {
	return archsimd.LoadInt16x8Array((*[8]int16)(p))
}

func loadF32x4(p unsafe.Pointer) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(p))
}

// storeInt16x8 / storeF32x4 write a vector through a raw pointer, likewise
// avoiding the slice-store bounds check.
func storeInt16x8(p unsafe.Pointer, v archsimd.Int16x8) {
	v.StoreArray((*[8]int16)(p))
}

func storeF32x4(p unsafe.Pointer, v archsimd.Float32x4) {
	v.StoreArray((*[4]float32)(p))
}
