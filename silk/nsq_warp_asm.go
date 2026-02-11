//go:build arm64 || amd64

package silk

// warpedARFeedback24 computes the 24-tap warped AR noise shaping feedback.
// It updates sAR2Q14 in-place and returns the raw nARQ14 accumulator
// (before the final <<1, tilt, and <<2 shifts — caller applies those).
//
// Algorithm (matching libopus NSQ_del_dec.c warped AR loop, order=24):
//
//	tmp2 = diffQ14 + (sAR[0]*warpQ16)>>16
//	tmp1 = sAR[0]  + ((sAR[1]-tmp2)*warpQ16)>>16
//	sAR[0] = tmp2
//	acc  = 12 + (tmp2*arShpQ13[0])>>16
//	for j = 2,4,...,22:
//	    tmp2 = sAR[j-1] + ((sAR[j]-tmp1)*warpQ16)>>16
//	    sAR[j-1] = tmp1;  acc += (tmp1*arShpQ13[j-1])>>16
//	    tmp1 = sAR[j]   + ((sAR[j+1]-tmp2)*warpQ16)>>16
//	    sAR[j] = tmp2;    acc += (tmp2*arShpQ13[j])>>16
//	sAR[23] = tmp1;       acc += (tmp1*arShpQ13[23])>>16
//	return acc
//
//go:noescape
func warpedARFeedback24(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32

// warpedARFeedback16 computes the 16-tap warped AR noise shaping feedback.
// Same algorithm as warpedARFeedback24 but for order=16.
// Rounding bias is 8 (order>>1).
//
//go:noescape
func warpedARFeedback16(sAR2Q14 *[maxShapeLpcOrder]int32, diffQ14 int32, arShpQ13 []int16, warpQ16 int32) int32
