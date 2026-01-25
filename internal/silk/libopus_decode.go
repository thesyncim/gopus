package silk

import "github.com/thesyncim/gopus/internal/rangecoding"

func silkDecoderSetFs(st *decoderState, fsKHz int) {
	st.subfrLength = subFrameLengthMs * fsKHz
	frameLength := st.nbSubfr * st.subfrLength

	if st.fsKHz != fsKHz || frameLength != st.frameLength {
		if fsKHz == 8 {
			if st.nbSubfr == maxNbSubfr {
				st.pitchContourICDF = silk_pitch_contour_NB_iCDF
			} else {
				st.pitchContourICDF = silk_pitch_contour_10_ms_NB_iCDF
			}
		} else {
			if st.nbSubfr == maxNbSubfr {
				st.pitchContourICDF = silk_pitch_contour_iCDF
			} else {
				st.pitchContourICDF = silk_pitch_contour_10_ms_iCDF
			}
		}

		if st.fsKHz != fsKHz {
			st.ltpMemLength = ltpMemLengthMs * fsKHz
			if fsKHz == 8 || fsKHz == 12 {
				st.lpcOrder = minLPCOrder
				st.nlsfCB = &silk_NLSF_CB_NB_MB
			} else {
				st.lpcOrder = maxLPCOrder
				st.nlsfCB = &silk_NLSF_CB_WB
			}
			switch fsKHz {
			case 16:
				st.pitchLagLowBitsICDF = silk_uniform8_iCDF
			case 12:
				st.pitchLagLowBitsICDF = silk_uniform6_iCDF
			case 8:
				st.pitchLagLowBitsICDF = silk_uniform4_iCDF
			}
			st.firstFrameAfterReset = true
			st.lagPrev = 100
			st.lastGainIndex = 10
			st.prevSignalType = typeNoVoiceActivity
			for i := range st.outBuf {
				st.outBuf[i] = 0
			}
			for i := range st.sLPCQ14Buf {
				st.sLPCQ14Buf[i] = 0
			}
		}

		st.fsKHz = fsKHz
		st.frameLength = frameLength
	}
}

func silkDecodeIndices(st *decoderState, rd *rangecoding.Decoder, vadFlag bool, condCoding int) {
	var ix int
	if vadFlag {
		ix = rd.DecodeICDF(silk_type_offset_VAD_iCDF, 8) + 2
	} else {
		ix = rd.DecodeICDF(silk_type_offset_no_VAD_iCDF, 8)
	}
	st.indices.signalType = int8(ix >> 1)
	st.indices.quantOffsetType = int8(ix & 1)

	if condCoding == codeConditionally {
		st.indices.GainsIndices[0] = int8(rd.DecodeICDF(silk_delta_gain_iCDF, 8))
	} else {
		msb := rd.DecodeICDF(silk_gain_iCDF[int(st.indices.signalType)], 8)
		lsb := rd.DecodeICDF(silk_uniform8_iCDF, 8)
		st.indices.GainsIndices[0] = int8((msb << 3) + lsb)
	}

	for i := 1; i < st.nbSubfr; i++ {
		st.indices.GainsIndices[i] = int8(rd.DecodeICDF(silk_delta_gain_iCDF, 8))
	}

	cb := st.nlsfCB
	stypeBand := int(st.indices.signalType) >> 1
	cb1Offset := stypeBand * cb.nVectors
	st.indices.NLSFIndices[0] = int8(rd.DecodeICDF(cb.cb1ICDF[cb1Offset:], 8))

	ecIx := make([]int16, maxLPCOrder)
	predQ8 := make([]uint8, maxLPCOrder)
	silkNLSFUnpack(ecIx, predQ8, cb, int(st.indices.NLSFIndices[0]))

	for i := 0; i < cb.order; i++ {
		idx := rd.DecodeICDF(cb.ecICDF[int(ecIx[i]):], 8)
		if idx == 0 {
			idx -= rd.DecodeICDF(silk_NLSF_EXT_iCDF, 8)
		} else if idx == 2*nlsfQuantMaxAmplitude {
			idx += rd.DecodeICDF(silk_NLSF_EXT_iCDF, 8)
		}
		st.indices.NLSFIndices[i+1] = int8(idx - nlsfQuantMaxAmplitude)
	}

	if st.nbSubfr == maxNbSubfr {
		st.indices.NLSFInterpCoefQ2 = int8(rd.DecodeICDF(silk_NLSF_interpolation_factor_iCDF, 8))
	} else {
		st.indices.NLSFInterpCoefQ2 = 4
	}

	if st.indices.signalType == typeVoiced {
		decodeAbsolute := true
		if condCoding == codeConditionally && st.ecPrevSignalType == typeVoiced {
			deltaLag := rd.DecodeICDF(silk_pitch_delta_iCDF, 8)
			if deltaLag > 0 {
				deltaLag -= 9
				st.indices.lagIndex = int16(st.ecPrevLagIndex + deltaLag)
				decodeAbsolute = false
			}
		}
		if decodeAbsolute {
			st.indices.lagIndex = int16(rd.DecodeICDF(silk_pitch_lag_iCDF, 8) * (st.fsKHz >> 1))
			st.indices.lagIndex += int16(rd.DecodeICDF(st.pitchLagLowBitsICDF, 8))
		}
		st.ecPrevLagIndex = int(st.indices.lagIndex)
		st.indices.contourIndex = int8(rd.DecodeICDF(st.pitchContourICDF, 8))

		st.indices.PERIndex = int8(rd.DecodeICDF(silk_LTP_per_index_iCDF, 8))
		for k := 0; k < st.nbSubfr; k++ {
			st.indices.LTPIndex[k] = int8(rd.DecodeICDF(silk_LTP_gain_iCDF_ptrs[int(st.indices.PERIndex)], 8))
		}
		if condCoding == codeIndependently {
			st.indices.LTPScaleIndex = int8(rd.DecodeICDF(silk_LTPscale_iCDF, 8))
		} else {
			st.indices.LTPScaleIndex = 0
		}
	}
	st.ecPrevSignalType = int(st.indices.signalType)

	st.indices.Seed = int8(rd.DecodeICDF(silk_uniform4_iCDF, 8))
}

