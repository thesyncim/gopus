package celt

import (
	"runtime"
	"testing"
)

// requireBitExactFloat skips a Tier-1 bit-exact CELT-float oracle on the builds
// whose float path cannot be byte-identical to the libopus reference the CI
// links:
//
//   - the fused arm64 default build (celtFusedFloat): the NEON-shaped float path
//     is quality-gated (opus_compare) rather than byte-identical to scalar C, the
//     same posture libopus's own NEON kernels take.
//   - the amd64 pure-Go build (-tags purego): gopus runs scalar Go float
//     (libopusFloatInnerProdUsesSSEOrder is false), but the linux/amd64 CI libopus
//     is the autoconf-default SSE/AVX RTCD build, so a scalar-vs-SIMD comparison
//     would diverge by ~1 ULP. Comparing the pure-Go float path against a SIMD
//     reference is not a fair bit-exact oracle.
//
// Bit-exact coverage of these kernels still runs where the comparison is fair:
// the amd64 asm build (SSE-ordered gopus vs SSE libopus) and the arm64 pure-Go
// build (scalar gopus vs the scalar libopus on that runner). The skipped builds'
// correctness is covered there plus the end-to-end quality gates.
func requireBitExactFloat(t *testing.T) {
	t.Helper()
	if celtFusedFloat {
		t.Skip("bit-exact vs scalar libopus; fused arm64 default build is quality-gated (asm amd64 / pure-Go arm64 hold the bit-exact oracle)")
	}
	if runtime.GOARCH == "amd64" && !libopusFloatInnerProdUsesSSEOrder {
		t.Skip("bit-exact vs SIMD libopus; amd64 pure-Go float path is quality-gated (asm amd64 / pure-Go arm64 hold the bit-exact oracle)")
	}
}
