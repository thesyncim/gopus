//go:build gopus_fixed_point && gopus_qext

package fixedpoint

// combFilterPFFixedWindow follows the coefficient width selected by the
// ENABLE_QEXT build. CELT's 48 kHz mode stores its overlap window as Q31 even
// when the runtime QEXT side coder is disabled.
func combFilterPFFixedWindow(y []int32, yOff int, x []int32, xOff, t0, t1, n int,
	g0, g1 int16, tapset0, tapset1 int, _ []int16, overlap int,
) {
	window := staticQEXTMDCT48000Window[:0]
	if overlap > len(staticQEXTMDCT48000Window) {
		panic("fixed-QEXT 48 kHz prefilter overlap exceeds Q31 window")
	}
	if overlap > 0 {
		window = staticQEXTMDCT48000Window[:overlap]
	}
	CombFilterQEXTPF(y, yOff, x, xOff, t0, t1, n, g0, g1,
		tapset0, tapset1, window, overlap)
}
