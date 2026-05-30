//go:build gopus_fixedpoint

package multistream

import "github.com/thesyncim/gopus/internal/fixedpoint"

// streamFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixedpoint build to produce integer-exact opus_res output for a single
// elementary stream of a multistream packet. It is embedded in streamState and
// the CELT decoder is created lazily on the first CELT-only frame so SILK-only
// streams pay no allocation.
type streamFixedFields struct {
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16
	fixedRes     []int32
}
