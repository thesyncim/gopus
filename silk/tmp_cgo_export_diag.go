//go:build cgo_libopus

package silk

func TmpLibopusFindLPCInterpDebug(
	x []float32,
	nbSubfr, subfrLen, lpcOrder int,
	useInterp, firstFrame bool,
	prevNLSF []int16,
	minInvGain float32,
) libopusLPCInterpDebugResult {
	return libopusFindLPCInterpDebug(x, nbSubfr, subfrLen, lpcOrder, useInterp, firstFrame, prevNLSF, minInvGain)
}

