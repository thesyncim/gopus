package silk

import "math"

const (
	ltpQuantScaleQ17     = 131072.0     // 2^17
	ltpQuantSum1Q15      = int32(32801) // round(1.001 * 2^15)
	ltpGainSafetyQ7      = int32(51)    // round(0.4 * 2^7)
	maxSumLogGainQ7Const = int32(5333)  // round((250/6) * 2^7)
	maxInt32             = int32(0x7fffffff)
)

func float64ToInt32(x float64) int32 {
	return float64ToInt32Round(x)
}

func corrMatrixFLP(x []float64, subfrLen, order int, out []float64) {
	if subfrLen <= 0 || order <= 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}

	// ptr1 points to &x[order-1] (start of column 0 of X)
	ptr1Idx := order - 1

	// Calculate X[:,0]'*X[:,0]
	energy := energyF64(x[ptr1Idx:ptr1Idx+subfrLen], subfrLen)
	out[0] = energy

	for j := 1; j < order; j++ {
		// Calculate X[:,j]'*X[:,j]
		// energy += x[ptr1Idx-j]*x[ptr1Idx-j] - x[ptr1Idx+subfrLen-j]*x[ptr1Idx+subfrLen-j]
		term1 := x[ptr1Idx-j]
		term2 := x[ptr1Idx+subfrLen-j]
		energy += term1*term1 - term2*term2
		out[j*order+j] = energy
	}

	ptr2Idx := order - 2 // First sample of column 1 of X
	for lag := 1; lag < order; lag++ {
		// Calculate X[:,0]'*X[:,lag]
		inner := 0.0
		for n := 0; n < subfrLen; n++ {
			inner += x[ptr1Idx+n] * x[ptr2Idx+n]
		}
		out[lag*order+0] = inner
		out[0*order+lag] = inner

		// Calculate X[:,j]'*X[:,j+lag]
		for j := 1; j < order-lag; j++ {
			term1 := x[ptr1Idx-j]
			term2 := x[ptr2Idx-j]
			term3 := x[ptr1Idx+subfrLen-j]
			term4 := x[ptr2Idx+subfrLen-j]
			inner += term1*term2 - term3*term4
			out[(lag+j)*order+j] = inner
			out[j*order+(lag+j)] = inner
		}
		ptr2Idx--
	}
}

func corrVectorFLP(x, y []float64, subfrLen, order int, out []float64) {
	if subfrLen <= 0 || order <= 0 {
		for i := range out {
			out[i] = 0
		}
		return
	}

	ptr1Idx := order - 1
	for lag := 0; lag < order; lag++ {
		sum := 0.0
		for n := 0; n < subfrLen; n++ {
			sum += x[ptr1Idx+n] * y[n]
		}
		out[lag] = sum
		ptr1Idx--
	}
}

func findLTPFLP(XX, xX []float64, residual []float64, resStart int, lag []int, subfrLen, nbSubfr int) {
	xxIdx := 0
	xXIdx := 0
	rPtrStart := resStart
	for k := 0; k < nbSubfr; k++ {
		if k >= len(lag) {
			break
		}
		lagPtrStart := rPtrStart - (lag[k] + ltpOrderConst/2)
		if lagPtrStart < 0 || rPtrStart < 0 || rPtrStart+subfrLen+ltpOrderConst > len(residual) || lagPtrStart+subfrLen+ltpOrderConst > len(residual) {
			for i := 0; i < ltpOrderConst*ltpOrderConst; i++ {
				XX[xxIdx+i] = 0
			}
			for i := 0; i < ltpOrderConst; i++ {
				xX[xXIdx+i] = 0
			}
			rPtrStart += subfrLen
			xxIdx += ltpOrderConst * ltpOrderConst
			xXIdx += ltpOrderConst
			continue
		}

		lagPtr := residual[lagPtrStart:]
		rPtr := residual[rPtrStart:]
		corrMatrixFLP(lagPtr, subfrLen, ltpOrderConst, XX[xxIdx:xxIdx+ltpOrderConst*ltpOrderConst])
		corrVectorFLP(lagPtr, rPtr, subfrLen, ltpOrderConst, xX[xXIdx:xXIdx+ltpOrderConst])

		xx := energyF64(rPtr, subfrLen+ltpOrderConst)
		diag0 := XX[xxIdx]
		diagLast := XX[xxIdx+ltpOrderConst*ltpOrderConst-1]
		temp := 1.0 / math.Max(xx, ltpCorrInvMax*0.5*(diag0+diagLast)+1.0)
		for i := 0; i < ltpOrderConst*ltpOrderConst; i++ {
			XX[xxIdx+i] *= temp
		}
		for i := 0; i < ltpOrderConst; i++ {
			xX[xXIdx+i] *= temp
		}

		rPtrStart += subfrLen
		xxIdx += ltpOrderConst * ltpOrderConst
		xXIdx += ltpOrderConst
	}
}

