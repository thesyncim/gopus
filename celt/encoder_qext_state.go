package celt

import (
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/rangecoding"
)

type encoderQEXTState struct {
	enabled     bool
	lastPayload []byte
}

type encoderQEXTScratch struct {
	buf       []byte
	extraBits []int
	fineBits  []int
	bandE     []float64
	bandLogE  []float64
	quantized []float64
	qerr      []float64
	oldBandE  []float64
	normL     []float64
	normR     []float64
	encoder   rangecoding.Encoder
}

func (e *Encoder) ensureQEXTState() *encoderQEXTState {
	if e.qext == nil {
		e.qext = &encoderQEXTState{}
	}
	return e.qext
}

func (e *Encoder) qextActive() bool {
	return extsupport.QEXT && e.qext != nil && e.qext.enabled
}

func (e *Encoder) clearLastQEXTPayload() {
	if e.qext != nil {
		e.qext.lastPayload = e.qext.lastPayload[:0]
	}
}

func (e *Encoder) setLastQEXTPayload(payload []byte) {
	e.ensureQEXTState().lastPayload = payload
}

func (s *encoderScratch) ensureQEXTScratch() *encoderQEXTScratch {
	if s.qext == nil {
		s.qext = &encoderQEXTScratch{}
	}
	return s.qext
}
