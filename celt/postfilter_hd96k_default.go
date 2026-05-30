//go:build !gopus_qext

package celt

// Default-build stubs for the native 96 kHz comb-filter postfilter. The HD96k
// mode is only reachable under the gopus_qext build tag, so these are never
// active in the default build (synthOverlap stays 0).

func (d *Decoder) hd96kPostfilterActive() bool { return false }

func (d *Decoder) applyHD96kPostfilterInterleaved(_ []float32, _, _ int, _ int, _ float32, _ int) {
}
