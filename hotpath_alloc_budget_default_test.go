//go:build !gopus_fixed_point

package gopus

// decodeInt16HotPathAllocBudget is the per-call allocation budget for
// DecodeInt16 in the default (float) build: strictly zero.
const decodeInt16HotPathAllocBudget = 0

// SILK packet-loss-concealment budgets. The gopus decode entry (decoder.go /
// decoder_opus_frame.go) is strictly zero-alloc for SILK PLC: the concealment
// output is written into decoder-owned scratch and copied to the caller buffer.
// The residual allocations come solely from the SILK PLC concealment kernel
// (plc.ConcealSILKWithLTP), which allocates its own working buffers per call
// (output, sLTP, sLTPQ15, sLPC). These ceilings bound that kernel footprint
// (one ConcealSILKWithLTP call for mono, two for stereo) and guard against
// regressions reintroducing per-call allocations in the decode entry.
const (
	silkPLCMonoHotPathAllocBudget   = 4
	silkPLCStereoHotPathAllocBudget = 7
)

// Multistream decode budget (default float build). The single-stream
// Decoder/Encoder hot paths and the multistream encoder are strictly
// zero-alloc; the multistream decoder retains a small bounded per-frame
// footprint: the elementary CELT/SILK/Hybrid per-stream output buffers and the
// opus framing parse (parseOpusPacket, invoked for the duration probe and the
// decode) allocate, and the channel-mapped output is returned to the caller.
//
// This bound catches regressions while documenting the residual.
const multistreamDecodeHotPathAllocBudget = 8
