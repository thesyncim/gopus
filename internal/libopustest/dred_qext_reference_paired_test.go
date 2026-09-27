package libopustest

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestResolveLibopusDREDQEXTReferenceMatchesGoISA(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	want := libopustooling.LibopusReferenceDREDQEXTScalar
	if goReferenceSIMDEnabled() && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		want = libopustooling.LibopusReferenceDREDQEXTSIMD
	}
	got, err := libopustooling.ResolveLibopusDREDQEXTReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("DRED-QEXT reference=%q want %q", got, want)
	}

	path := DREDQEXTRefPath(".libs", "libopus.a")
	if filepath.Base(filepath.Dir(filepath.Dir(path))) != "opus-"+libopustooling.DefaultVersion+mustReferenceSuffix(t, want) {
		t.Fatalf("DRED-QEXT archive path=%q does not select %s", path, want)
	}
}

func goReferenceSIMDEnabled() bool {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	return err == nil && variant == libopustooling.LibopusReferenceSIMD
}

func mustReferenceSuffix(t *testing.T, variant libopustooling.LibopusReferenceVariant) string {
	t.Helper()
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	return suffix
}

func TestHelperRefDirSelectsDREDQEXTTree(t *testing.T) {
	scalar := helperRefDir(CHelperConfig{DREDQEXTRef: true}, libopustooling.LibopusReferenceScalar)
	if filepath.Base(scalar) != "opus-1.6.1-dred-qext-scalar" {
		t.Fatalf("DRED-QEXT scalar ref dir=%q", scalar)
	}
	simd := helperRefDir(CHelperConfig{DREDQEXTRef: true}, libopustooling.LibopusReferenceSIMD)
	if filepath.Base(simd) != "opus-1.6.1-dred-qext-simd" {
		t.Fatalf("DRED-QEXT SIMD ref dir=%q", simd)
	}
}

func TestCHelperReferenceSelectionRejectsConflictingVariants(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  CHelperConfig
	}{
		{name: "fixed", cfg: CHelperConfig{DREDQEXTRef: true, FixedRef: true}},
		{name: "fixed_qext", cfg: CHelperConfig{DREDQEXTRef: true, FixedQEXTRef: true}},
		{name: "qext", cfg: CHelperConfig{DREDQEXTRef: true, QEXTRef: true}},
		{name: "custom", cfg: CHelperConfig{DREDQEXTRef: true, CustomRef: true}},
		{name: "force_scalar", cfg: CHelperConfig{DREDQEXTRef: true, ForceScalarRef: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCHelperReferenceSelection(tc.cfg)
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("selection error=%v want LibopusReferenceConfigError", err)
			}
		})
	}

	if err := validateCHelperReferenceSelection(CHelperConfig{DREDQEXTRef: true, SIMDRef: true}); err != nil {
		t.Fatalf("matched DRED-QEXT SIMD selection rejected: %v", err)
	}
}
