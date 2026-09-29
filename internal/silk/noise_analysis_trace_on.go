//go:build gopus_silk_trace

package silk

const maxShapeLpcWindowLength = 15 * 16 // SHAPE_LPC_WIN_MAX: 15*MAX_FS_KHZ, with MAX_FS_KHZ=16.
const silkNoiseAnalysisTraceEnabled = true

// SILKNoiseAnalysisTraceSnapshot captures one subframe's unwarped shaping LPC
// analysis. Its dimensions match silk/define.h: MAX_SHAPE_LPC_ORDER is 24 and
// SHAPE_LPC_WIN_MAX is 15*MAX_FS_KHZ (15*16 samples).
//
// The snapshot is test-only and valid only during the trace callback.
type SILKNoiseAnalysisTraceSnapshot struct {
	Subframe      int32
	NumSubframes  int32
	Order         int32
	WindowLength  int32
	WarpingQ16    int32
	Valid         bool
	Window        [maxShapeLpcWindowLength]float32
	AutoCorrRaw   [maxShapeLpcOrder + 1]float32
	AutoCorrWhite [maxShapeLpcOrder + 1]float32
	Reflection    [maxShapeLpcOrder]float32
	Energy        float32
	SqrtGain      float32
}

var silkNoiseAnalysisTraceWants func(*Encoder, int32) bool
var silkNoiseAnalysisTraceHook func(*Encoder, *SILKNoiseAnalysisTraceSnapshot)

var silkNoiseAnalysisTracePending struct {
	owner    *Encoder
	snapshot SILKNoiseAnalysisTraceSnapshot
	raw      bool
	adjusted bool
}

func wantsSILKNoiseAnalysisTrace(e *Encoder, subframe int32) bool {
	return silkNoiseAnalysisTraceHook != nil && silkNoiseAnalysisTraceWants != nil &&
		silkNoiseAnalysisTraceWants(e, subframe)
}

func beginSILKNoiseAnalysisTrace(
	e *Encoder,
	subframe, numSubframes, order, windowLength, warpingQ16 int32,
	window []float32,
) {
	pending := &silkNoiseAnalysisTracePending
	*pending = struct {
		owner    *Encoder
		snapshot SILKNoiseAnalysisTraceSnapshot
		raw      bool
		adjusted bool
	}{owner: e}
	s := &pending.snapshot
	s.Subframe = subframe
	s.NumSubframes = numSubframes
	s.Order = order
	s.WindowLength = windowLength
	s.WarpingQ16 = warpingQ16
	s.Valid = numSubframes > 0 && numSubframes <= maxNbSubfr &&
		windowLength > 0 && windowLength <= maxShapeLpcWindowLength &&
		int(windowLength) == len(window) && order > 0 && order <= maxShapeLpcOrder &&
		windowLength > order && warpingQ16 == 0
	if s.Valid {
		copy(s.Window[:windowLength], window)
	}
}

func captureSILKNoiseAutoCorrTrace(e *Encoder, autoCorr []float32, adjusted bool) {
	pending := &silkNoiseAnalysisTracePending
	if pending.owner != e {
		return
	}
	s := &pending.snapshot
	if !s.Valid || len(autoCorr) < int(s.Order)+1 {
		s.Valid = false
		return
	}
	if adjusted {
		copy(s.AutoCorrWhite[:s.Order+1], autoCorr)
		pending.adjusted = true
	} else {
		copy(s.AutoCorrRaw[:s.Order+1], autoCorr)
		pending.raw = true
	}
}

func finishSILKNoiseAnalysisTrace(e *Encoder, rc []float32, energy, sqrtGain float32) {
	pending := &silkNoiseAnalysisTracePending
	if pending.owner != e {
		return
	}
	s := &pending.snapshot
	if len(rc) < int(s.Order) || !pending.raw || !pending.adjusted {
		s.Valid = false
	}
	if s.Valid {
		copy(s.Reflection[:s.Order], rc)
	}
	s.Energy = energy
	s.SqrtGain = sqrtGain
	snapshot := *s
	pending.owner = nil
	if silkNoiseAnalysisTraceHook != nil {
		silkNoiseAnalysisTraceHook(e, &snapshot)
	}
}

// WithSILKNoiseAnalysisTraceHook installs a bounded test-only capture around
// the actual unwarped shaping analysis for the duration of fn. wants selects
// which encoder/subframe pairs copy the fixed-size snapshot buffers.
func WithSILKNoiseAnalysisTraceHook(
	wants func(*Encoder, int32) bool,
	hook func(*Encoder, *SILKNoiseAnalysisTraceSnapshot),
	fn func(),
) {
	previousWants := silkNoiseAnalysisTraceWants
	previousHook := silkNoiseAnalysisTraceHook
	silkNoiseAnalysisTraceWants = wants
	silkNoiseAnalysisTraceHook = hook
	defer func() {
		silkNoiseAnalysisTraceWants = previousWants
		silkNoiseAnalysisTraceHook = previousHook
	}()
	fn()
}
