//go:build gopus_tmp_env

package celt

var debugStereoMergeEnabled = tmpGetenv("GOPUS_TMP_DEBUG_STEREO_MERGE") == "1"

var debugDualStereoEnabled = tmpGetenv("GOPUS_TMP_DEBUG_DUAL_STEREO") == "1"

var debugDualStereoAllocEnabled = tmpGetenv("GOPUS_TMP_DEBUG_DUAL_STEREO_ALLOC") == "1"

var debugEnergyDecodingEnabled = tmpGetenv("GOPUS_TMP_DEBUG_ENERGY_DECODING") == "1"

var mdctStageDumpEnabled = tmpGetenv("GOPUS_TMP_MDCT_STAGE_DUMP") == "1"

var kissFFTStageDumpEnabled = tmpGetenv("GOPUS_TMP_KISSFFT_STAGE_DUMP") == "1"
