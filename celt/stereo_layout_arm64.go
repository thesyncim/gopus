//go:build arm64

package celt

//go:noescape
func deinterleaveStereoIntoImpl(interleaved, left, right []float64, n int)

//go:noescape
func interleaveStereoIntoImpl(left, right, interleaved []float64, n int)
