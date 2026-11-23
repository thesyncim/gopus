package celt

import "unsafe"

// Compile-time layout assertions: both kissCpx (struct{ r, i float32 }) and
// complex64 occupy exactly two contiguous IEEE-754 float32 values with 4-byte
// alignment and zero padding.
var (
	_ [0]byte = [unsafe.Sizeof(kissCpx{}) - 2*unsafe.Sizeof(float32(0))]byte{}
	_ [0]byte = [unsafe.Sizeof(complex64(0)) - 2*unsafe.Sizeof(float32(0))]byte{}
)

// kissCpxAsFloat32 returns a zero-copy float32 view of s with interleaved
// {r, i} pairs (length 2*len(s)).
//
// Safety: kissCpx is struct{ r, i float32 } with zero padding (asserted at
// compile time above). The returned slice shares s's backing array and
// preserves its exact byte bounds, so every subsequent element load and store
// is bounds-checked by Go.
func kissCpxAsFloat32(s []kissCpx) []float32 {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(s))), 2*len(s))
}

// complex64AsFloat32 returns a zero-copy float32 view of s with interleaved
// real/imaginary pairs (length 2*len(s)).
//
// Safety: the Go language specification defines complex64 as two contiguous
// IEEE-754 float32 values (real, then imaginary) with identical 4-byte
// alignment and zero padding (asserted at compile time above). The returned
// slice shares s's backing array and preserves its exact byte bounds.
func complex64AsFloat32(s []complex64) []float32 {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(unsafe.SliceData(s))), 2*len(s))
}
