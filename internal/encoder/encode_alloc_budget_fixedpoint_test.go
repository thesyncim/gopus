//go:build gopus_fixedpoint

package encoder

// encodeHotPathCaseBudget is the per-call allocation ceiling for one steady-
// state Encode hot-path case under -tags gopus_fixedpoint.
//
// The integer CELT encode driver (internal/fixedpoint + the encoder fixed-CELT
// wiring) is strictly zero-alloc: all per-frame and per-band working buffers are
// encoder-owned scratch grown once and reused across every frame of a packet, so
// every CELT-mode case is guarded at zero exactly like the float build.
//
// The integer SILK encode bodies (the silk package) are out of this scope and
// still allocate a bounded, per-frame-count amount; their cases keep a measured
// ceiling so the guard still catches regressions there. These values are the
// observed steady-state allocation counts (mono/stereo, 20/60/120 ms); they must
// only ever shrink.
func encodeHotPathCaseBudget(c encodeAllocGuardCase) int {
	if c.mode == ModeCELT {
		return 0
	}
	if b, ok := fixedpointSilkAllocBudget[c.name]; ok {
		return b
	}
	return 0
}

// fixedpointSilkAllocBudget records the measured steady-state per-call
// allocation count of the integer SILK/Hybrid encode path (out of the CELT
// scratch scope) for each guard case.
var fixedpointSilkAllocBudget = map[string]int{
	"SILK-mono-20ms":      45,
	"SILK-stereo-20ms":    86,
	"SILK-mono-60ms":      135,
	"SILK-stereo-60ms":    262,
	"SILK-mono-120ms":     270,
	"Hybrid-mono-20ms":    45,
	"Hybrid-stereo-20ms":  81,
	"Hybrid-mono-120ms":   270,
	"Hybrid-stereo-120ms": 490,
}