func silkShellDecoder(pulses []int16, rd *rangecoding.Decoder, pulses4 int) {
	pulses3 := make([]int16, 2)
	pulses2 := make([]int16, 4)
	pulses1 := make([]int16, 8)

	decodeSplit := func(c1, c2 *int16, p int, table []uint8) {
		if p > 0 {
			*c1 = int16(rd.DecodeICDF(table[silk_shell_code_table_offsets[p]:], 8))
			*c2 = int16(p - int(*c1))
		} else {
			*c1 = 0
			*c2 = 0
		}
	}

	decodeSplit(&pulses3[0], &pulses3[1], pulses4, silk_shell_code_table3)
	decodeSplit(&pulses2[0], &pulses2[1], int(pulses3[0]), silk_shell_code_table2)

	decodeSplit(&pulses1[0], &pulses1[1], int(pulses2[0]), silk_shell_code_table1)
	decodeSplit(&pulses[0], &pulses[1], int(pulses1[0]), silk_shell_code_table0)
	decodeSplit(&pulses[2], &pulses[3], int(pulses1[1]), silk_shell_code_table0)

	decodeSplit(&pulses1[2], &pulses1[3], int(pulses2[1]), silk_shell_code_table1)
	decodeSplit(&pulses[4], &pulses[5], int(pulses1[2]), silk_shell_code_table0)
	decodeSplit(&pulses[6], &pulses[7], int(pulses1[3]), silk_shell_code_table0)

	decodeSplit(&pulses2[2], &pulses2[3], int(pulses3[1]), silk_shell_code_table2)

	decodeSplit(&pulses1[4], &pulses1[5], int(pulses2[2]), silk_shell_code_table1)
	decodeSplit(&pulses[8], &pulses[9], int(pulses1[4]), silk_shell_code_table0)
	decodeSplit(&pulses[10], &pulses[11], int(pulses1[5]), silk_shell_code_table0)

	decodeSplit(&pulses1[6], &pulses1[7], int(pulses2[3]), silk_shell_code_table1)
	decodeSplit(&pulses[12], &pulses[13], int(pulses1[6]), silk_shell_code_table0)
	decodeSplit(&pulses[14], &pulses[15], int(pulses1[7]), silk_shell_code_table0)
}

func silkDecodeSigns(rd *rangecoding.Decoder, pulses []int16, length int, signalType int, quantOffsetType int, sumPulses []int) {
	icdf := []uint8{0, 0}
	qPtr := 0
	idx := 7 * (quantOffsetType + (signalType << 1))
	icdfPtr := silk_sign_iCDF[idx:]
	blocks := (length + shellCodecFrameLength/2) >> log2ShellCodecFrameLength
	for i := 0; i < blocks; i++ {
		p := sumPulses[i]
		if p > 0 {
			icdf[0] = icdfPtr[silkMinInt(p&0x1F, 6)]
			for j := 0; j < shellCodecFrameLength; j++ {
				if pulses[qPtr+j] > 0 {
					sign := rd.DecodeICDF(icdf, 8)
					if sign == 0 {
						pulses[qPtr+j] = -pulses[qPtr+j]
					}
				}
			}
		}
		qPtr += shellCodecFrameLength
	}
}

