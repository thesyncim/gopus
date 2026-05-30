//go:build !gopus_fixedpoint

package multistream

// streamFixedFields is empty in the default build so the per-stream decoder
// state carries no FIXED_POINT integer CELT bookkeeping. The struct is embedded
// in streamState; an empty struct contributes zero size, keeping the default
// build byte-identical and the feature truly zero-cost.
type streamFixedFields struct{}
