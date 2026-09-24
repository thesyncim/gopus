package celt

import (
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// requireBitExactFloat skips a Tier-1 bit-exact CELT-float oracle on the builds
// whose float path cannot be byte-identical to the libopus reference the CI
// links:
//
//   - the fused arm64 default build (celtFusedFloat): the NEON-shaped float path
//     is quality-gated (opus_compare) rather than byte-identical to scalar C, the
//     same posture libopus's own NEON kernels take.
//   - the amd64 pure-Go build (-tags nosimd): gopus runs scalar Go float
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

// requireRenormalizeVectorOracleMode keeps the vector renormalization oracle
// enabled on arm64 when the caller selects the matching libopus build. The
// pinned arm64 libopus config compiles celt_inner_prod to NEON even when the
// helper passes arch=0, so fused Go SIMD needs the default C reference and the
// scalar Go builds need GOPUS_LIBOPUS_REF_SCALAR=1.
func requireRenormalizeVectorOracleMode(t *testing.T) {
	t.Helper()
	if runtime.GOARCH != "arm64" {
		requireBitExactFloat(t)
		return
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve libopus reference variant: %v", err)
	}
	if celtFusedFloat != (variant == libopustooling.LibopusReferenceSIMD) {
		t.Fatalf("Go CELT float mode and libopus reference do not match: Go SIMD=%t libopus=%s", celtFusedFloat, variant)
	}
}