func silkDecodePulses(rd *rangecoding.Decoder, pulses []int16, signalType int, quantOffsetType int, frameLength int) {
	rateLevel := rd.DecodeICDF(silk_rate_levels_iCDF[signalType>>1], 8)
	iter := frameLength >> log2ShellCodecFrameLength
	if iter*shellCodecFrameLength < frameLength {
		iter++
	}

	sumPulses := make([]int, iter)
	nLshifts := make([]int, iter)

	cdfPtr := silk_pulses_per_block_iCDF[rateLevel]
	for i := 0; i < iter; i++ {
		nLshifts[i] = 0
		sumPulses[i] = rd.DecodeICDF(cdfPtr, 8)
		for sumPulses[i] == silkMaxPulses+1 {
			nLshifts[i]++
			table := silk_pulses_per_block_iCDF[nRateLevels-1]
			if nLshifts[i] == 10 {
				table = table[1:]
			}
			sumPulses[i] = rd.DecodeICDF(table, 8)
		}
	}

	for i := 0; i < iter; i++ {
		off := i * shellCodecFrameLength
		if sumPulses[i] > 0 {
			silkShellDecoder(pulses[off:off+shellCodecFrameLength], rd, sumPulses[i])
		} else {
			for j := 0; j < shellCodecFrameLength; j++ {
				pulses[off+j] = 0
			}
		}
	}

	for i := 0; i < iter; i++ {
		if nLshifts[i] > 0 {
			nLS := nLshifts[i]
			off := i * shellCodecFrameLength
			for k := 0; k < shellCodecFrameLength; k++ {
				absQ := int32(pulses[off+k])
				for j := 0; j < nLS; j++ {
					absQ <<= 1
					absQ += int32(rd.DecodeICDF(silk_lsb_iCDF, 8))
				}
				pulses[off+k] = int16(absQ)
			}
			sumPulses[i] |= nLS << 5
		}
	}

	silkDecodeSigns(rd, pulses, frameLength, signalType, quantOffsetType, sumPulses)
}

func silkDecodePitch(lagIndex int16, contourIndex int8, pitchL []int, fsKHz int, nbSubfr int) {
	var lagCB [][]int8
	var cbkSize int
	if fsKHz == 8 {
		if nbSubfr == peMaxNbSubfr {
			lagCB = silk_CB_lags_stage2
			cbkSize = peNbCbksStage2Ext
		} else {
			lagCB = silk_CB_lags_stage2_10_ms
			cbkSize = peNbCbksStage2_10ms
		}
	} else {
		if nbSubfr == peMaxNbSubfr {
			lagCB = silk_CB_lags_stage3
			cbkSize = peNbCbksStage3Max
		} else {
			lagCB = silk_CB_lags_stage3_10_ms
			cbkSize = peNbCbksStage3_10ms
		}
	}
	minLag := peMinLagMs * fsKHz
	maxLag := peMaxLagMs * fsKHz
	lag := minLag + int(lagIndex)
	for k := 0; k < nbSubfr; k++ {
		idx := int(contourIndex)
		if idx < 0 {
			idx = 0
		}
		if idx >= cbkSize {
			idx = cbkSize - 1
		}
		pitchL[k] = lag + int(lagCB[k][idx])
		pitchL[k] = silkLimitInt(pitchL[k], minLag, maxLag)
	}
}

func silkBwExpander(ar []int16, chirpQ16 int32) {
	if len(ar) == 0 {
		return
	}
	chirpMinusOneQ16 := chirpQ16 - 65536
	for i := 0; i < len(ar)-1; i++ {
		ar[i] = int16(silkRSHIFT_ROUND(silkMUL(chirpQ16, int32(ar[i])), 16))
		chirpQ16 += silkRSHIFT_ROUND(silkMUL(chirpQ16, chirpMinusOneQ16), 16)
	}
	ar[len(ar)-1] = int16(silkRSHIFT_ROUND(silkMUL(chirpQ16, int32(ar[len(ar)-1])), 16))
}

