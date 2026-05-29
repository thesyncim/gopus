//go:build gopus_silk_trace

package silk

const encodeFrameTraceEnabled = true

var encodeFrameTraceHook func(*Encoder, encodeFrameTrace)

func recordEncodeFrameTrace(e *Encoder, trace encodeFrameTrace) {
	if encodeFrameTraceHook != nil {
		encodeFrameTraceHook(e, trace)
	}
}

func withEncodeFrameTraceHook(hook func(*Encoder, encodeFrameTrace), fn func()) {
	prev := encodeFrameTraceHook
	encodeFrameTraceHook = hook
	defer func() {
		encodeFrameTraceHook = prev
	}()
	fn()
}

// SILKCtrlSnapshot is an exported, test-only view of the per-SILK-frame
// silk_encoder_control_FLP state captured at iter==0 (after process_gains,
// before the rate-control loop). It mirrors the C oracle dump in
// tools/csrc/silk_encode_frame_FLP_dump.c so a root-package comparison test
// can bisect the first diverging shaping/NSQ quantity vs libopus.
type SILKCtrlSnapshot struct {
	SignalType   int
	QuantOffset  int
	NbSubfr      int
	LambdaQ10    int32
	GainsQ16     [4]int32
	TiltQ14      [4]int32
	HarmShapeQ14 [4]int32
	LFShpQ14     [4]int32
	ARShpQ13     [4 * maxShapeLpcOrder]int16
	PitchL       [4]int32
}

// WithSILKCtrlSnapshotHook installs a per-frame snapshot callback for the
// duration of fn. The callback fires once per SILK frame at the iter==0
// AfterPulses trace point.
func WithSILKCtrlSnapshotHook(cb func(SILKCtrlSnapshot), fn func()) {
	withEncodeFrameTraceHook(func(_ *Encoder, tr encodeFrameTrace) {
		if tr.iter != 0 || tr.stage != encodeFrameTraceAfterPulses {
			return
		}
		var s SILKCtrlSnapshot
		s.SignalType = tr.ctrlSignalType
		s.QuantOffset = tr.ctrlQuantOffset
		s.NbSubfr = tr.ctrlNbSubfr
		s.LambdaQ10 = tr.ctrlLambdaQ10
		s.GainsQ16 = tr.ctrlGainsQ16
		s.TiltQ14 = tr.ctrlTiltQ14
		s.HarmShapeQ14 = tr.ctrlHarmShapeQ14
		s.LFShpQ14 = tr.ctrlLFShpQ14
		s.ARShpQ13 = tr.ctrlARShpQ13
		s.PitchL = tr.ctrlPitchL
		cb(s)
	}, fn)
}
