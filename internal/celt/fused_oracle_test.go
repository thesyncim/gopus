package celt

import (
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// requireRenormalizeVectorOracleMode checks that renormalization uses the
// matching libopus instruction lane. The arm64 C kernel uses NEON even when
// the helper passes arch=0.
func requireRenormalizeVectorOracleMode(t *testing.T) {
	t.Helper()
	if runtime.GOARCH != "arm64" {
		requirePairedCELTOracleMode(t)
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
