//go:build gopus_fixed_point

package gopus

// DecodeInt16 writes libopus-exact fixed-point output into caller-owned
// storage without allocations after warmup.
const decodeInt16HotPathAllocBudget = 0

// SILK packet-loss-concealment budgets under -tags gopus_fixed_point. SILK PLC
// runs the same float concealment path as the default build, so the residual
// allocations are identical: the decode entry is zero-alloc and only the SILK
// PLC kernel (plc.ConcealSILKWithLTP) allocates its working buffers.
const (
	silkPLCMonoHotPathAllocBudget   = 4
	silkPLCStereoHotPathAllocBudget = 7
)

// Multistream decode under -tags gopus_fixed_point uses fixed-point elementary
// decoders. This ceiling includes wrapper allocations for the default stereo
// configuration. The multistream encoder is zero-alloc in every build.
const multistreamDecodeHotPathAllocBudget = 8