func silkDecodeParameters(st *decoderState, ctrl *decoderControl, condCoding int) {
	silkGainsDequant(&ctrl.GainsQ16, &st.indices.GainsIndices, &st.lastGainIndex, condCoding == codeConditionally, st.nbSubfr)

	var nlsfQ15 [maxLPCOrder]int16
	silkNLSFDecode(nlsfQ15[:], st.indices.NLSFIndices[:], st.nlsfCB)

	if !silkNLSF2A(ctrl.PredCoefQ12[1][:st.lpcOrder], nlsfQ15[:st.lpcOrder], st.lpcOrder) {
		lpc1 := lsfToLPCDirect(nlsfQ15[:st.lpcOrder])
		copy(ctrl.PredCoefQ12[1][:], lpc1)
	}

	if st.firstFrameAfterReset {
		st.indices.NLSFInterpCoefQ2 = 4
	}
	if st.indices.NLSFInterpCoefQ2 < 4 {
		var nlsf0 [maxLPCOrder]int16
		for i := 0; i < st.lpcOrder; i++ {
			diff := int32(nlsfQ15[i]) - int32(st.prevNLSFQ15[i])
			nlsf0[i] = int16(int32(st.prevNLSFQ15[i]) + (int32(st.indices.NLSFInterpCoefQ2) * diff >> 2))
		}
		if !silkNLSF2A(ctrl.PredCoefQ12[0][:st.lpcOrder], nlsf0[:st.lpcOrder], st.lpcOrder) {
			lpc0 := lsfToLPCDirect(nlsf0[:st.lpcOrder])
			copy(ctrl.PredCoefQ12[0][:], lpc0)
		}
	} else {
		copy(ctrl.PredCoefQ12[0][:], ctrl.PredCoefQ12[1][:])
	}

	copy(st.prevNLSFQ15[:], nlsfQ15[:])

	if st.lossCnt != 0 {
		silkBwExpander(ctrl.PredCoefQ12[0][:st.lpcOrder], int32(bweAfterLossQ16))
		silkBwExpander(ctrl.PredCoefQ12[1][:st.lpcOrder], int32(bweAfterLossQ16))
	}

	if st.indices.signalType == typeVoiced {
		silkDecodePitch(st.indices.lagIndex, st.indices.contourIndex, ctrl.pitchL[:], st.fsKHz, st.nbSubfr)
		cbk := silk_LTP_vq_ptrs_Q7[st.indices.PERIndex]
		for k := 0; k < st.nbSubfr; k++ {
			idx := int(st.indices.LTPIndex[k]) * ltpOrder
			for i := 0; i < ltpOrder; i++ {
				ctrl.LTPCoefQ14[k*ltpOrder+i] = int16(int32(cbk[idx+i]) << 7)
			}
		}
		ctrl.LTPScaleQ14 = int32(silk_LTPScales_table_Q14[st.indices.LTPScaleIndex])
	} else {
		for i := range ctrl.pitchL {
			ctrl.pitchL[i] = 0
		}
		for i := range ctrl.LTPCoefQ14 {
			ctrl.LTPCoefQ14[i] = 0
		}
		st.indices.PERIndex = 0
		ctrl.LTPScaleQ14 = 0
	}
}

func silkLPCAnalysisFilter(out []int16, in []int16, B []int16, length int, order int) {
	for i := 0; i < order; i++ {
		out[i] = 0
	}
	for ix := order; ix < length; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		outQ12 = silkLSHIFT(int32(in[ix]), 12) - outQ12
		out32 := silkRSHIFT_ROUND(outQ12, 12)
		out[ix] = silkSAT16(out32)
	}
}

