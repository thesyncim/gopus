//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var silkLPCAnalysisFilterUsesAVX2 = archsimd.X86.AVX2()

// silkLPCAnalysisFilterVec computes silk_LPC_analysis_filter outputs eight at
// a time from index max(order, 16) on and returns the first index it did not
// compute. Each output's prediction is a single 256-bit VPMADDWD dot product
// of the 16 preceding inputs with the (zero-padded) coefficients, reduced to
// int32 via VPHADDD. The int32 sums wrap as the silk_SMLABB chain does.
func silkLPCAnalysisFilterVec(out, in, B []int16, length, order int) int {
	ix := order
	if !silkLPCAnalysisFilterUsesAVX2 || order <= 0 || order > maxLPCOrder || length > len(in) || length > len(out) {
		return ix
	}
	start := max(order, maxLPCOrder)
	if start+8 > length {
		return ix
	}
	return silkLPCAnalysisFilterVecAVX2(out[:length], in[:length], B[:order], order, start)
}

//go:noinline
func silkLPCAnalysisFilterVecAVX2(out, in, B []int16, order, start int) int {
	length := len(in)
	ix := order
	for ; ix < start; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		out[ix] = silkSAT16(silkRSHIFT_ROUND(silkLSHIFT(int32(in[ix]), 12)-outQ12, 12))
	}
	// rev[j] multiplies in[ix-16+j].
	var rev [maxLPCOrder]int16
	for k := range order {
		rev[maxLPCOrder-1-k] = B[k]
	}
	c16 := archsimd.LoadInt16x16Array(&rev)
	half := archsimd.BroadcastInt32x4(1)
	pred := func(ix int) archsimd.Int32x4 {
		d := archsimd.LoadInt16x16Array((*[16]int16)(in[ix-maxLPCOrder:])).DotProductPairs(c16)
		return d.GetLo().Add(d.GetHi())
	}
	for ; ix+8 <= length; ix += 8 {
		p0 := pred(ix).ConcatAddPairs(pred(ix + 1)).ConcatAddPairs(pred(ix + 2).ConcatAddPairs(pred(ix + 3)))
		p1 := pred(ix + 4).ConcatAddPairs(pred(ix + 5)).ConcatAddPairs(pred(ix + 6).ConcatAddPairs(pred(ix + 7)))
		cur := archsimd.LoadInt16x8Array((*[8]int16)(in[ix:])).ExtendToInt32().ShiftAllLeft(12)
		// silk_RSHIFT_ROUND(outQ12, 12), then silk_SAT16 in the pack.
		r0 := cur.GetLo().Sub(p0).ShiftAllRight(11).Add(half).ShiftAllRight(1)
		r1 := cur.GetHi().Sub(p1).ShiftAllRight(11).Add(half).ShiftAllRight(1)
		r0.SaturateToInt16Concat(r1).StoreArray((*[8]int16)(out[ix:]))
	}
	archsimd.ClearAVXUpperBits()
	return ix
}
