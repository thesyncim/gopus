//go:build amd64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// loadF32x8 / storeF32x8 are the 256-bit AVX counterparts of loadF32x4: a raw
// 8-lane load/store with no slice bounds check. Callers guarantee the pointer
// addresses eight in-range float32s.
func loadF32x8(p unsafe.Pointer) archsimd.Float32x8 {
	return archsimd.LoadFloat32x8Array((*[8]float32)(p))
}

func storeF32x8(p unsafe.Pointer, v archsimd.Float32x8) {
	v.StoreArray((*[8]float32)(p))
}
