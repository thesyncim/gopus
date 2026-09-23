//go:build (amd64 || arm64) && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// loadF32x4 / storeF32x4 read and write a 4-lane vector through a raw pointer,
// skipping the slice length bounds check that archsimd.LoadFloat32x4(s[i:]) and
// .Store(s[i:]) emit on every call. That check machinery — not the SIMD — is what
// makes the slice form several times slower than the hand asm on load/store-bound
// kernels, so the pointer form is the difference between losing to and beating the
// asm. Both inline to a bare vector load/store; callers guarantee the pointer
// addresses four in-range float32s.
func loadF32x4(p unsafe.Pointer) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(p))
}

func storeF32x4(p unsafe.Pointer, v archsimd.Float32x4) {
	v.StoreArray((*[4]float32)(p))
}
