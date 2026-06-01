//go:build !gopus_fixedpoint

package encoder

// encodeHotPathAllocBudget is the per-call allocation ceiling for the steady-
// state Encode hot path in the default (float) build: strictly zero across
// mono+stereo and CELT/SILK/Hybrid, single-frame and long/multi-frame packets.
const encodeHotPathAllocBudget = 0
