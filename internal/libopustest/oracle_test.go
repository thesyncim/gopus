package libopustest

import (
	"path/filepath"
	"testing"
)

func TestOracleEnabledEnvironmentMatrix(t *testing.T) {
	tests := []struct {
		name   string
		oracle string
		tier   string
		strict string
		want   bool
	}{
		{name: "default_on", want: true},
		{name: "parity_on", tier: "parity", want: true},
		{name: "fast_off_without_tag", tier: "fast", want: oracleBuildTagEnabled},
		{name: "smoke_off_without_tag", tier: "smoke", want: oracleBuildTagEnabled},
		{name: "explicit_zero_off", oracle: "0", want: false},
		{name: "explicit_false_off", oracle: " false ", want: false},
		{name: "strict_overrides_fast", tier: "fast", strict: "true", want: true},
		{name: "strict_overrides_explicit_off", oracle: "off", strict: "yes", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOPUS_LIBOPUS_ORACLE", tc.oracle)
			t.Setenv("GOPUS_TEST_TIER", tc.tier)
			t.Setenv("GOPUS_STRICT_LIBOPUS_REF", tc.strict)
			if got := OracleEnabled(); got != tc.want {
				t.Fatalf("OracleEnabled()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestHelperOutputPathIncludesSourceAndFlavor(t *testing.T) {
	got := helperOutputPathForGOOS("/tmp/helpers", "gopus_helper", "tools/csrc/a.c", "ref", "linux", "amd64")
	if filepath.Base(got) != "gopus_helper_a_ref_linux_amd64" {
		t.Fatalf("helperOutputPathForGOOS()=%q", got)
	}

	otherSource := helperOutputPathForGOOS("/tmp/helpers", "gopus_helper", "tools/csrc/b.c", "ref", "linux", "amd64")
	if got == otherSource {
		t.Fatalf("source stem did not affect helper path: %q", got)
	}

	otherFlavor := helperOutputPathForGOOS("/tmp/helpers", "gopus_helper", "tools/csrc/a.c", "dred", "linux", "amd64")
	if got == otherFlavor {
		t.Fatalf("flavor did not affect helper path: %q", got)
	}
}

func TestHelperOutputPathUsesWindowsSuffix(t *testing.T) {
	got := helperOutputPathForGOOS("/tmp/helpers", "gopus_helper", "tools/csrc/a.c", "ref", "windows", "arm64")
	if filepath.Base(got) != "gopus_helper_a_ref_windows_arm64.exe" {
		t.Fatalf("helperOutputPathForGOOS(windows)=%q", got)
	}
}
