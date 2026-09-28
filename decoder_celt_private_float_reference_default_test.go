//go:build !gopus_fixed_point

package gopus

// Non-fixed builds already resolve the public helper to the matching float
// feature and ISA archive, including DNN and custom-mode builds.
func privateFloatCELTAPIRateReferenceHelperPath() (string, error) {
	return apiRateReferenceHelperPath()
}
