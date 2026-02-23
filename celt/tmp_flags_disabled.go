//go:build !gopus_tmp_env

package celt

// Production/default build: compile out temporary debug/tuning branches.
const tmpSkipMDCTHistRoundEnabled = false
const tmpQuantInputLSBEnabled = false
const tmpDisableDCRejectEnabled = false
const tmpDisablePrefilterEnabled = false
const tmpForceMaxPitchRatio1Enabled = false
const tmpDumpXB19Enabled = false
const tmpSkipPrefOutRoundEnabled = false
const tmpDumpMDCT56Enabled = false
const tmpEnergyInputF32Enabled = false
const tmpDumpNorm56Enabled = false
const tmpRoundNormF32Enabled = false
const tmpDumpNormEnabled = false
const tmpTrimUseAnalysisEnabled = false
const tmpTrimDebugEnabled = false
const tmpPVQDump56Enabled = false
const tmpPVQDump75Enabled = false
const tmpPVQCallDebugEnabled = false
const tmpTHDebugEnabled = false
const tmpQDebugEnabled = false
const tmpSkipPrefInputRoundEnabled = false
const tmpPrefilterF64Enabled = false
const tmpPrefCombDumpEnabled = false
const tmpSkipPrefMemRoundEnabled = false
