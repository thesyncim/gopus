//go:build arm64

package opusmath

// SelectF32 returns a when c holds and b otherwise. On arm64 the compiler
// lowers this directly to FCSELS in FP registers without FP-to-GPR bit moves.
func SelectF32(c bool, a, b float32) float32 {
	if c {
		return a
	}
	return b
}
