//go:build !cgo_libopus

package silk

// LibopusLPCInterpDebugResult is the stub type for non-cgo builds.
// The real definition is in cgo_libopus.go (cgo_libopus build tag).
type LibopusLPCInterpDebugResult struct {
	NLSF         []int16
	InterpQ2     int
	ResNrg       float32
	ResNrgLast   float32
	ResNrgInterp [4]float32
}

// TmpLibopusFindLPCInterpDebug is a no-op stub for non-cgo builds.
// The real implementation is in tmp_cgo_export_diag.go (cgo_libopus build tag).
func TmpLibopusFindLPCInterpDebug(
	x []float32,
	nbSubfr, subfrLen, lpcOrder int,
	useInterp, firstFrame bool,
	prevNLSF []int16,
	minInvGain float32,
) LibopusLPCInterpDebugResult {
	return LibopusLPCInterpDebugResult{}
}
