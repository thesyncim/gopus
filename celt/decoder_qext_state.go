package celt

import "github.com/thesyncim/gopus/rangecoding"

type decoderQEXTState struct {
	pendingPayload []byte
	oldBandE       []celtGLog

	scratchEnergies  []celtGLog
	scratchDecode    preparedQEXTDecode
	scratchBands     bandDecodeScratch
	scratchPulses    []int32
	scratchFineQuant []int32

	rangeDecoderScratch rangecoding.Decoder
}
