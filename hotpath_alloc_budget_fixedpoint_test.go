//go:build gopus_fixed_point

package gopus

// DecodeInt16 writes libopus-exact fixed-point output into caller-owned
// storage without allocations after warmup.
const decodeInt16HotPathAllocBudget = 0

// SILK mono/stereo loss and recovery reuse decoder-owned working buffers.
const (
	silkPLCMonoHotPathAllocBudget   = 0
	silkPLCStereoHotPathAllocBudget = 0
)

// Multistream decode under -tags gopus_fixed_point uses fixed-point elementary
// decoders. This ceiling includes wrapper allocations for the default stereo
// configuration. The multistream encoder is zero-alloc in every build.
const multistreamDecodeHotPathAllocBudget = 8
