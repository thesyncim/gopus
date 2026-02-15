package silk

// shortTermPrediction16 computes 16-tap LPC prediction.
// Returns 8 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..15.
func shortTermPrediction16(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	_ = sLPCQ14[idx]
	_ = sLPCQ14[idx-15]
	_ = aQ12[15]
	out := int32(8) // order>>1 = 16>>1
	out = silk_SMLAWB(out, sLPCQ14[idx-0], int32(aQ12[0]))
	out = silk_SMLAWB(out, sLPCQ14[idx-1], int32(aQ12[1]))
	out = silk_SMLAWB(out, sLPCQ14[idx-2], int32(aQ12[2]))
	out = silk_SMLAWB(out, sLPCQ14[idx-3], int32(aQ12[3]))
	out = silk_SMLAWB(out, sLPCQ14[idx-4], int32(aQ12[4]))
	out = silk_SMLAWB(out, sLPCQ14[idx-5], int32(aQ12[5]))
	out = silk_SMLAWB(out, sLPCQ14[idx-6], int32(aQ12[6]))
	out = silk_SMLAWB(out, sLPCQ14[idx-7], int32(aQ12[7]))
	out = silk_SMLAWB(out, sLPCQ14[idx-8], int32(aQ12[8]))
	out = silk_SMLAWB(out, sLPCQ14[idx-9], int32(aQ12[9]))
	out = silk_SMLAWB(out, sLPCQ14[idx-10], int32(aQ12[10]))
	out = silk_SMLAWB(out, sLPCQ14[idx-11], int32(aQ12[11]))
	out = silk_SMLAWB(out, sLPCQ14[idx-12], int32(aQ12[12]))
	out = silk_SMLAWB(out, sLPCQ14[idx-13], int32(aQ12[13]))
	out = silk_SMLAWB(out, sLPCQ14[idx-14], int32(aQ12[14]))
	out = silk_SMLAWB(out, sLPCQ14[idx-15], int32(aQ12[15]))
	return out
}

// shortTermPrediction10 computes 10-tap LPC prediction.
// Returns 5 + sum((sLPCQ14[idx-k] * int16(aQ12[k])) >> 16) for k=0..9.
func shortTermPrediction10(sLPCQ14 []int32, idx int, aQ12 []int16) int32 {
	_ = sLPCQ14[idx]
	_ = sLPCQ14[idx-9]
	_ = aQ12[9]
	out := int32(5) // order>>1 = 10>>1
	out = silk_SMLAWB(out, sLPCQ14[idx-0], int32(aQ12[0]))
	out = silk_SMLAWB(out, sLPCQ14[idx-1], int32(aQ12[1]))
	out = silk_SMLAWB(out, sLPCQ14[idx-2], int32(aQ12[2]))
	out = silk_SMLAWB(out, sLPCQ14[idx-3], int32(aQ12[3]))
	out = silk_SMLAWB(out, sLPCQ14[idx-4], int32(aQ12[4]))
	out = silk_SMLAWB(out, sLPCQ14[idx-5], int32(aQ12[5]))
	out = silk_SMLAWB(out, sLPCQ14[idx-6], int32(aQ12[6]))
	out = silk_SMLAWB(out, sLPCQ14[idx-7], int32(aQ12[7]))
	out = silk_SMLAWB(out, sLPCQ14[idx-8], int32(aQ12[8]))
	out = silk_SMLAWB(out, sLPCQ14[idx-9], int32(aQ12[9]))
	return out
}