func silkVQWMatEC(ind *int8, resNrgQ15 *int32, rateDistQ8 *int32, gainQ7 *int32, XX_Q17 []int32, xX_Q17 []int32, cb_Q7 []int8, cb_gain_Q7 []uint8, cl_Q5 []uint8, subfrLen int, maxGainQ7 int32, L int) {
	var neg_xX_Q24 [ltpOrderConst]int32
	for i := 0; i < ltpOrderConst; i++ {
		neg_xX_Q24[i] = -silkLSHIFT(xX_Q17[i], 7)
	}

	*rateDistQ8 = maxInt32
	*resNrgQ15 = maxInt32
	*ind = 0

	for k := 0; k < L; k++ {
		cbRow := cb_Q7[k*ltpOrderConst:]
		gainTmpQ7 := int32(cb_gain_Q7[k])
		penalty := silkLSHIFT(silkMax32(gainTmpQ7-maxGainQ7, 0), 11)

		sum1_Q15 := ltpQuantSum1Q15

		sum2_Q24 := silkMLA(neg_xX_Q24[0], XX_Q17[1], int32(cbRow[1]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[2], int32(cbRow[2]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[3], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[4], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[0], int32(cbRow[0]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[0]))

		sum2_Q24 = silkMLA(neg_xX_Q24[1], XX_Q17[7], int32(cbRow[2]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[8], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[9], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[6], int32(cbRow[1]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[1]))

		sum2_Q24 = silkMLA(neg_xX_Q24[2], XX_Q17[13], int32(cbRow[3]))
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[14], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[12], int32(cbRow[2]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[2]))

		sum2_Q24 = silkMLA(neg_xX_Q24[3], XX_Q17[19], int32(cbRow[4]))
		sum2_Q24 = silkLSHIFT(sum2_Q24, 1)
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[18], int32(cbRow[3]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[3]))

		sum2_Q24 = silkLSHIFT(neg_xX_Q24[4], 1)
		sum2_Q24 = silkMLA(sum2_Q24, XX_Q17[24], int32(cbRow[4]))
		sum1_Q15 = silkSMLAWB(sum1_Q15, sum2_Q24, int32(cbRow[4]))

		if sum1_Q15 >= 0 {
			bitsResQ8 := silkSMULBB(int32(subfrLen), silkLin2Log(sum1_Q15+penalty)-(15<<7))
			bitsTotQ8 := silkADD_LSHIFT32(bitsResQ8, int32(cl_Q5[k]), 2)
			if bitsTotQ8 <= *rateDistQ8 {
				*rateDistQ8 = bitsTotQ8
				*resNrgQ15 = sum1_Q15 + penalty
				*ind = int8(k)
				*gainQ7 = gainTmpQ7
			}
		}
	}
}

func silkQuantLTPGains(B_Q14 []int16, cbkIndex []int8, periodicityIndex *int8, sumLogGainQ7 *int32, predGainQ7 *int32, XX_Q17 []int32, xX_Q17 []int32, subfrLen, nbSubfr int) {
	minRateDistQ7 := maxInt32
	bestSumLogGainQ7 := int32(0)
	var tempIdx [maxNbSubfr]int8
	gainSafetyQ7 := ltpGainSafetyQ7
	maxSumLogGainQ7 := maxSumLogGainQ7Const
	var resNrgQ15 int32

	for k := 0; k < 3; k++ {
		clPtr := silk_LTP_gain_BITS_Q5_ptrs[k]
		cbkPtr := silk_LTP_vq_ptrs_Q7[k]
		cbkGainPtr := silk_LTP_vq_gain_ptrs_Q7[k]
		cbkSize := int(silk_LTP_vq_sizes[k])

		XXPtr := XX_Q17
		xXPtr := xX_Q17
		resNrgQ15 = int32(0)
		rateDistQ7 := int32(0)
		sumLogGainTmpQ7 := *sumLogGainQ7

		for j := 0; j < nbSubfr; j++ {
			maxGainQ7 := silkLog2Lin((maxSumLogGainQ7-sumLogGainTmpQ7)+(7<<7)) - gainSafetyQ7

			var resNrgSubQ15, rateDistSubQ7, gainQ7 int32
			var idx int8
			silkVQWMatEC(&idx, &resNrgSubQ15, &rateDistSubQ7, &gainQ7, XXPtr, xXPtr, cbkPtr, cbkGainPtr, clPtr, subfrLen, maxGainQ7, cbkSize)
			tempIdx[j] = idx
			resNrgQ15 = silkAddPosSat32(resNrgQ15, resNrgSubQ15)
			rateDistQ7 = silkAddPosSat32(rateDistQ7, rateDistSubQ7)
			sumLogGainTmpQ7 = silkMax32(0, sumLogGainTmpQ7+silkLin2Log(gainSafetyQ7+gainQ7)-(7<<7))

			XXPtr = XXPtr[ltpOrderConst*ltpOrderConst:]
			xXPtr = xXPtr[ltpOrderConst:]
		}

		if rateDistQ7 <= minRateDistQ7 {
			minRateDistQ7 = rateDistQ7
			*periodicityIndex = int8(k)
			copy(cbkIndex, tempIdx[:nbSubfr])
			bestSumLogGainQ7 = sumLogGainTmpQ7
		}
	}

	cbkPtr := silk_LTP_vq_ptrs_Q7[*periodicityIndex]
	for j := 0; j < nbSubfr; j++ {
		base := int(cbkIndex[j]) * ltpOrderConst
		for k := 0; k < ltpOrderConst; k++ {
			B_Q14[j*ltpOrderConst+k] = int16(cbkPtr[base+k]) << 7
		}
	}

	if nbSubfr == 2 {
		*predGainQ7 = int32(silkSMULBB(-3, silkLin2Log(silkRSHIFT(resNrgQ15, 1))-(15<<7)))
	} else {
		*predGainQ7 = int32(silkSMULBB(-3, silkLin2Log(silkRSHIFT(resNrgQ15, 2))-(15<<7)))
	}
	*sumLogGainQ7 = bestSumLogGainQ7
}

func (e *Encoder) analyzeLTPQuantized(residual []float64, resStart int, pitchLags []int, numSubframes, subframeSamples int) (LTPCoeffsArray, [maxNbSubfr]int8, int, int32) {
	var ltpCoeffs LTPCoeffsArray
	var cbkIndex [maxNbSubfr]int8
	perIndex := 0
	predGainQ7 := int32(0)

	if numSubframes <= 0 || len(pitchLags) == 0 || len(residual) == 0 {
		return ltpCoeffs, cbkIndex, perIndex, predGainQ7
	}

	if resStart < 0 {
		resStart = 0
	}
	if resStart >= len(residual) {
		resStart = 0
	}

	var XX [maxNbSubfr * ltpOrderConst * ltpOrderConst]float64
	var xX [maxNbSubfr * ltpOrderConst]float64
	findLTPFLP(XX[:], xX[:], residual, resStart, pitchLags, subframeSamples, numSubframes)

	if e.trace != nil && e.trace.LTP != nil {
		tr := e.trace.LTP
		xxLen := numSubframes * ltpOrderConst * ltpOrderConst
		xXLen := numSubframes * ltpOrderConst
		tr.XXLen = xxLen
		tr.XxLen = xXLen
		tr.XXHash = hashFloat64AsFloat32(XX[:xxLen])
		tr.XxHash = hashFloat64AsFloat32(xX[:xXLen])
		if tr.CaptureXX {
			tr.XX = tr.XX[:0]
			for i := 0; i < xxLen; i++ {
				tr.XX = append(tr.XX, float32(XX[i]))
			}
			tr.Xx = tr.Xx[:0]
			for i := 0; i < xXLen; i++ {
				tr.Xx = append(tr.Xx, float32(xX[i]))
			}
		}
	}

	var XXQ17 [maxNbSubfr * ltpOrderConst * ltpOrderConst]int32
	var xXQ17 [maxNbSubfr * ltpOrderConst]int32
	xxLen := numSubframes * ltpOrderConst * ltpOrderConst
	xXLen := numSubframes * ltpOrderConst
	for i := 0; i < xxLen; i++ {
		XXQ17[i] = float64ToInt32(XX[i] * ltpQuantScaleQ17)
	}
	for i := 0; i < xXLen; i++ {
		xXQ17[i] = float64ToInt32(xX[i] * ltpQuantScaleQ17)
	}

	var bQ14 [maxNbSubfr * ltpOrderConst]int16
	sumLogGainQ7 := e.sumLogGainQ7
	per := int8(0)
	silkQuantLTPGains(bQ14[:], cbkIndex[:], &per, &sumLogGainQ7, &predGainQ7, XXQ17[:], xXQ17[:], subframeSamples, numSubframes)
	e.sumLogGainQ7 = sumLogGainQ7
	perIndex = int(per)

	for sf := 0; sf < numSubframes; sf++ {
		for tap := 0; tap < ltpOrderConst; tap++ {
			ltpCoeffs[sf][tap] = int8(bQ14[sf*ltpOrderConst+tap] >> 7)
		}
	}

	return ltpCoeffs, cbkIndex, perIndex, predGainQ7
}
