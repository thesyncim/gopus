package testvectors

import "github.com/thesyncim/gopus/silk"

func compareNSQTraceWithLibopus(tr silk.NSQTrace) string {
	_ = tr
	return ""
}

type libopusNSQStateSnapshot struct {
	XQ            []int16
	SLTPShpQ14    []int32
	SLPCQ14       []int32
	SAR2Q14       []int32
	LFARQ14       int32
	DiffQ14       int32
	LagPrev       int
	SLTPBufIdx    int
	SLTPShpBufIdx int
	RandSeed      int32
	PrevGainQ16   int32
	RewhiteFlag   int
}

func captureLibopusNSQState(samples []float32, sampleRate, bitrate, frameSize, frameIndex int) (libopusNSQStateSnapshot, bool) {
	_ = samples
	_ = sampleRate
	_ = bitrate
	_ = frameSize
	_ = frameIndex
	return libopusNSQStateSnapshot{}, false
}
