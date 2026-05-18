//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

// osceLACECrossFade10ms mirrors libopus dnn/osce_features.c
// `osce_cross_fade_10ms`. It blends the first 10 ms of an enhanced 16 kHz
// LACE/NoLACE output buffer (`xEnhanced`) against the raw, pre-enhancement
// input (`xIn`) using the libopus `osce_window[]` half-window weights:
//
//	x_enhanced[i] = w[i] * x_enhanced[i] + (1 - w[i]) * x_in[i]    for i in [0, 160)
//
// The libopus implementation operates on float32 buffers; the helper writes
// the cross-fade back into `xEnhanced` so callers can keep using it as the
// effective LACE/NoLACE output. The buffer length must be at least 160
// samples (10 ms @ 16 kHz). The trailing 160 samples are left untouched so
// callers can continue to consume the enhanced LACE/NoLACE output for the
// second half of the 20 ms frame.
//
// libopus invokes this helper on the frame immediately after a non-LACE ->
// LACE/NoLACE mode transition (`psDec->osce.features.reset == 1`). The
// gopus wiring drives the same fade on the first SILK postfilter-active
// frame after a non-postfilter frame (mirroring `prevLACEActive` flipping
// from false to true).
//
// Re-uses the `osceWindow` constant table from
// `decoder_osce_bwe_crossfade.go` so the LACE / BWE cross-fades share the
// same 320-entry weight table that ships with libopus.
func osceLACECrossFade10ms(xEnhanced, xIn []float32, length int) {
	if length < 160 {
		return
	}
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := osceWindow[i]
		xEnhanced[i] = w*xEnhanced[i] + (1.0-w)*xIn[i]
	}
}

// osceLACECrossFade10msInt16 mirrors osceLACECrossFade10ms but operates on
// int16 PCM buffers (the SILK native lowband). The libopus float-domain
// cross-fade is performed in floating point and re-quantised; this helper
// preserves that ordering so the result matches libopus to within one int16
// quantisation step.
//
// `xEnhanced` is the postfilter output (the fade-in buffer); `xIn` is the
// raw pre-enhancement input (the fade-out buffer). The first 160 samples
// of `xEnhanced` are overwritten with the cross-faded mix. The trailing
// samples are left untouched.
func osceLACECrossFade10msInt16(xEnhanced, xIn []int16, length int) {
	if length < 160 {
		return
	}
	if len(xEnhanced) < 160 || len(xIn) < 160 {
		return
	}
	for i := 0; i < 160; i++ {
		w := osceWindow[i]
		enh := float32(xEnhanced[i]) * (1.0 / 32768.0)
		raw := float32(xIn[i]) * (1.0 / 32768.0)
		mix := w*enh + (1.0-w)*raw
		v := mix * 32768.0
		if v > 32767.0 {
			v = 32767.0
		} else if v < -32768.0 {
			v = -32768.0
		}
		xEnhanced[i] = int16(v)
	}
}
