//go:build gopus_custom_modes && gopus_fixed_point && gopus_qext

package custom

import "github.com/thesyncim/gopus/internal/fixedpoint"

func newFixedCustomCELTEncoder(channels int, mode fixedpoint.CELTCustomMode) *fixedpoint.CELTEncoder {
	return fixedpoint.NewCELTEncoderCustomQEXT(channels, mode)
}
