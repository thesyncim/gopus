package celt

import (
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// requireBitExactFloat skips a Tier-1 bit-exact CELT-float oracle on the fused
// arm64 SIMD build (celtFusedFloat), whose NEON-shaped float path is
// quality-gated (opus_compare) rather than byte-identical to the reference.
// Every amd64 build and the arm64 scalar builds run the oracle bit-exactly
// against their paired libopus reference.
func requireBitExactFloat(t *testing.T) {
	t.Helper()
	if celtFusedFloat {
		t.Skip("bit-exact vs scalar libopus; fused arm64 default build is quality-gated (asm amd64 / pure-Go arm64 hold the bit-exact oracle)")
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

// requirePairedCELTOracleMode allows exact float-stage comparisons only when
// the Go build and resolver select the same scalar or native SIMD lane.
func requirePairedCELTOracleMode(t *testing.T) libopustooling.LibopusReferenceVariant {
	t.Helper()
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatalf("resolve libopus reference variant: %v", err)
	}
	goSIMD := libopusFloatInnerProdUsesNeonOrder || libopusFloatInnerProdUsesSSEOrder
	if goSIMD != (variant == libopustooling.LibopusReferenceSIMD) {
		t.Fatalf("Go CELT mode and libopus reference do not match: Go SIMD=%t libopus=%s", goSIMD, variant)
	}
	return variant
}
