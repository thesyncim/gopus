package celt

import (
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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

// requireCELTV3OracleTarget keeps amd64.v3 kernel witnesses paired with a
// separately built libopus v3 reference. An unset target uses the baseline C
// tree, so target-specific witnesses skip outside the v3 audit runner.
func requireCELTV3OracleTarget(t *testing.T) {
	t.Helper()
	if level := libopustooling.CompiledGoAMD64Level(); level != "v3" {
		t.Fatalf("CELT v3 oracle witness was compiled for GOAMD64=%q, want v3", level)
	}
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err != nil {
		t.Fatalf("resolve matching libopus AMD64 target: %v", err)
	}
	if target == "" {
		message := "CELT v3 oracle witness requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}
	if target != "v3" {
		t.Fatalf("CELT v3 oracle witness requires matching libopus v3 oracle, got target %q", target)
	}
}
