//go:build arm64 && !purego

package celt

import "unsafe"

func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int) {
	if len(fftIn) == 0 {
		return
	}
	out := unsafe.Slice((*float32)(unsafe.Pointer(&fftIn[0])), len(fftIn)*2)
	imdctPreRotateF32Asm(out, spectrum, trig, n2, n4)
}

//go:noescape
func imdctPreRotateF32Asm(out []float32, spectrum []float64, trig []float32, n2, n4 int)
