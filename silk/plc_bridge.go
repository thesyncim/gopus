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

func (d *Decoder) GetSLPCQ14HistoryQ14() []int32 {
	st := &d.state[0]
	order := st.lpcOrder
	if order <= 0 {
		return nil
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	start := maxLPCOrder - order
	if start < 0 {
		start = 0
	}
	return st.sLPCQ14Buf[start:maxLPCOrder]
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
	st := &d.state[0]
	mem := st.ltpMemLength
	if mem <= 0 {
		return nil
	}
	if mem > len(st.outBuf) {
		mem = len(st.outBuf)
	}
	return st.outBuf[:mem]
}

type silkPLCChannelView struct {
	d  *Decoder
	ch int
}

func (d *Decoder) plcDecoderView(channel int) *silkPLCChannelView {
	if channel < 0 || channel >= len(d.state) {
		return nil
	}
	return &silkPLCChannelView{d: d, ch: channel}
}

func (v *silkPLCChannelView) state() *decoderState {
	if v == nil || v.d == nil || v.ch < 0 || v.ch >= len(v.d.state) {
		return nil
	}
	return &v.d.state[v.ch]
}

func (v *silkPLCChannelView) PrevLPCValues() []float32 {
	return v.d.prevLPCValues
}

func (v *silkPLCChannelView) LPCOrder() int {
	st := v.state()
	if st == nil {
		return 0
	}
	if st.lpcOrder > 0 {
		return st.lpcOrder
	}
	return 16
}

func (v *silkPLCChannelView) IsPreviousFrameVoiced() bool {
	st := v.state()
	if st == nil {
		return false
	}
	return int(st.indices.signalType) == typeVoiced
}

func (v *silkPLCChannelView) OutputHistory() []float32 {
	return v.d.outputHistory
}

func (v *silkPLCChannelView) HistoryIndex() int {
	return v.d.historyIndex
}

func (v *silkPLCChannelView) GetLastSignalType() int {
	st := v.state()
	if st == nil {
		return 0
	}
	return int(st.indices.signalType)
}

func (v *silkPLCChannelView) GetLTPCoefficients() [ltpOrder]int16 {
	state := v.d.ensureSILKPLCState(v.ch)
	if state == nil {
		return [ltpOrder]int16{}
	}
	return state.LTPCoefQ14
}

func (v *silkPLCChannelView) GetPitchLag() int {
	state := v.d.ensureSILKPLCState(v.ch)
	if state != nil {
		return int((state.PitchLQ8 + 128) >> 8)
	}
	st := v.state()
	if st == nil {
		return 0
	}
	return st.lagPrev
}

func (v *silkPLCChannelView) GetLastGain() int32 {
	st := v.state()
	if st == nil {
		return 1 << 16
	}
	return st.prevGainQ16
}

func (v *silkPLCChannelView) GetLTPScale() int32 {
	state := v.d.ensureSILKPLCState(v.ch)
	if state == nil {
		return 0
	}
	return state.PrevLTPScaleQ14
}

func (v *silkPLCChannelView) GetExcitationHistory() []int32 {
	st := v.state()
	if st == nil {
		return nil
	}
	if st.frameLength > 0 && st.frameLength <= len(st.excQ14) {
		return st.excQ14[:st.frameLength]
	}
	return st.excQ14[:]
}

func (v *silkPLCChannelView) GetLPCCoefficientsQ12() []int16 {
	state := v.d.ensureSILKPLCState(v.ch)
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

func (v *silkPLCChannelView) GetSampleRateKHz() int {
	st := v.state()
	if st == nil || st.fsKHz <= 0 {
		return 16
	}
	return st.fsKHz
}

func (v *silkPLCChannelView) GetSubframeLength() int {
	st := v.state()
	if st == nil || st.subfrLength <= 0 {
		return 80
	}
	return st.subfrLength
}

func (v *silkPLCChannelView) GetNumSubframes() int {
	st := v.state()
	if st == nil || st.nbSubfr <= 0 {
		return maxNbSubfr
	}
	return st.nbSubfr
}

func (v *silkPLCChannelView) GetLTPMemoryLength() int {
	st := v.state()
	if st == nil || st.ltpMemLength <= 0 {
		return maxFrameLength
	}
	return st.ltpMemLength
}

func (v *silkPLCChannelView) GetSLPCQ14HistoryQ14() []int32 {
	st := v.state()
	if st == nil {
		return nil
	}
	order := st.lpcOrder
	if order <= 0 {
		return nil
	}
	if order > maxLPCOrder {
		order = maxLPCOrder
	}
	start := maxLPCOrder - order
	if start < 0 {
		start = 0
	}
	return st.sLPCQ14Buf[start:maxLPCOrder]
}

func (v *silkPLCChannelView) SetSLPCQ14HistoryQ14(history []int32) {
	setSLPCQ14HistoryQ14(v.state(), history)
}

func (v *silkPLCChannelView) GetOutBufHistoryQ0() []int16 {
	st := v.state()
	if st == nil {
		return nil
	}
	mem := st.ltpMemLength
	if mem <= 0 {
		return nil
	}
	if mem > len(st.outBuf) {
		mem = len(st.outBuf)
	}
	return st.outBuf[:mem]
}
