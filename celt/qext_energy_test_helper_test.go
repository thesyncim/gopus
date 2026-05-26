//go:build gopus_qext

package celt

func computeQEXTBandAmplitudesInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner) {
	coeffs := float32Slice(mdctCoeffs)
	computeQEXTBandAmplitudesF32Into(coeffs, cfg, end, lm, bandE)
}

func computeQEXTBandLogEInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner, bandLogE []celtGLog) {
	coeffs := float32Slice(mdctCoeffs)
	computeQEXTBandLogEF32Into(coeffs, cfg, end, lm, bandE, bandLogE)
}

func normalizeQEXTBandsInto(mdctCoeffs []float64, cfg *qextModeConfig, end, lm int, bandE []celtEner, norm []celtNorm) {
	coeffs := float32Slice(mdctCoeffs)
	normalizeQEXTBandsF32Into(coeffs, cfg, end, lm, bandE, norm)
}
