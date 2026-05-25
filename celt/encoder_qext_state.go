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
	bandE     []celtEner
	bandLogE  []celtGLog
	quantized []celtGLog
	qerr      []celtGLog
	oldBandE  []celtGLog
	normL     []celtNorm
	normR     []celtNorm
	encoder   rangecoding.Encoder
}
