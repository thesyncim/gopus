//go:build !amd64 || !goexperiment.simd || nosimd

package silk

// rewhitenLTP is silk_LPC_analysis_filter on the quantized history:
// sLTP[startIdx+ix] for ix in [0, length) from xq[startIdx+offset+ix].
func rewhitenLTP(sLTP []int16, xq []int16, startIdx, offset int, aQ12 []int16, length, order int) {
	// Set first 'order' outputs to zero (per libopus silk_LPC_analysis_filter)
	for i := startIdx; i < startIdx+order && i < len(sLTP); i++ {
		sLTP[i] = 0
	}
	rewhitenLTPScalar(sLTP, xq, startIdx, offset, aQ12, order, length, order)
}
