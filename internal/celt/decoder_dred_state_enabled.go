//go:build gopus_dred || gopus_extra_controls

package celt

type decoderDREDState struct {
	scratchPLCUpdate48k  []float32
	scratchPLCDREDNeural []float32
	scratchPLCDREDFrame  []float32
	scratchPLCDREDBase   []celtSig
}
