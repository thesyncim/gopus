//go:build gopus_qext

package celt

// Native 96 kHz CELT encode driver (Opus HD / QEXT).
//
// EnableHD96kMode switches a CELT encoder into the native 96 kHz HD mode
// (libopus mode96000_1920_240): 1920-sample frames, 3840-sample long MDCT,
// overlap 240, 8 short blocks. It mirrors the decoder's EnableHD96kMode
// (decoder_hd96k_decode_qext.go) on the analysis side: the base bands reuse the
// shared eBand5ms / logN400 layout, while the >20 kHz content is carried by the
// QEXT extension-band encode chain (qextEBands240).
//
// The deterministic analysis front-end is already mode-parametric and
// oracle-verified against the libopus QEXT reference:
//   - forward long/transient MDCT at the HD lengths (hd96kMDCTForward /
//     hd96kMDCTForwardShort in mdct_hd96k_qext.go), pinned bit-exact (amd64) in
//     mdct_hd96k_qext_test.go;
//   - the size-driven band-energy / normalisation / coarse+fine energy / PVQ
//     band quant kernels accept frameSize, lm and band-edge overrides.
//
// EnableHD96kMode threads the analysis overlap (240), the 2-tap HD pre-emphasis
// coefficients (HD96kMode.Preemph) and Fs=96000 into the encoder state, and
// grows/clears the overlap-history buffer for overlap=240, exactly as the
// decoder grows its synthesis overlap.
//
// What is NOT yet wired here (the remaining native-encode increments, tracked
// against the native 96 kHz encode oracle in
// internal/libopustest/qext_encode96k_oracle.go):
//   - The full EncodeFrame driver still threads the 48 kHz overlap constant
//     through transient analysis, the pre-filter and the MDCT-history helpers;
//     HD96k needs overlap=240 wired through all of those.
//   - The pre-filter comb (run_prefilter / comb_filter_qext) at the HD scale is
//     the analysis counterpart of the decode-side comb_filter_qext, which still
//     carries a cross-frame residual (postfilter_hd96k_qext.go).
//   - The >20 kHz QEXT extension-band ENCODE (qext.go / qext_alloc.go /
//     qext_energy.go / qext_header.go encode paths) is not yet driven into the
//     secondary range coder from the HD encode.
//   - The top-level Opus packet framing of the reserved extension payload
//     (encoder_96k_qext.go) still resamples 2:1 rather than emitting a native
//     96 kHz QEXT packet.

// EnableHD96kMode reconfigures the encoder analysis state for the native 96 kHz
// HD mode. It is idempotent and must be called before encoding 96 kHz frames.
// The per-channel overlap history is grown to overlap=240 and cleared the first
// time the mode is enabled.
func (e *Encoder) EnableHD96kMode() {
	m := NewHD96kMode()
	channels := int(e.channels)
	if channels < 1 {
		channels = 1
	}

	e.sampleRate = int32(m.Fs)
	e.hd96kOverlap = m.Overlap
	e.hd96kPreemph = m.Preemph

	if len(e.overlapBuffer) < m.Overlap*channels {
		e.overlapBuffer = make([]celtSig, m.Overlap*channels)
	}
	for i := range e.overlapBuffer {
		e.overlapBuffer[i] = 0
	}
	for i := range e.preemphState {
		e.preemphState[i] = 0
	}
	e.overlapMax = 0
}

// HD96kEncodeEnabled reports whether the encoder is in the native 96 kHz HD
// analysis mode.
func (e *Encoder) HD96kEncodeEnabled() bool {
	return e.hd96kOverlap == 240 && e.sampleRate == 96000
}

// analysisOverlap returns the MDCT-analysis overlap for the active mode: the
// HD overlap (240) when the native 96 kHz mode is enabled, otherwise the 48 kHz
// package constant. The 48 kHz path is unchanged (hd96kOverlap == 0).
func (e *Encoder) analysisOverlap() int {
	if e.hd96kOverlap > 0 {
		return e.hd96kOverlap
	}
	return Overlap
}
