//go:build gopus_fixedpoint

package silk

// This file ports the LPC-whitening front-end of silk_find_pitch_lags_FIX from
// silk/fixed/find_pitch_lags_FIX.c. It wires the already-ported sine window
// (silkApplySineWindowFIX), autocorrelation (silkAutocorrFixed), Schur
// recursion (silkSchur), reflection-to-prediction conversion (silkK2a),
// bandwidth expander (silkBwExpander), and the LPC analysis filter
// (silkLPCAnalysisFilterFixed) into the windowed Burg-free whitening that
// produces the residual fed to the pitch estimator, plus the prediction gain.
//
// The trailing call into silk_pitch_analysis_core (the stage-1/2/3 contour
// search) is NOT performed here: the full FIXED_POINT silk_pitch_analysis_core
// is not yet ported (only the stage-3 energy/correlation kernels exist:
// silkPAnaCalcEnergySt3Fixed / silkPAnaCalcCorrSt3Fixed). The front-end is
// bit-exact and fully determines the whitened residual the core consumes; the
// core search and the threshold/signalType decision that depend on it remain
// to be ported once silk_pitch_analysis_core lands.

// maxFindPitchLPCOrder is MAX_FIND_PITCH_LPC_ORDER from silk/define.h, the
// maximum LPC order used by the pitch-lag whitening analysis.
const maxFindPitchLPCOrder = 16

// findPitchWhiteNoiseFractionQ16 is SILK_FIX_CONST(FIND_PITCH_WHITE_NOISE_FRACTION, 16)
// with FIND_PITCH_WHITE_NOISE_FRACTION == 1e-3 (tuning_parameters.h):
// round(1e-3 * 2^16) = 66.
const findPitchWhiteNoiseFractionQ16 = 66

// findPitchBandwidthExpansionQ16 is SILK_FIX_CONST(FIND_PITCH_BANDWIDTH_EXPANSION, 16)
// with FIND_PITCH_BANDWIDTH_EXPANSION == 0.99 (tuning_parameters.h):
// round(0.99 * 2^16) = 64881.
const findPitchBandwidthExpansionQ16 = 64881

// silkFindPitchLagsInput collects the silk_encoder_state fields read by the
// front-end of silk_find_pitch_lags_FIX together with the input speech signal.
type silkFindPitchLagsInput struct {
	laPitch                int // psEnc->sCmn.la_pitch
	frameLength            int // psEnc->sCmn.frame_length
	ltpMemLength           int // psEnc->sCmn.ltp_mem_length
	pitchLPCWinLength      int // psEnc->sCmn.pitch_LPC_win_length
	pitchEstimationLPCOrder int // psEnc->sCmn.pitchEstimationLPCOrder
	// x holds buf_len = la_pitch + frame_length + ltp_mem_length samples.
	x []int16
}

// silkFindPitchLagsResult carries the front-end outputs: the whitened residual
// (buf_len samples) and the prediction gain.
type silkFindPitchLagsResult struct {
	res         []int16
	predGainQ16 int32
}

// silkFindPitchLagsFIXFrontEnd is the bit-exact Go port of the LPC-whitening
// front-end of silk_find_pitch_lags_FIX (everything up to and including the
// silk_LPC_analysis_filter call that produces res).
func silkFindPitchLagsFIXFrontEnd(in *silkFindPitchLagsInput) silkFindPitchLagsResult {
	bufLen := in.laPitch + in.frameLength + in.ltpMemLength
	order := in.pitchEstimationLPCOrder

	// Calculate windowed signal.
	wsig := make([]int16, in.pitchLPCWinLength)

	// First LA_LTP samples: sine window onset.
	xOff := bufLen - in.pitchLPCWinLength
	wOff := 0
	silkApplySineWindowFIX(wsig[wOff:], in.x[xOff:], 1, in.laPitch)

	// Middle un-windowed samples.
	wOff += in.laPitch
	xOff += in.laPitch
	midLen := in.pitchLPCWinLength - in.laPitch<<1
	copy(wsig[wOff:wOff+midLen], in.x[xOff:xOff+midLen])

	// Last LA_LTP samples: sine window decay.
	wOff += midLen
	xOff += midLen
	silkApplySineWindowFIX(wsig[wOff:], in.x[xOff:], 2, in.laPitch)

	// Calculate autocorrelation sequence.
	autoCorr := make([]int32, maxFindPitchLPCOrder+1)
	var scale int
	silkAutocorrFixed(autoCorr, &scale, wsig, in.pitchLPCWinLength, order+1)

	// Add white noise, as fraction of energy.
	autoCorr[0] = silkSMLAWB(autoCorr[0], autoCorr[0], findPitchWhiteNoiseFractionQ16) + 1

	// Calculate the reflection coefficients using schur.
	var rcQ15 [maxFindPitchLPCOrder]int16
	resNrg := silkSchur(rcQ15[:order], autoCorr, int32(order))

	// Prediction gain.
	predGainQ16 := silkDiv32VarQ(autoCorr[0], int32(silkMaxInt(int(resNrg), 1)), 16)

	// Convert reflection coefficients to prediction coefficients.
	var aQ24 [maxFindPitchLPCOrder]int32
	silkK2a(aQ24[:order], rcQ15[:order], int32(order))

	// Convert from 32 bit Q24 to 16 bit Q12 coefs.
	var aQ12 [maxFindPitchLPCOrder]int16
	for i := 0; i < order; i++ {
		aQ12[i] = silkSAT16(silkRSHIFT(aQ24[i], 12))
	}

	// Do BWE.
	silkBwExpander(aQ12[:order], findPitchBandwidthExpansionQ16)

	// LPC analysis filtering, producing the whitened residual.
	res := make([]int16, bufLen)
	silkLPCAnalysisFilterFixed(res, in.x, aQ12[:order], bufLen, order)

	return silkFindPitchLagsResult{
		res:         res,
		predGainQ16: predGainQ16,
	}
}
