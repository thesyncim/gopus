package dred

import (
	"errors"

	"github.com/thesyncim/gopus/rangecoding"
)

var errInvalidHeader = errors.New("dred: invalid experimental header")

// Header mirrors the low-cost metadata libopus decodes before the model-heavy
// DRED processing stage.
type Header struct {
	Q0              int
	DQ              int
	QMax            int
	ExtraOffset     int
	DredOffset      int
	DredFrameOffset int
}

var dqTable = [...]int{0, 2, 3, 4, 6, 8, 12, 16}

// OffsetSamples converts the parsed DRED offset from 2.5 ms units to samples
// at the caller's sample rate.
func (h Header) OffsetSamples(sampleRate int) int {
	return h.DredOffset * sampleRate / 400
}

// EndSamples mirrors opus_dred_parse()'s dred_end output: the number of
// trailing silence samples between the DRED timestamp and the last usable DRED
// sample.
func (h Header) EndSamples(sampleRate int) int {
	offset := h.OffsetSamples(sampleRate)
	if offset < 0 {
		return -offset
	}
	return 0
}

// ParseHeader decodes the lightweight libopus DRED header from a payload body
// with the temporary extension prefix already stripped. dredFrameOffset is in
// 2.5 ms units, matching libopus dred_find_payload().
func ParseHeader(payload []byte, dredFrameOffset int) (Header, error) {
	if len(payload) < MinBytes {
		return Header{}, errInvalidHeader
	}

	var rd rangecoding.Decoder
	rd.Init(payload)

	q0 := int(rd.DecodeUniform(16))
	dq := int(rd.DecodeUniform(8))

	extraOffset := 0
	if rd.DecodeUniform(2) != 0 {
		extraOffset = 32 * int(rd.DecodeUniform(256))
	}

	dredOffset := 16 - int(rd.DecodeUniform(32)) - extraOffset + dredFrameOffset
	qmax := 15
	if q0 < 14 && dq > 0 {
		nvals := 15 - (q0 + 1)
		s := int(rd.Decode(uint32(2 * nvals)))
		if s >= nvals {
			qmax = q0 + (s - nvals) + 1
			rd.Update(uint32(s), uint32(s+1), uint32(2*nvals))
		} else {
			rd.Update(0, uint32(nvals), uint32(2*nvals))
		}
	}

	return Header{
		Q0:              q0,
		DQ:              dq,
		QMax:            qmax,
		ExtraOffset:     extraOffset,
		DredOffset:      dredOffset,
		DredFrameOffset: dredFrameOffset,
	}, nil
}

// QuantizerLevel mirrors libopus compute_quantizer() for the parsed DRED
// quantizer metadata.
func (h Header) QuantizerLevel(i int) int {
	if i < 0 {
		i = 0
	}
	dq := h.DQ
	if dq < 0 {
		dq = 0
	}
	if dq >= len(dqTable) {
		dq = len(dqTable) - 1
	}
	quant := h.Q0 + (dqTable[dq]*i+8)/16
	if quant > h.QMax {
		return h.QMax
	}
	return quant
}
