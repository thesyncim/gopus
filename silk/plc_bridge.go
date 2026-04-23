package silk

import "github.com/thesyncim/gopus/plc"

func (d *Decoder) ensureSILKPLCState(channel int) *plc.SILKPLCState {
	if channel < 0 || channel >= len(d.silkPLCState) {
		return nil
	}
	if d.silkPLCState[channel] == nil {
		d.silkPLCState[channel] = plc.NewSILKPLCState()
	}
	return d.silkPLCState[channel]
}

func (d *Decoder) updateSILKPLCStateFromCtrl(channel int, st *decoderState, ctrl *decoderControl) {
	if st == nil || ctrl == nil {
		return
	}
	state := d.ensureSILKPLCState(channel)
	if state == nil {
		return
	}

	nbSubfr := st.nbSubfr
	if nbSubfr <= 0 || nbSubfr > maxNbSubfr {
		nbSubfr = maxNbSubfr
	}
	lpcOrder := st.lpcOrder
	if lpcOrder <= 0 || lpcOrder > maxLPCOrder {
		lpcOrder = maxLPCOrder
	}

	fsKHz := st.fsKHz
	if fsKHz <= 0 {
		fsKHz = 16
	}
	subfrLength := st.subfrLength
	if subfrLength <= 0 {
		subfrLength = 80
	}

	var pitchL [maxNbSubfr]int
	copy(pitchL[:nbSubfr], ctrl.pitchL[:nbSubfr])

	var ltpCoefQ14 [ltpOrder * maxNbSubfr]int16
	copy(ltpCoefQ14[:nbSubfr*ltpOrder], ctrl.LTPCoefQ14[:nbSubfr*ltpOrder])

	var gainsQ16 [maxNbSubfr]int32
	copy(gainsQ16[:nbSubfr], ctrl.GainsQ16[:nbSubfr])

	var lpcQ12 [maxLPCOrder]int16
	copy(lpcQ12[:lpcOrder], ctrl.PredCoefQ12[1][:lpcOrder])

	state.UpdateFromGoodFrame(
		int(st.indices.signalType),
		pitchL[:nbSubfr],
		ltpCoefQ14[:nbSubfr*ltpOrder],
		ctrl.LTPScaleQ14,
		gainsQ16[:nbSubfr],
		lpcQ12[:lpcOrder],
		fsKHz,
		nbSubfr,
		subfrLength,
	)
	state.LastFrameLost = false
}

func (d *Decoder) GetLTPCoefficients() [ltpOrder]int16 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return [ltpOrder]int16{}
	}
	return state.LTPCoefQ14
}

func (d *Decoder) GetPitchLag() int {
	return pitchLagFromState(d.ensureSILKPLCState(0), &d.state[0])
}

func (d *Decoder) GetLastGain() int32 {
	return lastGainQ16FromState(&d.state[0])
}

func (d *Decoder) GetLTPScale() int32 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return 0
	}
	return state.PrevLTPScaleQ14
}

func (d *Decoder) GetExcitationHistory() []int32 {
	return excitationHistoryFromState(&d.state[0])
}

func (d *Decoder) GetLPCCoefficientsQ12() []int16 {
	return plcLPCCoefficientsQ12(d.ensureSILKPLCState(0))
}

func (d *Decoder) GetSampleRateKHz() int {
	return sampleRateKHzFromState(&d.state[0])
}

func (d *Decoder) GetSubframeLength() int {
	return subframeLengthFromState(&d.state[0])
}

func (d *Decoder) GetNumSubframes() int {
	return numSubframesFromState(&d.state[0])
}

func (d *Decoder) GetLTPMemoryLength() int {
	return ltpMemoryLengthFromState(&d.state[0])
}

func (d *Decoder) GetSLPCQ14HistoryQ14() []int32 {
	return slpcQ14HistoryFromState(&d.state[0])
}

func setSLPCQ14HistoryQ14(st *decoderState, history []int32) {
	if st == nil || len(history) == 0 {
		return
	}
	order := st.lpcOrder
	if order <= 0 {
		return
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	if len(history) < order {
		order = len(history)
	}
	start := maxLPCOrder - order
	if start < 0 {
		start = 0
	}
	copy(st.sLPCQ14Buf[start:maxLPCOrder], history[len(history)-order:])
}

func (d *Decoder) SetSLPCQ14HistoryQ14(history []int32) {
	setSLPCQ14HistoryQ14(&d.state[0], history)
}

func (d *Decoder) GetOutBufHistoryQ0() []int16 {
	return outBufHistoryFromState(&d.state[0])
}

func (d *Decoder) FillMonoOutBufTailFloat(dst []float32, samples int) int {
	if d == nil || samples <= 0 || len(dst) < samples {
		return 0
	}
	history := outBufHistoryFromState(&d.state[0])
	if len(history) < samples {
		return 0
	}
	history = history[len(history)-samples:]
	const scale = float32(1.0 / 32768.0)
	for i := 0; i < samples; i++ {
		dst[i] = float32(history[i]) * scale
	}
	return samples
}
