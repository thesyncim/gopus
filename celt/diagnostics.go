package celt

// CeltTargetStats captures per-frame VBR target diagnostics for CELT.
type CeltTargetStats struct {
	FrameSize     int
	BaseBits      int
	TargetBits    int
	Tonality      float64
	DynallocBoost int
	TFBoost       int
	PitchChange   bool
	FloorLimited  bool
	MaxDepth      float64
}

// PrefilterDebugStats captures optional per-frame prefilter diagnostics.
type PrefilterDebugStats struct {
	Frame          int
	Enabled        bool
	UsedTonePath   bool
	UsedPitchPath  bool
	TFEstimate     float64
	NBBytes        int
	ToneFreq       float64
	Toneishness    float64
	MaxPitchRatio  float64
	PitchSearchOut int
	PitchBeforeRD  int
	PitchAfterRD   int
	PFOn           bool
	QG             int
	Gain           float64
}

// coarseDecisionStats captures optional per-band coarse energy diagnostics.
type coarseDecisionStats struct {
	Frame     int
	Band      int
	Channel   int
	Intra     bool
	LM        int
	ProbFS0   int
	ProbDecay int
	X         float64
	Pred      float64
	Residual  float64
	QIInitial int
	QIFinal   int
	Tell      int
	BitsLeft  int
}

// SetPrefilterDebugHook installs a callback that receives per-frame prefilter stats.
//
// This hook is intended for parity investigation and development diagnostics.
func (e *Encoder) SetPrefilterDebugHook(fn func(PrefilterDebugStats)) {
	e.prefilterDebugHook = fn
}

// SetTargetStatsHook installs a callback that receives per-frame CELT VBR targets.
//
// This hook is intended for parity investigation and development diagnostics.
func (e *Encoder) SetTargetStatsHook(fn func(CeltTargetStats)) {
	e.targetStatsHook = fn
}

func (e *Encoder) emitTargetStats(stats CeltTargetStats, baseBits, targetBits int) {
	if e.targetStatsHook == nil {
		return
	}
	stats.BaseBits = baseBits
	stats.TargetBits = targetBits
	e.targetStatsHook(stats)
}
