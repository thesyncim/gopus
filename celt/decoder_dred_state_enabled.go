//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package celt

type decoderDREDState struct {
	scratchPLCUpdate48k  []float32
	scratchPLCDREDNeural []float32
	scratchPLCDREDBase   []float64
}
