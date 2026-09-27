//go:build !gopus_fixed_point

package gopus

// decodeInt16HotPathAllocBudget is the per-call allocation budget for
// DecodeInt16 in the default (float) build: strictly zero.
const decodeInt16HotPathAllocBudget = 0

// SILK mono/stereo loss and recovery reuse decoder-owned working buffers.
const (
	silkPLCMonoHotPathAllocBudget   = 0
	silkPLCStereoHotPathAllocBudget = 0
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
