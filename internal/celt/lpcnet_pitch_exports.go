package celt

// LPCNetPitchXCorrFloat32 runs the selected CELT pitch correlation kernel used
// by libopus dnn/lpcnet_enc.c:compute_frame_features. The caller supplies
// len(x) >= length, len(y) >= length+maxPitch-1, and len(dst) >= maxPitch.
func LPCNetPitchXCorrFloat32(dst, x, y []float32, length, maxPitch int) {
	pitchXCorrFloat32(x, y, dst, length, maxPitch)
}

// LPCNetInnerProdFloat32 runs the selected celt_inner_prod kernel at the same
// libopus compute_frame_features callsite.
func LPCNetInnerProdFloat32(x, y []float32, length int) float32 {
	return innerProdFloat32(x, y, length)
}
