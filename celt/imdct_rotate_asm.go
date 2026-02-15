//go:build arm64 || amd64

package celt

// imdctPreRotateF32 performs the IMDCT pre-rotation using platform-optimized assembly.
// It writes directly into fftIn backing storage as interleaved float32 (re, im).
//
//go:noescape
func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int)

// imdctPostRotateF32 performs the IMDCT post-rotation using platform-optimized assembly.
//
//go:noescape
func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int)
