//go:build !arm64 && !amd64

package silk

// warpedARFeedback24 computes 24-tap warped AR noise shaping feedback (pure Go).
func warpedARFeedback24(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32 {
	sAR := sAR2Q14
	w := int64(warpQ16)
	_ = sAR[23] // BCE
	_ = arShpQ13[23]

	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(12) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	// j=2
	tmp2 = sAR[1] + int32((int64(sAR[2]-tmp1)*w)>>16)
	sAR[1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[1])) >> 16)
	tmp1 = sAR[2] + int32((int64(sAR[3]-tmp2)*w)>>16)
	sAR[2] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[2])) >> 16)
	// j=4
	tmp2 = sAR[3] + int32((int64(sAR[4]-tmp1)*w)>>16)
	sAR[3] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[3])) >> 16)
	tmp1 = sAR[4] + int32((int64(sAR[5]-tmp2)*w)>>16)
	sAR[4] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[4])) >> 16)
	// j=6
	tmp2 = sAR[5] + int32((int64(sAR[6]-tmp1)*w)>>16)
	sAR[5] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[5])) >> 16)
	tmp1 = sAR[6] + int32((int64(sAR[7]-tmp2)*w)>>16)
	sAR[6] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[6])) >> 16)
	// j=8
	tmp2 = sAR[7] + int32((int64(sAR[8]-tmp1)*w)>>16)
	sAR[7] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[7])) >> 16)
	tmp1 = sAR[8] + int32((int64(sAR[9]-tmp2)*w)>>16)
	sAR[8] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[8])) >> 16)
	// j=10
	tmp2 = sAR[9] + int32((int64(sAR[10]-tmp1)*w)>>16)
	sAR[9] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[9])) >> 16)
	tmp1 = sAR[10] + int32((int64(sAR[11]-tmp2)*w)>>16)
	sAR[10] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[10])) >> 16)
	// j=12
	tmp2 = sAR[11] + int32((int64(sAR[12]-tmp1)*w)>>16)
	sAR[11] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[11])) >> 16)
	tmp1 = sAR[12] + int32((int64(sAR[13]-tmp2)*w)>>16)
	sAR[12] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[12])) >> 16)
	// j=14
	tmp2 = sAR[13] + int32((int64(sAR[14]-tmp1)*w)>>16)
	sAR[13] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[13])) >> 16)
	tmp1 = sAR[14] + int32((int64(sAR[15]-tmp2)*w)>>16)
	sAR[14] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[14])) >> 16)
	// j=16
	tmp2 = sAR[15] + int32((int64(sAR[16]-tmp1)*w)>>16)
	sAR[15] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[15])) >> 16)
	tmp1 = sAR[16] + int32((int64(sAR[17]-tmp2)*w)>>16)
	sAR[16] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[16])) >> 16)
	// j=18
	tmp2 = sAR[17] + int32((int64(sAR[18]-tmp1)*w)>>16)
	sAR[17] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[17])) >> 16)
	tmp1 = sAR[18] + int32((int64(sAR[19]-tmp2)*w)>>16)
	sAR[18] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[18])) >> 16)
	// j=20
	tmp2 = sAR[19] + int32((int64(sAR[20]-tmp1)*w)>>16)
	sAR[19] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[19])) >> 16)
	tmp1 = sAR[20] + int32((int64(sAR[21]-tmp2)*w)>>16)
	sAR[20] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[20])) >> 16)
	// j=22
	tmp2 = sAR[21] + int32((int64(sAR[22]-tmp1)*w)>>16)
	sAR[21] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[21])) >> 16)
	tmp1 = sAR[22] + int32((int64(sAR[23]-tmp2)*w)>>16)
	sAR[22] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[22])) >> 16)
	// final tap
	sAR[23] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[23])) >> 16)

	return acc
}

// warpedARFeedback16 computes 16-tap warped AR noise shaping feedback (pure Go).
func warpedARFeedback16(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32 {
	sAR := sAR2Q14
	w := int64(warpQ16)
	_ = sAR[15] // BCE
	_ = arShpQ13[15]

	tmp2 := diffQ14 + int32((int64(sAR[0])*w)>>16)
	tmp1 := sAR[0] + int32((int64(sAR[1]-tmp2)*w)>>16)
	sAR[0] = tmp2
	acc := int32(8) + int32((int64(tmp2)*int64(arShpQ13[0]))>>16)

	// j=2
	tmp2 = sAR[1] + int32((int64(sAR[2]-tmp1)*w)>>16)
	sAR[1] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[1])) >> 16)
	tmp1 = sAR[2] + int32((int64(sAR[3]-tmp2)*w)>>16)
	sAR[2] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[2])) >> 16)
	// j=4
	tmp2 = sAR[3] + int32((int64(sAR[4]-tmp1)*w)>>16)
	sAR[3] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[3])) >> 16)
	tmp1 = sAR[4] + int32((int64(sAR[5]-tmp2)*w)>>16)
	sAR[4] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[4])) >> 16)
	// j=6
	tmp2 = sAR[5] + int32((int64(sAR[6]-tmp1)*w)>>16)
	sAR[5] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[5])) >> 16)
	tmp1 = sAR[6] + int32((int64(sAR[7]-tmp2)*w)>>16)
	sAR[6] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[6])) >> 16)
	// j=8
	tmp2 = sAR[7] + int32((int64(sAR[8]-tmp1)*w)>>16)
	sAR[7] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[7])) >> 16)
	tmp1 = sAR[8] + int32((int64(sAR[9]-tmp2)*w)>>16)
	sAR[8] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[8])) >> 16)
	// j=10
	tmp2 = sAR[9] + int32((int64(sAR[10]-tmp1)*w)>>16)
	sAR[9] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[9])) >> 16)
	tmp1 = sAR[10] + int32((int64(sAR[11]-tmp2)*w)>>16)
	sAR[10] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[10])) >> 16)
	// j=12
	tmp2 = sAR[11] + int32((int64(sAR[12]-tmp1)*w)>>16)
	sAR[11] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[11])) >> 16)
	tmp1 = sAR[12] + int32((int64(sAR[13]-tmp2)*w)>>16)
	sAR[12] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[12])) >> 16)
	// j=14
	tmp2 = sAR[13] + int32((int64(sAR[14]-tmp1)*w)>>16)
	sAR[13] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[13])) >> 16)
	tmp1 = sAR[14] + int32((int64(sAR[15]-tmp2)*w)>>16)
	sAR[14] = tmp2
	acc += int32((int64(tmp2) * int64(arShpQ13[14])) >> 16)
	// final tap
	sAR[15] = tmp1
	acc += int32((int64(tmp1) * int64(arShpQ13[15])) >> 16)

	return acc
}
