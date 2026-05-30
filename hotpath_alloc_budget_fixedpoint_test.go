//go:build gopus_fixedpoint

package gopus

// decodeInt16HotPathAllocBudget is the per-call allocation budget for
// DecodeInt16 under -tags gopus_fixedpoint, where it additionally runs the
// integer FIXED_POINT CELT decoder (internal/fixedpoint.CELTDecoder) for
// libopus-exact output. That decoder is not yet fully zero-alloc; this budget
// bounds its per-frame allocations and exists only in the gated build (the
// default build remains strictly zero-alloc).
const decodeInt16HotPathAllocBudget = 80
