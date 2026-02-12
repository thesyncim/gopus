//go:build !gopus_tmp_env

package celt

func debugStereoMergeEnabled() bool { return false }

func debugDualStereoEnabled() bool { return false }

func debugDualStereoAllocEnabled() bool { return false }

func debugEnergyDecodingEnabled() bool { return false }
