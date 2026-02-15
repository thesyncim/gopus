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
