//go:build !gopus_fixedpoint

package gopus

// decodeInt16HotPathAllocBudget is the per-call allocation budget for
// DecodeInt16 in the default (float) build: strictly zero.
const decodeInt16HotPathAllocBudget = 0

// encodeRestrictedSilkHotPathAllocBudget is the per-call allocation budget for
// Encode in restricted-SILK mode in the default (float) build: strictly zero.
const encodeRestrictedSilkHotPathAllocBudget = 0
