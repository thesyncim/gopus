//go:build gopus_tmp_env

package celt

import "strconv"

var tmpPVQAbsQ15Enabled = tmpGetenv("GOPUS_TMP_PVQ_ABS_Q15") == "1"

var tmpPVQIdxBiasValue, tmpPVQIdxBiasEnabled = func() (float32, bool) {
	s := tmpGetenv("GOPUS_TMP_PVQ_IDX_BIAS")
	if s == "" || s == "0" {
		return 0, false
	}
	if s == "1" {
		return 0.000003, true
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return 0, false
	}
	return float32(v), true
}()

var tmpEnergyPredMulNativeEnabled = tmpGetenv("GOPUS_TMP_ENERGY_PRED_MUL_NATIVE") == "1"
var tmpCoarseDumpEnabled = tmpGetenv("GOPUS_TMP_COARSE_DUMP") == "1"
var tmpFineDumpEnabled = tmpGetenv("GOPUS_TMP_FINE_DUMP") == "1"

var tmpFineQEpsValue, tmpFineQEpsEnabled = func() (float64, bool) {
	s := tmpGetenv("GOPUS_TMP_FINE_Q_EPS")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}()

var tmpCombFilterSeqAccumEnabled = tmpGetenv("GOPUS_TMP_COMBFILTER_SEQ_ACCUM") == "1"
var tmpCombFilterFMAOverlapEnabled = tmpGetenv("GOPUS_TMP_COMBFILTER_FMA_OVERLAP") == "1"

var tmpDisableHAARASMEnabled = tmpGetenv("GOPUS_TMP_DISABLE_HAAR_ASM") == "1"
var tmpExpRotF32Enabled = tmpGetenv("GOPUS_TMP_EXP_ROT_F32") == "1"
var tmpRenormF32RefEnabled = tmpGetenv("GOPUS_TMP_RENORM_F32_REF") == "1"
var tmpPVQDumpEnabled = tmpGetenv("GOPUS_TMP_PVQ_DUMP") == "1"
var tmpPVQInputF32Enabled = tmpGetenv("GOPUS_TMP_PVQ_INPUT_F32") == "1"
var tmpIThetaF32Enabled = tmpGetenv("GOPUS_TMP_ITHETA_F32") == "1"
var tmpGainF32Enabled = tmpGetenv("GOPUS_TMP_GAIN_F32") == "1"
var tmpQuantPartF32Enabled = tmpGetenv("GOPUS_TMP_QUANT_PART_F32") == "1"
var tmpUseQ30CosSplitEnabled = tmpGetenv("GOPUS_TMP_USE_Q30_COS_SPLIT") == "1"
var tmpRoundBeforePVQEnabled = tmpGetenv("GOPUS_TMP_ROUND_BEFORE_PVQ") == "1"
var tmpQDebugDecEnabled = tmpGetenv("GOPUS_TMP_QDBG_DEC") == "1"
var tmpQuantBandF32Enabled = tmpGetenv("GOPUS_TMP_QUANT_BAND_F32") == "1"
var tmpLowbandOutF32Enabled = tmpGetenv("GOPUS_TMP_LOWBAND_OUT_F32") == "1"
var tmpRoundBandStateF32Enabled = tmpGetenv("GOPUS_TMP_ROUND_BAND_STATE_F32") == "1"
