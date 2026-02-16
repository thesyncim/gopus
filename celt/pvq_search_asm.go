//go:build arm64 || amd64

package celt

// pvqSearchBestPos finds the position with the best rate-distortion score
// for placing a pulse. xy and yy are passed as float64 to avoid float32
// stack layout issues; they are converted to float32 inside the assembly.
//
// The function computes:
//   bestID = 0; bestNum = (xy+absX[0])^2; bestDen = yy+y[0]
//   for j = 1..n-1: if bestDen*(xy+absX[j])^2 > (yy+y[j])*bestNum: update best
//
//go:noescape
func pvqSearchBestPos(absX, y []float32, xy, yy float64, n int) int

// pvqSearchPulseLoop places pulsesLeft pulses one at a time into the best
// position using the rate-distortion criterion. It merges the entire outer
// pulse loop and inner position search into a single assembly call,
// eliminating per-pulse Go→asm transition overhead.
//
// On entry:
//   absX[0..n-1] = absolute values of input vector (read-only)
//   y[0..n-1]    = 2*iy[j] pulse counts (modified in-place: y[bestID] += 2 per pulse)
//   iy[0..n-1]   = integer pulse counts (modified in-place: iy[bestID]++ per pulse)
//   xy, yy       = running cross-correlation and energy (float32 via float64 args)
//   n            = vector dimension
//   pulsesLeft   = number of pulses to place
//
// Returns updated (xy, yy) as float64 (containing float32 values).
//
//go:noescape
func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64)

// pvqExtractAbsSign converts float64 input x to float32 absolute values and
// extracts sign bits. For each element: absX[j] = float32(|x[j]|),
// signx[j] = 1 if x[j] < 0, else 0. Also zeros iy[0..n-1] and y[0..n-1].
//
//go:noescape
func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []int, iy []int, n int)
