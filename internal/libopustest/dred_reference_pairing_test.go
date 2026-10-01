package libopustest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestDREDReferenceRejectsFeatureAndInstructionMismatches(t *testing.T) {
	const features = "#define ENABLE_DRED 1\n#define ENABLE_DEEP_PLC 1\n"
	tests := []struct {
		name   string
		config string
		wantOK bool
	}{
		{"scalar_paired", features, true},
		{"missing_dred", "#define ENABLE_DEEP_PLC 1\n", false},
		{"missing_deep_plc", "#define ENABLE_DRED 1\n", false},
		{"osce_enabled", features + "#define ENABLE_OSCE 1\n", false},
		{"osce_zero_defined", features + "#define ENABLE_OSCE 0\n", false},
		{"qext_enabled", features + "#define ENABLE_QEXT 1\n", false},
		{"custom_empty_defined", features + "#define CUSTOM_MODES\n", false},
		{"fixed_point", features + "#define FIXED_POINT 1\n", false},
		{"scalar_with_neon", features + "#define OPUS_ARM_MAY_HAVE_NEON_INTR 1\n", false},
		{"scalar_with_sse", features + "#define OPUS_X86_MAY_HAVE_SSE2 1\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buildDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(buildDir, "config.h"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			err := validateDREDInstructionBuild(buildDir, dredScalarDNNBuild)
			if (err == nil) != tc.wantOK {
				t.Fatalf("validateDREDInstructionBuild() error=%v wantOK=%v", err, tc.wantOK)
			}
		})
	}

	var simdMacro string
	switch runtime.GOARCH {
	case "arm64":
		simdMacro = "#define OPUS_ARM_MAY_HAVE_NEON_INTR 1\n"
	case "amd64":
		simdMacro = "#define OPUS_X86_MAY_HAVE_SSE2 1\n"
	default:
		return
	}
	for _, tc := range []struct {
		name   string
		config string
		wantOK bool
	}{
		{"simd_paired", features + simdMacro, true},
		{"simd_without_instruction_macro", features, false},
		{"simd_missing_deep_plc", strings.Replace(features+simdMacro, "#define ENABLE_DEEP_PLC 1\n", "", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buildDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(buildDir, "config.h"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			err := validateDREDInstructionBuild(buildDir, dredSIMDDNNBuild)
			if (err == nil) != tc.wantOK {
				t.Fatalf("validateDREDInstructionBuild() error=%v wantOK=%v", err, tc.wantOK)
			}
		})
	}
}

func TestPublicDNNReferenceIdentityUsesBuilderStampContract(t *testing.T) {
	if _, err := libopustooling.FindCCompiler(); err != nil {
		t.Skipf("C compiler unavailable: %v", err)
	}
	tests := []struct {
		name     string
		identity PublicAPIReferenceIdentity
		flavor   string
	}{
		{
			name: "dred_scalar",
			identity: PublicAPIReferenceIdentity{
				DNN: true, DRED: true, Variant: libopustooling.LibopusReferenceScalar,
			},
			flavor: "dred",
		},
		{
			name: "dred_simd",
			identity: PublicAPIReferenceIdentity{
				DNN: true, DRED: true, Variant: libopustooling.LibopusReferenceSIMD,
			},
			flavor: "dred-simd",
		},
		{
			name: "osce_qext",
			identity: PublicAPIReferenceIdentity{
				DNN: true, OSCE: true, QEXT: true, Variant: libopustooling.LibopusReferenceScalar,
			},
			flavor: "dnn-osce-qext",
		},
		{
			name: "osce_custom",
			identity: PublicAPIReferenceIdentity{
				DNN: true, OSCE: true, Custom: true, Variant: libopustooling.LibopusReferenceScalar,
			},
			flavor: "dnn-osce-custom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.identity.dnnBuildConfig()
			if cfg.buildFlavor != tc.flavor {
				t.Fatalf("dnnBuildConfig().buildFlavor=%q, want %q", cfg.buildFlavor, tc.flavor)
			}

			buildDir := t.TempDir()
			if err := cfg.writeStamp(buildDir); err != nil {
				t.Fatalf("write matching build stamp: %v", err)
			}
			if !cfg.buildCurrent(buildDir) {
				t.Fatal("matching builder stamp rejected")
			}

			wrongVariant := tc.identity
			if wrongVariant.Variant == libopustooling.LibopusReferenceSIMD {
				wrongVariant.Variant = libopustooling.LibopusReferenceScalar
			} else {
				wrongVariant.Variant = libopustooling.LibopusReferenceSIMD
			}
			if wrongVariant.dnnBuildConfig().buildCurrent(buildDir) {
				t.Fatal("build stamp for a different instruction variant accepted")
			}

			entries, err := os.ReadDir(buildDir)
			if err != nil {
				t.Fatalf("read build stamp directory: %v", err)
			}
			staleStampFound := false
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".gopus-") || !strings.Contains(entry.Name(), "build") {
					continue
				}
				staleStampFound = true
				if err := os.WriteFile(filepath.Join(buildDir, entry.Name()), []byte("stale build identity\n"), 0o644); err != nil {
					t.Fatalf("write stale build stamp: %v", err)
				}
			}
			if !staleStampFound {
				t.Fatal("builder did not write a build stamp")
			}
			if cfg.buildCurrent(buildDir) {
				t.Fatal("stale build stamp accepted")
			}
		})
	}
}
