package celt

import "github.com/thesyncim/gopus/rangecoding"

type decoderQEXTState struct {
	pendingPayload []byte
	oldBandE       []float64

	scratchEnergies  []float64
	scratchSpectrumL []float64
	scratchSpectrumR []float64
	scratchDecode    preparedQEXTDecode
	scratchBands     bandDecodeScratch
	scratchPulses    []int
	scratchFineQuant []int

	rangeDecoderScratch rangecoding.Decoder
}

func (d *Decoder) ensureQEXTState() *decoderQEXTState {
	if d.qext == nil {
		d.qext = &decoderQEXTState{}
	}
	return d.qext
}
