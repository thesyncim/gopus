//go:build amd64 && !purego

package celt

import (
	"unsafe"

	"golang.org/x/sys/cpu"
)

var imdctPreRotateF32Impl = imdctPreRotateF32Out
var imdctPostRotateF32Impl = imdctPostRotateF32Go

func init() {
	if cpu.X86.HasAVX2 {
		imdctPreRotateF32Impl = imdctPreRotateF32AVX2
		imdctPostRotateF32Impl = imdctPostRotateF32AVX2
		return
	}
	if cpu.X86.HasAVX {
		imdctPreRotateF32Impl = imdctPreRotateF32AVX
		imdctPostRotateF32Impl = imdctPostRotateF32AVX
	}
}

func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int) {
	if len(fftIn) == 0 {
		return
	}
	out := unsafe.Slice((*float32)(unsafe.Pointer(&fftIn[0])), len(fftIn)*2)
	imdctPreRotateF32Impl(out, spectrum, trig, n2, n4)
}

func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int) {
	imdctPostRotateF32Impl(buf, trig, n2, n4)
}

//go:noescape
func imdctPreRotateF32AVX(out []float32, spectrum []float64, trig []float32, n2, n4 int)

//go:noescape
func imdctPreRotateF32AVX2(out []float32, spectrum []float64, trig []float32, n2, n4 int)

//go:noescape
func imdctPostRotateF32AVX(buf []float32, trig []float32, n2, n4 int)

//go:noescape
func imdctPostRotateF32AVX2(buf []float32, trig []float32, n2, n4 int)
