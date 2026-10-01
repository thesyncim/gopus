//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

func gemmAccumF32GenericReferenceProduct(weight, input float32) float32 {
	return round32(weight * input)
}
