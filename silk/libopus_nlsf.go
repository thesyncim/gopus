package silk

func silkNLSFUnpack(ecIx []int16, predQ8 []uint8, cb *nlsfCB, cb1Index int) {
	ecSelPtr := cb.ecSel[cb1Index*cb.order/2:]
	for i := 0; i < cb.order; i += 2 {
		entry := ecSelPtr[0]
		ecSelPtr = ecSelPtr[1:]
		ecIx[i] = int16(silkSMULBB(int32(entry>>1&7), 2*nlsfQuantMaxAmplitude+1))
		predQ8[i] = cb.predQ8[i+int(entry&1)*(cb.order-1)]
		ecIx[i+1] = int16(silkSMULBB(int32(entry>>5&7), 2*nlsfQuantMaxAmplitude+1))
		predQ8[i+1] = cb.predQ8[i+int((entry>>4)&1)*(cb.order-1)+1]
	}
}

func silkNLSFResidualDequant(xQ10 []int16, indices []int8, predQ8 []uint8, quantStepSizeQ16 int, order int) {
	var outQ10 int32
	for i := order - 1; i >= 0; i-- {
		predQ10 := silkRSHIFT(silkSMULBB(outQ10, int32(predQ8[i])), 8)
		outQ10 = int32(indices[i]) << 10
		if outQ10 > 0 {
			outQ10 -= nlsfQuantLevelAdjQ10
		} else if outQ10 < 0 {
			outQ10 += nlsfQuantLevelAdjQ10
		}
		outQ10 = silkSMLAWB(predQ10, outQ10, int32(quantStepSizeQ16))
		xQ10[i] = int16(outQ10)
	}
}

func silkNLSFDecode(nlsfQ15 []int16, indices []int8, cb *nlsfCB) {
	ecIx := make([]int16, maxLPCOrder)
	predQ8 := make([]uint8, maxLPCOrder)
	resQ10 := make([]int16, maxLPCOrder)

	silkNLSFUnpack(ecIx, predQ8, cb, int(indices[0]))
	silkNLSFResidualDequant(resQ10, indices[1:], predQ8, cb.quantStepSizeQ16, cb.order)

	baseIdx := int(indices[0]) * cb.order
	cbBase := cb.cb1NLSFQ8[baseIdx:]
	cbWght := cb.cb1WghtQ9[baseIdx:]
	for i := 0; i < cb.order; i++ {
		resQ10Val := int32(resQ10[i])
		wght := int32(cbWght[i])
		if wght == 0 {
			wght = 1
		}
		val := silkADD_LSHIFT32(int32(resQ10Val<<14)/wght, int32(cbBase[i]), 7)
		if val < 0 {
			val = 0
		}
		if val > 32767 {
			val = 32767
		}
		nlsfQ15[i] = int16(val)
	}

	silkNLSFStabilize(nlsfQ15[:cb.order], cb.deltaMinQ15, cb.order)
}

func silkNLSFStabilize(nlsfQ15 []int16, deltaMinQ15 []int16, order int) {
	const maxLoops = 20
	for loops := 0; loops < maxLoops; loops++ {
		minDiff := int32(nlsfQ15[0]) - int32(deltaMinQ15[0])
		idx := 0
		for i := 1; i <= order-1; i++ {
			diff := int32(nlsfQ15[i]) - (int32(nlsfQ15[i-1]) + int32(deltaMinQ15[i]))
			if diff < minDiff {
				minDiff = diff
				idx = i
			}
		}
		diff := int32(1<<15) - (int32(nlsfQ15[order-1]) + int32(deltaMinQ15[order]))
		if diff < minDiff {
			minDiff = diff
			idx = order
		}
		if minDiff >= 0 {
			return
		}
		if idx == 0 {
			nlsfQ15[0] = deltaMinQ15[0]
		} else if idx == order {
			nlsfQ15[order-1] = int16((1 << 15) - int32(deltaMinQ15[order]))
		} else {
			minCenter := int32(0)
			for k := 0; k < idx; k++ {
				minCenter += int32(deltaMinQ15[k])
			}
			minCenter += int32(deltaMinQ15[idx]) >> 1

			maxCenter := int32(1 << 15)
			for k := order; k > idx; k-- {
				maxCenter -= int32(deltaMinQ15[k])
			}
			maxCenter -= int32(deltaMinQ15[idx]) >> 1

			sum := int32(nlsfQ15[idx-1]) + int32(nlsfQ15[idx])
			center := silkRSHIFT_ROUND(sum, 1)
			center = silkLimit32(center, minCenter, maxCenter)
			nlsfQ15[idx-1] = int16(center - (int32(deltaMinQ15[idx]) >> 1))
			nlsfQ15[idx] = int16(int32(nlsfQ15[idx-1]) + int32(deltaMinQ15[idx]))
		}
	}

	silkInsertionSortInt16(nlsfQ15, order)
	if nlsfQ15[0] < deltaMinQ15[0] {
		nlsfQ15[0] = deltaMinQ15[0]
	}
	for i := 1; i < order; i++ {
		minVal := int16(int32(nlsfQ15[i-1]) + int32(deltaMinQ15[i]))
		if nlsfQ15[i] < minVal {
			nlsfQ15[i] = minVal
		}
	}
	lastMax := int16((1 << 15) - int32(deltaMinQ15[order]))
	if nlsfQ15[order-1] > lastMax {
		nlsfQ15[order-1] = lastMax
	}
	for i := order - 2; i >= 0; i-- {
		maxVal := int16(int32(nlsfQ15[i+1]) - int32(deltaMinQ15[i+1]))
		if nlsfQ15[i] > maxVal {
			nlsfQ15[i] = maxVal
		}
	}
}

func silkInsertionSortInt16(a []int16, n int) {
	for i := 1; i < n; i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
