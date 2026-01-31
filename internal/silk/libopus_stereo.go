package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

func silkStereoDecodePred(rd *rangecoding.Decoder, predQ13 []int32) {
	ix := [2][3]int{}
	n := rd.DecodeICDF(silk_stereo_pred_joint_iCDF, 8)
	ix[0][2] = n / 5
	ix[1][2] = n - 5*ix[0][2]
	for i := 0; i < 2; i++ {
		ix[i][0] = rd.DecodeICDF(silk_uniform3_iCDF, 8)
		ix[i][1] = rd.DecodeICDF(silk_uniform5_iCDF, 8)
	}

	for i := 0; i < 2; i++ {
		ix[i][0] += 3 * ix[i][2]
		lowQ13 := int32(silk_stereo_pred_quant_Q13[ix[i][0]])
		stepQ13 := silkSMULWB(int32(silk_stereo_pred_quant_Q13[ix[i][0]+1])-lowQ13, int32(silkFixConst(0.5/float64(stereoQuantSubSteps), 16)))
		predQ13[i] = silkSMLABB(lowQ13, stepQ13, int32(2*ix[i][1]+1))
	}
	predQ13[0] -= predQ13[1]
}

func silkStereoDecodeMidOnly(rd *rangecoding.Decoder) int {
	return rd.DecodeICDF(silk_stereo_only_code_mid_iCDF, 8)
}

func silkStereoMSToLR(state *stereoDecState, mid []int16, side []int16, predQ13 []int32, fsKHz int, frameLength int) {
	copy(mid, state.sMid[:])
	copy(side, state.sSide[:])
	copy(state.sMid[:], mid[frameLength:frameLength+2])
	copy(state.sSide[:], side[frameLength:frameLength+2])

	pred0 := state.predPrevQ13[0]
	pred1 := state.predPrevQ13[1]
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0 := silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-pred0, denomQ16), 16)
	delta1 := silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-pred1, denomQ16), 16)

	interpSamples := stereoInterpLenMs * fsKHz
	for n := 0; n < interpSamples; n++ {
		pred0 += delta0
		pred1 += delta1
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(side[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), pred1)
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	pred0 = predQ13[0]
	pred1 = predQ13[1]
	for n := interpSamples; n < frameLength; n++ {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(mid[n])+int32(mid[n+2]), int32(mid[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(side[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(mid[n+1]), 11), pred1)
		side[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}

	state.predPrevQ13[0] = predQ13[0]
	state.predPrevQ13[1] = predQ13[1]

	for n := 0; n < frameLength; n++ {
		sum := int32(mid[n+1]) + int32(side[n+1])
		diff := int32(mid[n+1]) - int32(side[n+1])
		mid[n+1] = silkSAT16(sum)
		side[n+1] = silkSAT16(diff)
	}
}
