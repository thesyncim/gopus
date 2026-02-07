package silk

// LibopusLPCInterpDebugResult holds debug outputs for LPC interpolation analysis.
type LibopusLPCInterpDebugResult struct {
	NLSF         []int16
	InterpQ2     int
	ResNrg       float32
	ResNrgLast   float32
	ResNrgInterp [4]float32
}

// TmpLibopusFindLPCInterpDebug is currently a no-op placeholder.
func TmpLibopusFindLPCInterpDebug(
	x []float32,
	nbSubfr, subfrLen, lpcOrder int,
	useInterp, firstFrame bool,
	prevNLSF []int16,
	minInvGain float32,
) LibopusLPCInterpDebugResult {
	return LibopusLPCInterpDebugResult{}
}
