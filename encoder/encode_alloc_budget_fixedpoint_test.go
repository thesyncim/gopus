//go:build gopus_fixedpoint

package encoder

// encodeHotPathAllocBudget bounds per-call allocations of the steady-state
// Encode hot path under -tags gopus_fixedpoint, where it drives the integer
// FIXED_POINT SILK/CELT encode bodies. Those drivers are not yet fully
// zero-alloc; this is a measured ceiling that catches regressions while the
// default (float) build stays strictly zero-alloc. The measured worst case is
// CELT stereo 120 ms (~1623 allocs/op); the ceiling keeps headroom above it.
const encodeHotPathAllocBudget = 2048
