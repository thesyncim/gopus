//go:build gopus_qext

package celt

// Native 96 kHz (HD / QEXT) comb-filter prefilter core.
//
// At 96 kHz libopus comb_filter dispatches to comb_filter_qext when
// mode->overlap==240 (celt/celt.c). The prefilter (run_prefilter) calls
// comb_filter with x != y (the de-emphasised input pre[c] as the delay line,
// the in[] frame as the destination), so this is the x!=y branch of
// comb_filter_qext: each even/odd sample phase is filtered independently with a
// half-rate window (new_window[i] = window[2*i+s]) at N/2 and overlap/2, reading
// 2*COMBFILTER_MAXPERIOD samples of input history (x[2*i+s-2*COMBFILTER_MAXPERIOD]).
// This doubles the comb period and tap spacing, mirroring the filter around
// 24 kHz.

// combFilterWithInputSigQEXT applies libopus comb_filter_qext for the prefilter
// (x != y). It filters dst[start:start+n] from the src delay line, splitting
// src into even/odd phases that reach back 2*COMBFILTER_MAXPERIOD samples before
// start, running the plain comb filter on each phase at n/2 with overlap/2 and a
// half-rate window, then re-interleaving into dst.
func combFilterWithInputSigQEXT(dst, src []celtSig, start, t0, t1, n int, g0, g1 float32, tapset0, tapset1 int, window []float32, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		copy(dst[start:start+n], src[start:start+n])
		return
	}
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}
	n2 := n / 2
	overlap2 := overlap / 2
	if n2 <= 0 {
		return
	}

	newWindow := make([]float32, overlap2)
	phase := make([]float32, combFilterMaxPeriod+n2)

	for s := 0; s < 2; s++ {
		for i := 0; i < overlap2; i++ {
			newWindow[i] = window[2*i+s]
		}
		// mem_buf[i] = x[2*i+s - 2*COMBFILTER_MAXPERIOD], x indexed from start.
		for i := 0; i < combFilterMaxPeriod+n2; i++ {
			phase[i] = float32(src[start+2*i+s-hd96kCombHistory])
		}
		combFilterScalarFloat32(phase, combFilterMaxPeriod, t0, t1, n2, g0, g1, tapset0, tapset1, newWindow, overlap2)
		for i := 0; i < n2; i++ {
			dst[start+2*i+s] = celtSig(phase[combFilterMaxPeriod+i])
		}
	}
}
