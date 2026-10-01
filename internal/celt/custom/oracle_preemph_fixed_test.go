//go:build gopus_custom_modes && gopus_fixed_point

package custom_test

// oraclePreemphExpected returns the normalized coefficient after the
// FIXED_POINT QCONST16 conversion used by celt/modes.c.
func oraclePreemphExpected(preemph [4]float32, i int) float32 {
	shift := [...]uint{15, 15, 12, 13}[i]
	value := preemph[i]
	negative := value < 0
	if negative {
		value = -value
	}
	scaled := float32(value * float32(uint32(1)<<shift))
	q := int32(float64(scaled) + 0.5)
	if negative {
		q = -q
	}
	return float32(q) / float32(uint32(1)<<shift)
}
