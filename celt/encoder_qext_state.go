package celt

import "github.com/thesyncim/gopus/rangecoding"

type encoderQEXTState struct {
	enabled     bool
	lastPayload []byte
}

type encoderQEXTScratch struct {
	buf       []byte
	extraBits []int
	fineBits  []int
	bandE     []float64
	bandLogE  []celtGLog
	quantized []celtGLog
	qerr      []celtGLog
	oldBandE  []celtGLog
	normL     []float64
	normR     []float64
	encoder   rangecoding.Encoder
}
