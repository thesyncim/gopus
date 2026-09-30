//go:build !amd64.v3 || gopus_fixed_point || (goexperiment.simd && !nosimd && !purego)

package celt

// thetaRDODistortion computes the weighted normalized inner products for the
// selected non-scalar-v3 float path.
func thetaRDODistortion(w0, w1 float32, xSave, xBand, ySave, yBand []celtNorm) float32 {
	ipx, ipy := celtInnerProdPairLibopusOrder(xSave, xBand, ySave, yBand)
	return fma32(w0, ipx, noFMA32Mul(w1, ipy))
}
