//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package silk

type DebugLowbandSnapshot struct {
	LagPrev        int
	LastGainIndex  int
	LossCount      int
	PrevSignalType int
	SMid           [2]float32
	OutBuf         [maxFrameLength + 2*maxSubFrameLength]float32
	SLPCQ14        [maxLPCOrder]float32
	ExcQ14         [maxFrameLength]float32
	ResamplerIIR   [6]float32
	ResamplerFIR   [resamplerOrderFIR12]float32
	ResamplerDelay [96]float32
}

func (d *Decoder) DebugLowbandSnapshotMono() DebugLowbandSnapshot {
	var snap DebugLowbandSnapshot
	if d == nil {
		return snap
	}
	st := &d.state[0]
	snap.LagPrev = st.lagPrev
	snap.LastGainIndex = int(st.lastGainIndex)
	snap.LossCount = st.lossCnt
	snap.PrevSignalType = st.prevSignalType
	const scale = float32(1.0 / 32768.0)
	snap.SMid[0] = float32(d.stereo.sMid[0]) * scale
	snap.SMid[1] = float32(d.stereo.sMid[1]) * scale
	for i := range snap.OutBuf {
		snap.OutBuf[i] = float32(st.outBuf[i]) * scale
	}
	for i := range snap.SLPCQ14 {
		snap.SLPCQ14[i] = float32(st.sLPCQ14Buf[i])
	}
	for i := range snap.ExcQ14 {
		snap.ExcQ14[i] = float32(st.excQ14[i])
	}
	resampler := d.GetResampler(BandwidthWideband)
	if resampler == nil {
		return snap
	}
	for i := range snap.ResamplerIIR {
		snap.ResamplerIIR[i] = float32(resampler.sIIR[i])
	}
	for i := range snap.ResamplerFIR {
		snap.ResamplerFIR[i] = float32(resampler.sFIR[i]) * scale
	}
	for i := range snap.ResamplerDelay {
		if i < len(resampler.delayBuf) {
			snap.ResamplerDelay[i] = float32(resampler.delayBuf[i]) * scale
		}
	}
	return snap
}
