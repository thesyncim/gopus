//go:build !gopus_fixed_point

package multistream

import "math"

func projectionShortOracleBits(_ *Encoder, _, _ int, sample float32) uint32 {
	return math.Float32bits(sample)
}
