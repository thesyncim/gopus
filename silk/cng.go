package silk

// silkCNGReset mirrors libopus silk_CNG_Reset().
func silkCNGReset(st *decoderState) {
	if st == nil {
		return
	}
	order := st.lpcOrder
	if order <= 0 || order > maxLPCOrder {
		order = maxLPCOrder
	}
	stepQ15 := silkDiv32_16(int32(32767), int32(order+1))
	accQ15 := int32(0)
	for i := 0; i < order; i++ {
		accQ15 += stepQ15
		st.cng.smthNLSFQ15[i] = accQ15
	}
	for i := order; i < maxLPCOrder; i++ {
		st.cng.smthNLSFQ15[i] = 0
		st.cng.synthStateQ14[i] = 0
	}
	for i := range st.cng.excBufQ14 {
		st.cng.excBufQ14[i] = 0
	}
	st.cng.smthGainQ16 = 0
	st.cng.randSeed = 3176576
}

func silkCNGExc(dstQ14 []int32, excBufQ14 []int32, length int, randSeed *int32) {
	if length <= 0 || randSeed == nil {
		return
	}
	excMask := cngBufMaskMax
	for excMask > length {
		excMask >>= 1
	}
	seed := *randSeed
	for i := 0; i < length && i < len(dstQ14); i++ {
		seed = silkRand(seed)
		idx := int((seed >> 24) & int32(excMask))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(excBufQ14) {
			idx = len(excBufQ14) - 1
		}
		dstQ14[i] = excBufQ14[idx]
	}
	*randSeed = seed
}

func silkSMULTT(a32, b32 int32) int32 {
	return (a32 >> 16) * (b32 >> 16)
}

func silkSubLShift32(a, b int32, shift int) int32 {
	return a - (b << shift)
}

func silkAddSat16(a, b int16) int16 {
	sum := int32(a) + int32(b)
	return silkSAT16(sum)
}

// applyCNG mirrors libopus silk_CNG() cadence for a single channel/frame.
func (d *Decoder) applyCNG(channel int, st *decoderState, ctrl *decoderControl, frame []int16) {
	if st == nil || len(frame) == 0 {
		return
	}
	length := len(frame)

	if st.fsKHz != st.cng.fsKHz {
		silkCNGReset(st)
		st.cng.fsKHz = st.fsKHz
	}

	// Update CNG history during no-voice-activity good frames.
	if st.lossCnt == 0 && int(st.indices.signalType) == typeNoVoiceActivity && ctrl != nil {
		order := st.lpcOrder
		if order <= 0 || order > maxLPCOrder {
			order = maxLPCOrder
		}
		for i := 0; i < order; i++ {
			delta := int32(st.prevNLSFQ15[i]) - st.cng.smthNLSFQ15[i]
			st.cng.smthNLSFQ15[i] += silkSMULWB(delta, cngNLSFSMthQ16)
		}

		maxGainQ16 := int32(0)
		subfr := 0
		for i := 0; i < st.nbSubfr; i++ {
			if ctrl.GainsQ16[i] > maxGainQ16 {
				maxGainQ16 = ctrl.GainsQ16[i]
				subfr = i
			}
		}

		if st.subfrLength > 0 && st.nbSubfr > 0 {
			moveLen := (st.nbSubfr - 1) * st.subfrLength
			if moveLen > 0 && st.subfrLength+moveLen <= len(st.cng.excBufQ14) {
				copy(st.cng.excBufQ14[st.subfrLength:st.subfrLength+moveLen], st.cng.excBufQ14[:moveLen])
			}
			srcStart := subfr * st.subfrLength
			srcEnd := srcStart + st.subfrLength
			if srcStart >= 0 && srcEnd <= len(st.excQ14) && st.subfrLength <= len(st.cng.excBufQ14) {
				copy(st.cng.excBufQ14[:st.subfrLength], st.excQ14[srcStart:srcEnd])
			}
		}

		for i := 0; i < st.nbSubfr; i++ {
			st.cng.smthGainQ16 += silkSMULWB(ctrl.GainsQ16[i]-st.cng.smthGainQ16, cngGainSmthQ16)
			if silkSMULWW(st.cng.smthGainQ16, cngGainSmthThresholdQ16) > ctrl.GainsQ16[i] {
				st.cng.smthGainQ16 = ctrl.GainsQ16[i]
			}
		}
	}

	if st.lossCnt != 0 {
		plcState := d.ensureSILKPLCState(channel)
		if plcState == nil {
			return
		}

		gainQ16 := silkSMULWW(int32(plcState.RandScaleQ14), plcState.PrevGainQ16[1])
		if gainQ16 >= (1<<21) || st.cng.smthGainQ16 > (1<<23) {
			gainQ16 = silkSMULTT(gainQ16, gainQ16)
			gainQ16 = silkSubLShift32(silkSMULTT(st.cng.smthGainQ16, st.cng.smthGainQ16), gainQ16, 5)
			gainQ16 = silkLSHIFT(silkSqrtApproxPLC(gainQ16), 16)
		} else {
			gainQ16 = silkSMULWW(gainQ16, gainQ16)
			gainQ16 = silkSubLShift32(silkSMULWW(st.cng.smthGainQ16, st.cng.smthGainQ16), gainQ16, 5)
			gainQ16 = silkLSHIFT(silkSqrtApproxPLC(gainQ16), 8)
		}
		gainQ10 := silkRSHIFT(gainQ16, 6)

		order := st.lpcOrder
		if order <= 0 || order > maxLPCOrder {
			order = maxLPCOrder
		}

		var cngSigQ14 [maxFrameLength + maxLPCOrder]int32
		sig := cngSigQ14[:maxLPCOrder+length]
		silkCNGExc(sig[maxLPCOrder:], st.cng.excBufQ14[:], length, &st.cng.randSeed)

		var aQ12 [maxLPCOrder]int16
		var nlsfQ15 [maxLPCOrder]int16
		for i := 0; i < order; i++ {
			nlsfQ15[i] = int16(st.cng.smthNLSFQ15[i])
		}
		if !silkNLSF2A(aQ12[:order], nlsfQ15[:order], order) {
			lpc := lsfToLPCDirect(nlsfQ15[:order])
			copy(aQ12[:order], lpc[:order])
		}

		copy(sig[:maxLPCOrder], st.cng.synthStateQ14[:])
		for i := 0; i < length; i++ {
			base := maxLPCOrder + i
			lpcPredQ10 := int32(order >> 1)
			for j := 0; j < order; j++ {
				lpcPredQ10 = silkSMLAWB(lpcPredQ10, sig[base-j-1], int32(aQ12[j]))
			}
			sig[base] = silkAddSat32(sig[base], lShiftSAT32By4(lpcPredQ10))
			cngSample := silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sig[base], gainQ10), 8))
			frame[i] = silkAddSat16(frame[i], cngSample)
		}
		copy(st.cng.synthStateQ14[:], sig[length:length+maxLPCOrder])
		return
	}

	for i := 0; i < st.lpcOrder && i < maxLPCOrder; i++ {
		st.cng.synthStateQ14[i] = 0
	}
}
