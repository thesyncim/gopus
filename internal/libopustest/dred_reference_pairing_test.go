package libopustest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
