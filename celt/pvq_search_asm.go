//go:build arm64 && !purego

package celt

// pvqSearchBestPos finds the position with the best rate-distortion score
// for placing a pulse.
//
// The function computes:
//
//	bestID = 0; bestNum = (xy+absX[0])^2; bestDen = yy+y[0]
//	for j = 1..n-1: if bestDen*(xy+absX[j])^2 > (yy+y[j])*bestNum: update best
//
//go:noescape
func pvqSearchBestPos(absX, y []float32, xy, yy float32, n int) int

// pvqSearchPulseLoop places pulsesLeft pulses one at a time into the best
// position using the rate-distortion criterion. It merges the entire outer
// pulse loop and inner position search into a single assembly call,
// eliminating per-pulse Go→asm transition overhead.
//
// On entry:
//
//	absX[0..n-1] = absolute values of input vector (read-only)
//	y[0..n-1]    = 2*iy[j] pulse counts (modified in-place: y[bestID] += 2 per pulse)
//	iy[0..n-1]   = int32 pulse counts (modified in-place: iy[bestID]++ per pulse)
//	xy, yy       = running cross-correlation and energy
//	n            = vector dimension
//	pulsesLeft   = number of pulses to place
//
// Returns updated (xy, yy).
//
//go:noescape
func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32)
