//go:build gopus_tmp_env

package celt

func debugStereoMergeEnabled() bool { return tmpGetenv("GOPUS_TMP_DEBUG_STEREO_MERGE") == "1" }

func debugDualStereoEnabled() bool { return tmpGetenv("GOPUS_TMP_DEBUG_DUAL_STEREO") == "1" }

func debugDualStereoAllocEnabled() bool { return tmpGetenv("GOPUS_TMP_DEBUG_DUAL_STEREO_ALLOC") == "1" }

func debugEnergyDecodingEnabled() bool { return tmpGetenv("GOPUS_TMP_DEBUG_ENERGY_DECODING") == "1" }
