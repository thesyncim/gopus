//go:build !gopus_tmp_env

package celt

// Additional production/default toggles: compile out temporary tuning/debug branches.
const tmpPVQAbsQ15Enabled = false
const tmpPVQIdxBiasEnabled = false
const tmpPVQIdxBiasValue float32 = 0

const tmpEnergyPredMulNativeEnabled = false
const tmpCoarseDumpEnabled = false
const tmpFineDumpEnabled = false
const tmpFineQEpsEnabled = false
const tmpFineQEpsValue float64 = 0

const tmpCombFilterSeqAccumEnabled = false
const tmpCombFilterFMAOverlapEnabled = false

const tmpDisableHAARASMEnabled = false
const tmpExpRotF32Enabled = false
const tmpRenormF32RefEnabled = false
const tmpPVQDumpEnabled = false
const tmpPVQInputF32Enabled = false
const tmpIThetaF32Enabled = false
const tmpGainF32Enabled = false
const tmpQuantPartF32Enabled = false
const tmpUseQ30CosSplitEnabled = false
const tmpRoundBeforePVQEnabled = false
const tmpQDebugDecEnabled = false
const tmpQuantBandF32Enabled = false
const tmpLowbandOutF32Enabled = false
const tmpRoundBandStateF32Enabled = false
