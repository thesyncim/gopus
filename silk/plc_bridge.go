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
	state := d.ensureSILKPLCState(0)
	if state != nil {
		return int((state.PitchLQ8 + 128) >> 8)
	}
	return d.state[0].lagPrev
}

func (d *Decoder) GetLastGain() int32 {
	return d.state[0].prevGainQ16
}

func (d *Decoder) GetLTPScale() int32 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return 0
	}
	return state.PrevLTPScaleQ14
}

func (d *Decoder) GetExcitationHistory() []int32 {
	st := &d.state[0]
	if st.frameLength > 0 && st.frameLength <= len(st.excQ14) {
		return st.excQ14[:st.frameLength]
	}
	return st.excQ14[:]
}

func (d *Decoder) GetLPCCoefficientsQ12() []int16 {
	state := d.ensureSILKPLCState(0)
	if state == nil {
		return nil
	}
	order := state.LPCOrder
	if order < 0 {
		order = 0
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	return state.PrevLPCQ12[:order]
}

func (d *Decoder) GetSampleRateKHz() int {
	if d.state[0].fsKHz <= 0 {
		return 16
	}
	return d.state[0].fsKHz
}

func (d *Decoder) GetSubframeLength() int {
	if d.state[0].subfrLength <= 0 {
		return 80
	}
	return d.state[0].subfrLength
}

func (d *Decoder) GetNumSubframes() int {
	if d.state[0].nbSubfr <= 0 {
		return maxNbSubfr
	}
	return d.state[0].nbSubfr
}

func (d *Decoder) GetLTPMemoryLength() int {
	if d.state[0].ltpMemLength <= 0 {
		return maxFrameLength
	}
	return d.state[0].ltpMemLength
}
