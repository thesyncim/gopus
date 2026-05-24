package celt

import "github.com/thesyncim/gopus/rangecoding"

type decoderQEXTState struct {
	pendingPayload []byte
	oldBandE       []celtGLog

	scratchEnergies  []celtGLog
	scratchSpectrumL []float64
	scratchSpectrumR []float64
	scratchDecode    preparedQEXTDecode
	scratchBands     bandDecodeScratch
	scratchPulses    []int
	scratchFineQuant []int

	rangeDecoderScratch rangecoding.Decoder
}
