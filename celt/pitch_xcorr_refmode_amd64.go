//go:build amd64 && !purego

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return amd64UsePitchXcorrAVX2FMA
}

func pitchFMADD32(a, b, c float32) float32 {
	return pitchFMADD32AVXFMA(a, b, c)
}

//go:noescape
func pitchFMADD32AVXFMA(a, b, c float32) float32
