//go:build gopus_extra_controls

package silk

const nativePostfilterEnabled = true

type NativePostfilterHook func(channel int, samples []int16, ctrl LatestDecoderControl) bool

type nativePostfilterExtras struct {
	hook NativePostfilterHook
}

func (d *Decoder) SetNativePostfilterHook(hook NativePostfilterHook) {
	d.nativePostfilter.hook = hook
}

func latestDecoderControlFromFrame(st *decoderState, ctrl *decoderControl) LatestDecoderControl {
	if st == nil || ctrl == nil {
		return LatestDecoderControl{}
	}
	return LatestDecoderControl{
		PredCoefQ12: ctrl.PredCoefQ12,
		LTPCoefQ14:  ctrl.LTPCoefQ14,
		GainsQ16:    ctrl.GainsQ16,
		PitchL:      ctrl.pitchL,
		SignalType:  int(st.indices.signalType),
		LPCOrder:    int(st.lpcOrder),
		NbSubfr:     int(st.nbSubfr),
		FsKHz:       int(st.fsKHz),
		NumBits:     int(ctrl.NumBits),
	}
}

func (d *Decoder) fireNativePostfilterHook(channel int, st *decoderState, ctrl *decoderControl, frameOut []int16) bool {
	if d == nil || d.nativePostfilter.hook == nil {
		return false
	}
	return d.nativePostfilter.hook(channel, frameOut, latestDecoderControlFromFrame(st, ctrl))
}
