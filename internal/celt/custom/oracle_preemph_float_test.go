//go:build gopus_custom_modes && !gopus_fixed_point

package custom_test

func oraclePreemphExpected(preemph [4]float32, i int) float32 {
	return preemph[i]
}
