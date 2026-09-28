//go:build gopus_fixed_point

package multistream

import (
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// streamFixedFields carries the FIXED_POINT integer CELT decoder used by the
// gopus_fixed_point build to produce integer-exact opus_res output for a single
// elementary stream of a multistream packet. It is embedded in streamState and
// the CELT decoder is created lazily on the first CELT-only / Hybrid frame so
// SILK-only streams pay no allocation.
type streamFixedFields struct {
	streamFixedMultiframeFields
	fixedCELT    *fixedpoint.CELTDecoder
	fixedCELTPCM []int16
	fixedRes     []int32

	// fixedTransitionRes and fixedTransitionMain hold the integer-domain
	// previous-CELT PLC and raw target frame for an in-flight CELT-boundary
	// transition. The float decoder still advances its own PLC state; the fixed
	// output bridge uses these buffers to reproduce opus_res smooth_fade.
	fixedTransitionRes     []int32
	fixedTransitionMain    []int32
	fixedTransitionReady   bool
	fixedTransitionArmed   bool
	fixedTransitionHasMain bool
	fixedTransitionGainQ8  int32
	qext                   streamFixedQEXTFields

	// fixedHybridHook implements hybrid.FixedHybridHighband for the integer
	// Hybrid highband decode (start band 17, celt_accum onto the SILK opus_res
	// lowband). It is armed on the stream's hybrid decoder only while an integer
	// Hybrid frame is in flight and shares fixedCELT with the CELT-only path.
	fixedHybridHook            *streamFixedHybridHook
	fixedHybridRes             []int32
	fixedHybridPLCLowband      []int16
	fixedHybridPLCCapturing    bool
	fixedHybridPLCCursor       int
	fixedHybridEnd             int
	fixedHybridRangeDecoder    rangecoding.Decoder
	fixedHybridRedundant       bool
	fixedHybridRedundantToSilk bool
	fixedHybridRedundantData   []byte
	fixedHybridRedundantRes    []int32
	fixedHybridRedundantValid  bool
	fixedHybridCodedChannels   int
	fixedHybridPrevMode        int32
	fixedHybridPrevRedundancy  bool
	fixedHybridHandled         bool
}

// decoderFixedFields holds caller-independent output scratch for the integer
// multistream decode. The mapped result aliases this storage until the next
// fixed decode on the same decoder.
type decoderFixedFields struct {
	fixedStreamRes [][]int32
	fixedOutput    []int32
}

// setFixedRedundancy records the redundancy decision already read by the float
// SILK or Hybrid path. The fixed CELT decoder uses these packet bytes without
// reparsing the shared range decoder.
func (d *streamState) setFixedRedundancy(redundant, celtToSilk bool, data []byte, codedChannels int) {
	d.fixedHybridRedundant = redundant
	d.fixedHybridRedundantToSilk = redundant && celtToSilk
	d.fixedHybridRedundantData = nil
	d.fixedHybridRedundantValid = false
	d.fixedHybridCodedChannels = codedChannels
	if redundant {
		d.fixedHybridRedundantData = data
	}
}
