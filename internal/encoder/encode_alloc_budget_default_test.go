//go:build !gopus_fixed_point

package encoder

// encodeHotPathCaseBudget is the per-call allocation ceiling for one steady-
// state Encode hot-path case in the default (float) build: strictly zero across
// mono+stereo and CELT/SILK/Hybrid, single-frame and long/multi-frame packets.
func encodeHotPathCaseBudget(encodeAllocGuardCase) int { return 0 }
