//go:build gopus_fixed_point && gopus_qext

package gopus

import "github.com/thesyncim/gopus/internal/fixedpoint"

type decoderFixedQEXTFields struct {
	decoder *fixedpoint.QEXTCELTDecoder
	res     []int32
	invalid bool
}
