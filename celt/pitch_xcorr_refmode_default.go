//go:build !amd64 || purego

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return false
}

func pitchFMADD32(a, b, c float32) float32 {
	return noFMA32Add(noFMA32Mul(a, b), c)
}
