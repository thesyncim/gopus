//go:build !arm64

package opusmath

import "math"

// SelectF32 returns a when c holds and b otherwise. Both operands move to
// integer registers first so the compiler lowers the choice to a conditional
// move instead of a branch on targets without scalar FP conditional select.
func SelectF32(c bool, a, b float32) float32 {
	ra, rb := math.Float32bits(a), math.Float32bits(b)
	r := rb
	if c {
		r = ra
	}
	return math.Float32frombits(r)
}
