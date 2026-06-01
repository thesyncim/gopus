//go:build gopus_fixedpoint

package gopus

// decodeInt16HotPathAllocBudget is the per-call allocation budget for
// DecodeInt16 under -tags gopus_fixedpoint, where it additionally runs the
// integer FIXED_POINT CELT decoder (internal/fixedpoint.CELTDecoder) for
// libopus-exact output. That decoder is not yet fully zero-alloc; this budget
// bounds its per-frame allocations and exists only in the gated build (the
// default build remains strictly zero-alloc).
const decodeInt16HotPathAllocBudget = 80

// encodeRestrictedSilkHotPathAllocBudget bounds per-call allocations of Encode
// in restricted-SILK mode under -tags gopus_fixedpoint, where it runs the
// integer FIXED_POINT SILK encode driver (silkEncodeFramePayloadFIX). That
// driver is not yet fully zero-alloc; this budget bounds its per-frame
// allocations and exists only in the gated build (the default build remains
// strictly zero-alloc).
const encodeRestrictedSilkHotPathAllocBudget = 64

// SILK packet-loss-concealment budgets under -tags gopus_fixedpoint. SILK PLC
// runs the same float concealment path as the default build, so the residual
// allocations are identical: the decode entry is zero-alloc and only the SILK
// PLC kernel (plc.ConcealSILKWithLTP) allocates its working buffers.
const (
	silkPLCMonoHotPathAllocBudget   = 4
	silkPLCStereoHotPathAllocBudget = 7
)

// Multistream wrapper budgets under -tags gopus_fixedpoint. The float Decode
// path matches the default build; the integer DecodeInt16/Int24 paths run the
// FIXED_POINT elementary decoders, which are not yet zero-alloc. These bounds
// are measured ceilings for the default stereo configuration.
const (
	multistreamEncodeHotPathAllocBudget = 1
	multistreamDecodeHotPathAllocBudget = 8
)
