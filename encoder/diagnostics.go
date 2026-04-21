package encoder

import (
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/silk"
)

// SetSilkTrace enables SILK encoder tracing for parity diagnostics.
// Only applies when the SILK encoder is active.
func (e *Encoder) SetSilkTrace(trace *silk.EncoderTrace) {
	e.silkTrace = trace
	e.ensureSILKEncoder()
	e.silkEncoder.SetTrace(e.silkTrace)
}

// SetCELTTargetStatsHook installs a callback for per-frame CELT VBR diagnostics.
// Only applies when the CELT encoder is active.
func (e *Encoder) SetCELTTargetStatsHook(fn func(celt.CeltTargetStats)) {
	e.celtStatsHook = fn
	if e.celtEncoder != nil {
		e.celtEncoder.SetTargetStatsHook(fn)
	}
}

// SetCELTPrefilterDebugHook installs a callback for per-frame CELT prefilter diagnostics.
// Only applies when the CELT encoder is active.
func (e *Encoder) SetCELTPrefilterDebugHook(fn func(celt.PrefilterDebugStats)) {
	e.celtPrefilterHook = fn
	if e.celtEncoder != nil {
		e.celtEncoder.SetPrefilterDebugHook(fn)
	}
}
