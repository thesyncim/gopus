package libopustest

import (
	"os"
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

func TestHelperOutputPathPlacesDigestBeforeWindowsSuffix(t *testing.T) {
	got := helperOutputPathForGOOSWithDigest("/tmp/helpers", "gopus_helper", "tools/csrc/a.c", "ref", "windows", "arm64", "abc123")
	if filepath.Base(got) != "gopus_helper_a_ref_windows_arm64_abc123.exe" {
		t.Fatalf("helperOutputPathForGOOSWithDigest(windows)=%q", got)
	}
}

func TestHelperNeedsConfigFollowsConfigFlag(t *testing.T) {
	if !helperNeedsConfig([]string{"-O2", "-DHAVE_CONFIG_H"}) {
		t.Fatal("helperNeedsConfig missed -DHAVE_CONFIG_H")
	}
	if !helperNeedsConfig([]string{"-DHAVE_CONFIG_H=1"}) {
		t.Fatal("helperNeedsConfig missed -DHAVE_CONFIG_H=1")
	}
	if helperNeedsConfig([]string{"-O2", "-DNDEBUG"}) {
		t.Fatal("helperNeedsConfig unexpectedly enabled without config flag")
	}
}

func TestHelperReferenceLibMissingOnlyTracksReferenceLibraries(t *testing.T) {
	refDir := t.TempDir()
	missingRefLib := filepath.Join(refDir, ".libs", "libopus.a")
	if !helperReferenceLibMissing([]string{missingRefLib, "-lm"}, refDir) {
		t.Fatal("missing reference lib was not detected")
	}

	if err := os.MkdirAll(filepath.Dir(missingRefLib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missingRefLib, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if helperReferenceLibMissing([]string{missingRefLib, "-lm"}, refDir) {
		t.Fatal("existing reference lib was reported missing")
	}
	if helperReferenceLibMissing([]string{filepath.Join(t.TempDir(), "libmissing.a")}, refDir) {
		t.Fatal("external missing lib should not trigger reference bootstrap")
	}
}

func TestHelperConfigDigestTracksBuildInputs(t *testing.T) {
	tmp := t.TempDir()
	refDir := filepath.Join(tmp, "ref")
	if err := os.MkdirAll(filepath.Join(refDir, "silk"), 0o755); err != nil {
		t.Fatal(err)
	}
	srcPath := filepath.Join(tmp, "helper.c")
	if err := os.WriteFile(srcPath, []byte("int main(void) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "config.h"), []byte("#define OPUS_VERSION \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "silk", "ref.c"), []byte("int ref(void) { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := CHelperConfig{
		OutputBase: "gopus_helper",
		SourceFile: "helper.c",
		CFlags:     []string{"-DHAVE_CONFIG_H"},
		RefSources: []string{"silk/ref.c"},
	}
	base := helperConfigDigest(cfg, refDir, srcPath)
	cfg.CFlags = append(cfg.CFlags, "-DNDEBUG")
	if got := helperConfigDigest(cfg, refDir, srcPath); got == base {
		t.Fatal("digest did not change when C flags changed")
	}
	cfg.CFlags = []string{"-DHAVE_CONFIG_H"}
	if err := os.WriteFile(srcPath, []byte("int main(void) { return 2; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := helperConfigDigest(cfg, refDir, srcPath); got == base {
		t.Fatal("digest did not change when helper source changed")
	}
}
