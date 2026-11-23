//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package encoder

import "unsafe"

// complex64AsFloat32 returns a zero-copy float32 view of s with interleaved
// real/imaginary pairs (length 2*len(s)).
//
// Safety: the Go language specification defines complex64 as two contiguous
// IEEE-754 float32 values (real, then imaginary) with identical 4-byte alignment
// and zero padding (unsafe.Sizeof(complex64(0)) == 2*unsafe.Sizeof(float32(0))).
// The returned slice shares s's backing array and preserves its exact byte
// bounds, so all subsequent indexing is bounds-checked by Go.
func complex64AsFloat32(s []complex64) []float32 {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(s))), 2*len(s))
}
