package celt

func antiCollapse(
	coeffsL, coeffsR []celtNorm,
	collapse []byte,
	lm int,
	channels int,
	start, end int,
	logE, prev1LogE, prev2LogE []float64,
	pulses []int,
	seed uint32,
) {
	logEGLog := float64sToGLogs(logE)
	prev1GLog := float64sToGLogs(prev1LogE)
	prev2GLog := float64sToGLogs(prev2LogE)
	antiCollapseGLog(coeffsL, coeffsR, collapse, lm, channels, start, end, logEGLog, prev1GLog, prev2GLog, pulses, seed)
}
