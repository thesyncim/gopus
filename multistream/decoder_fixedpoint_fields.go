//go:build gopus_fixedpoint

package multistream

import "github.com/thesyncim/gopus/internal/fixedpoint"

// streamFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixedpoint build to produce integer-exact opus_res output for a single
// elementary stream of a multistream packet. It is embedded in streamState and
// the CELT decoder is created lazily on the first CELT-only / Hybrid frame so
// SILK-only streams pay no allocation.
type streamFixedFields struct {
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16
	fixedRes     []int32

	// fixedHybridHook implements hybrid.FixedHybridHighband for the integer
	// Hybrid highband decode (start band 17, celt_accum onto the SILK opus_res
	// lowband). It is armed on the stream's hybrid decoder only while an integer
	// Hybrid frame is in flight and shares fixedCELT with the CELT-only path.
	fixedHybridHook     *streamFixedHybridHook
	fixedHybridRes      []int32
	fixedHybridEnd      int
	fixedHybridFrameLen int
	fixedHybridHandled  bool
}
