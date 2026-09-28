package libopustest

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestResolveLibopusDREDQEXTReferenceMatchesGoISA(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	if err := validateDREDReferenceBuildPairing(); err != nil {
		var configErr *libopustooling.LibopusReferenceConfigError
		if !errors.As(err, &configErr) {
			t.Fatalf("fixed+DRED reference rejection error=%T %v, want LibopusReferenceConfigError", err, err)
		}
		if !strings.Contains(err.Error(), "FIXED_POINT with ENABLE_DRED") {
			t.Fatalf("fixed+DRED reference rejection=%q lacks incompatible-feature explanation", err)
		}
		if _, err := resolveDREDQEXTReferenceVariantForCurrentBuild(); !errors.As(err, &configErr) {
			t.Fatalf("DRED-QEXT resolver error=%T %v, want LibopusReferenceConfigError", err, err)
		}
		pathRejected := false
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					pathErr, ok := recovered.(error)
					if !ok || !errors.As(pathErr, &configErr) || !strings.Contains(pathErr.Error(), "FIXED_POINT with ENABLE_DRED") {
						t.Fatalf("DRED-QEXT path panic=%v, want explicit incompatible-feature error", recovered)
					}
					pathRejected = true
				}
			}()
			_ = DREDQEXTRefPath(".libs", "libopus.a")
		}()
		if !pathRejected {
			t.Fatal("DRED-QEXT path returned an archive for unsupported fixed+DRED pairing")
		}
		if _, err := BuildCHelper(CHelperConfig{
			Label:       "unsupported fixed+DRED oracle",
			OutputBase:  "unsupported_fixed_dred_oracle",
			SourceFile:  "unused.c",
			DREDQEXTRef: true,
		}); !errors.As(err, &configErr) {
			t.Fatalf("DRED-QEXT helper error=%T %v, want LibopusReferenceConfigError", err, err)
		}
		if _, err := BuildDREDHelper(t.TempDir(), "unused.c", "unsupported_fixed_dred", false); !errors.As(err, &configErr) {
			t.Fatalf("DRED helper error=%T %v, want LibopusReferenceConfigError", err, err)
		}
		return
	}
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