func silkDecodeCore(st *decoderState, ctrl *decoderControl, out []int16, pulses []int16) {
	offsetQ10 := silk_Quantization_Offsets_Q10[int(st.indices.signalType)>>1][int(st.indices.quantOffsetType)]
	interpFlag := st.indices.NLSFInterpCoefQ2 < 4

	randSeed := int32(st.indices.Seed)
	for i := 0; i < st.frameLength; i++ {
		randSeed = silkRand(randSeed)
		exc := int32(pulses[i]) << 14
		if exc > 0 {
			exc -= quantLevelAdjustQ10 << 4
		} else if exc < 0 {
			exc += quantLevelAdjustQ10 << 4
		}
		exc += int32(offsetQ10) << 4
		if randSeed < 0 {
			exc = -exc
		}
		st.excQ14[i] = exc
		randSeed += int32(pulses[i])
	}

	sLPC := make([]int32, st.subfrLength+maxLPCOrder)
	copy(sLPC, st.sLPCQ14Buf[:])
	pexc := st.excQ14[:]
	pxq := out

	sLTP := make([]int16, st.ltpMemLength)
	sLTP_Q15 := make([]int32, st.ltpMemLength+st.frameLength)
	sLTPBufIdx := st.ltpMemLength

	for k := 0; k < st.nbSubfr; k++ {
		A_Q12 := ctrl.PredCoefQ12[k>>1][:]
		B_Q14 := ctrl.LTPCoefQ14[k*ltpOrder : (k+1)*ltpOrder]
		signalType := int(st.indices.signalType)

		gainQ10 := ctrl.GainsQ16[k] >> 6
		invGainQ31 := silkInverse32VarQ(ctrl.GainsQ16[k], 47)
		gainAdjQ16 := int32(1 << 16)

		if ctrl.GainsQ16[k] != st.prevGainQ16 {
			gainAdjQ16 = silkDiv32VarQ(st.prevGainQ16, ctrl.GainsQ16[k], 16)
			for i := 0; i < maxLPCOrder; i++ {
				sLPC[i] = silkSMULWW(gainAdjQ16, sLPC[i])
			}
		}
		st.prevGainQ16 = ctrl.GainsQ16[k]

		if st.lossCnt != 0 && st.prevSignalType == typeVoiced && signalType != typeVoiced && k < maxNbSubfr/2 {
			for i := 0; i < ltpOrder; i++ {
				B_Q14[i] = 0
			}
			B_Q14[ltpOrder/2] = int16(silkFixConst(0.25, 14))
			signalType = typeVoiced
			ctrl.pitchL[k] = st.lagPrev
		}

		if signalType == typeVoiced {
			lag := ctrl.pitchL[k]
			if k == 0 || (k == 2 && interpFlag) {
				startIdx := st.ltpMemLength - lag - st.lpcOrder - ltpOrder/2
				if startIdx < 0 {
					startIdx = 0
				}
				if k == 2 {
					copy(st.outBuf[st.ltpMemLength:], out[:2*st.subfrLength])
				}
				silkLPCAnalysisFilter(sLTP[startIdx:], st.outBuf[startIdx+k*st.subfrLength:], A_Q12, st.ltpMemLength-startIdx, st.lpcOrder)
				if k == 0 {
					invGainQ31 = silkLSHIFT(silkSMULWB(invGainQ31, ctrl.LTPScaleQ14), 2)
				}
				for i := 0; i < lag+ltpOrder/2; i++ {
					sLTP_Q15[sLTPBufIdx-i-1] = silkSMULWB(invGainQ31, int32(sLTP[st.ltpMemLength-i-1]))
				}
			} else if gainAdjQ16 != int32(1<<16) {
				for i := 0; i < lag+ltpOrder/2; i++ {
					sLTP_Q15[sLTPBufIdx-i-1] = silkSMULWW(gainAdjQ16, sLTP_Q15[sLTPBufIdx-i-1])
				}
			}
		}

		var presQ14 []int32
		if signalType == typeVoiced {
			lag := ctrl.pitchL[k]
			predLagPtr := sLTPBufIdx - lag + ltpOrder/2
			presQ14 = make([]int32, st.subfrLength)
			for i := 0; i < st.subfrLength; i++ {
				ltpPredQ13 := int32(2)
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr+0], int32(B_Q14[0]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-1], int32(B_Q14[1]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-2], int32(B_Q14[2]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-3], int32(B_Q14[3]))
				ltpPredQ13 = silkSMLAWB(ltpPredQ13, sLTP_Q15[predLagPtr-4], int32(B_Q14[4]))
				predLagPtr++
				presQ14[i] = silkADD_LSHIFT32(pexc[i], ltpPredQ13, 1)
				sLTP_Q15[sLTPBufIdx] = silkLSHIFT(presQ14[i], 1)
				sLTPBufIdx++
			}
		} else {
			presQ14 = pexc[:st.subfrLength]
		}

		for i := 0; i < st.subfrLength; i++ {
			lpcPredQ10 := int32(st.lpcOrder >> 1)
			for j := 0; j < st.lpcOrder; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(A_Q12[j]))
			}
			sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
			pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))
		}

		copy(sLPC, sLPC[st.subfrLength:st.subfrLength+maxLPCOrder])
		pexc = pexc[st.subfrLength:]
		pxq = pxq[st.subfrLength:]
	}

	copy(st.sLPCQ14Buf[:], sLPC[:maxLPCOrder])
}

func silkRand(seed int32) int32 {
	return seed*196314165 + 907633515
}

func silkUpdateOutBuf(st *decoderState, frame []int16) {
	if st.ltpMemLength == 0 || st.frameLength == 0 {
		return
	}
	if st.ltpMemLength < st.frameLength {
		return
	}
	mvLen := st.ltpMemLength - st.frameLength
	buf := st.outBuf[:]
	if mvLen > 0 {
		copy(buf, buf[st.frameLength:st.frameLength+mvLen])
	}
	copy(buf[mvLen:mvLen+st.frameLength], frame)
}
