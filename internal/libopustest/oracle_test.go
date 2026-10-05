package libopustest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
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

func TestHelperCCompileFlagsUseCompilerDefaultDialectAndContraction(t *testing.T) {
	for _, scalarRef := range []bool{false, true} {
		flags := helperCCompileFlags(CHelperConfig{
			CFlags:    []string{"-DHAVE_CONFIG_H"},
			DeadStrip: true,
		}, scalarRef)
		for _, flag := range flags {
			if strings.HasPrefix(flag, "-std=") || strings.HasPrefix(flag, "-ffp-contract=") {
				t.Errorf("scalarRef=%t helper flag %q overrides the compiler's default reference policy; flags=%v", scalarRef, flag, flags)
			}
		}
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

func TestHelperRefDirSelectsQEXTTree(t *testing.T) {
	defaultDir := helperRefDir(CHelperConfig{}, libopustooling.LibopusReferenceScalar)
	qextScalarDir := helperRefDir(CHelperConfig{QEXTRef: true}, libopustooling.LibopusReferenceScalar)
	if qextScalarDir == defaultDir {
		t.Fatal("QEXT helper ref dir did not switch trees")
	}
	if filepath.Base(qextScalarDir) != "opus-1.6.1-qext-scalar" {
		t.Fatalf("QEXT scalar helper ref dir=%q", qextScalarDir)
	}
	qextSIMDDir := helperRefDir(CHelperConfig{QEXTRef: true}, libopustooling.LibopusReferenceSIMD)
	if filepath.Base(qextSIMDDir) != "opus-1.6.1-qext-simd" {
		t.Fatalf("QEXT SIMD helper ref dir=%q", qextSIMDDir)
	}
}

func TestHelperRefDirSelectsFixedQEXTTree(t *testing.T) {
	defaultDir := helperRefDir(CHelperConfig{}, libopustooling.LibopusReferenceScalar)
	combinedScalarDir := helperRefDir(CHelperConfig{FixedQEXTRef: true}, libopustooling.LibopusReferenceScalar)
	if combinedScalarDir == defaultDir || filepath.Base(combinedScalarDir) != "opus-1.6.1-fixed-qext-scalar" {
		t.Fatalf("fixed-QEXT scalar helper ref dir=%q", combinedScalarDir)
	}
	combinedSIMDDir := helperRefDir(CHelperConfig{FixedQEXTRef: true}, libopustooling.LibopusReferenceSIMD)
	if filepath.Base(combinedSIMDDir) != "opus-1.6.1-fixed-qext-simd" {
		t.Fatalf("fixed-QEXT SIMD helper ref dir=%q", combinedSIMDDir)
	}
}

func TestHelperRefDirSelectsCustomCombinedTrees(t *testing.T) {
	for _, tc := range []struct {
		cfg                CHelperConfig
		scalar, simd       libopustooling.LibopusReferenceVariant
		scalarDir, simdDir string
	}{
		{CHelperConfig{CustomQEXTRef: true}, libopustooling.LibopusReferenceCustomQEXTScalar, libopustooling.LibopusReferenceCustomQEXTSIMD, "opus-1.6.1-custom-qext-scalar", "opus-1.6.1-custom-qext-simd"},
		{CHelperConfig{CustomFixedRef: true}, libopustooling.LibopusReferenceCustomFixedScalar, libopustooling.LibopusReferenceCustomFixedSIMD, "opus-1.6.1-custom-fixed-scalar", "opus-1.6.1-custom-fixed-simd"},
		{CHelperConfig{CustomFixedQEXTRef: true}, libopustooling.LibopusReferenceCustomFixedQEXTScalar, libopustooling.LibopusReferenceCustomFixedQEXTSIMD, "opus-1.6.1-custom-fixed-qext-scalar", "opus-1.6.1-custom-fixed-qext-simd"},
	} {
		if err := validateCHelperReferenceSelection(tc.cfg); err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(helperRefDir(tc.cfg, tc.scalar)); got != tc.scalarDir {
			t.Errorf("scalar reference directory=%q, want %q", got, tc.scalarDir)
		}
		if got := filepath.Base(helperRefDir(tc.cfg, tc.simd)); got != tc.simdDir {
			t.Errorf("SIMD reference directory=%q, want %q", got, tc.simdDir)
		}
	}
	for _, cfg := range []CHelperConfig{
		{CustomQEXTRef: true, CustomRef: true},
		{CustomFixedRef: true, FixedRef: true},
		{CustomQEXTRef: true, CustomFixedRef: true},
		{CustomFixedQEXTRef: true, CustomFixedRef: true},
		{CustomQEXTRef: true, ForceScalarRef: true},
	} {
		if err := validateCHelperReferenceSelection(cfg); err == nil {
			t.Fatalf("accepted conflicting reference selectors: %+v", cfg)
		}
	}
}

func TestHelperRefDirSelectsScalarTreeWhenRequested(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "1")
	defaultDir := helperRefDir(CHelperConfig{}, libopustooling.LibopusReferenceScalar)
	if filepath.Base(defaultDir) != "opus-1.6.1-scalar" {
		t.Fatalf("default helper ref dir under scalar mode=%q want opus-1.6.1-scalar", defaultDir)
	}
	customDir := helperRefDir(CHelperConfig{CustomRef: true}, libopustooling.LibopusReferenceScalar)
	if filepath.Base(customDir) != "opus-1.6.1-custom-scalar" {
		t.Fatalf("custom helper ref dir under scalar mode=%q want opus-1.6.1-custom-scalar", customDir)
	}
	// A test that explicitly requests the matching SIMD kernel keeps its SIMD
	// reference tree, independent of the helper's paired scalar default.
	simdDir := helperRefDir(CHelperConfig{SIMDRef: true}, libopustooling.LibopusReferenceScalar)
	if filepath.Base(simdDir) != "opus-1.6.1-simd" {
		t.Fatalf("SIMD helper ref dir under scalar mode=%q want opus-1.6.1-simd", simdDir)
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
	stampPath := filepath.Join(refDir, ".gopus-libopus-build")
	if err := os.WriteFile(stampPath, []byte("CFLAGS=-O3 -DNDEBUG\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := CHelperConfig{
		OutputBase: "gopus_helper",
		SourceFile: "helper.c",
		CFlags:     []string{"-DHAVE_CONFIG_H"},
		RefSources: []string{"silk/ref.c"},
	}
	compiler := cHelperCompiler{path: "cc", target: "test-target", version: "test compiler 1"}
	digest := func() string {
		compileArgs := helperCCompileArgs(cfg, refDir, false)
		sourcePaths := helperCSourcePaths(cfg, refDir, srcPath)
		return helperConfigDigest(cfg, refDir, srcPath, false, compileArgs, sourcePaths, compiler, []byte("preprocessed helper sources"))
	}
	base := digest()
	if got := helperConfigDigest(cfg, refDir, srcPath, true, helperCCompileArgs(cfg, refDir, true), helperCSourcePaths(cfg, refDir, srcPath), compiler, []byte("preprocessed helper sources")); got == base {
		t.Fatal("digest did not change when scalar reference compile flags changed")
	}
	if got := helperConfigDigest(cfg, refDir, srcPath, false, helperCCompileArgs(cfg, refDir, false), helperCSourcePaths(cfg, refDir, srcPath), cHelperCompiler{path: "cc", target: "test-target", version: "test compiler 2"}, []byte("preprocessed helper sources")); got == base {
		t.Fatal("digest did not change when compiler version changed")
	}
	if err := os.WriteFile(stampPath, []byte("CFLAGS=-O3 -DNDEBUG\ncc=clang\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := digest(); got == base {
		t.Fatal("digest did not change when libopus build stamp changed")
	}
	base = digest()
	cfg.CFlags = append(cfg.CFlags, "-DNDEBUG")
	if got := digest(); got == base {
		t.Fatal("digest did not change when C flags changed")
	}
	cfg.CFlags = []string{"-DHAVE_CONFIG_H"}
	base = digest()
	if err := os.WriteFile(srcPath, []byte("int main(void) { return 2; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := digest(); got == base {
		t.Fatal("digest did not change when helper source changed")
	}
	base = digest()
	cfg.QEXTRef = true
	if got := digest(); got == base {
		t.Fatal("digest did not change when QEXT reference tree changed")
	}
	cfg.QEXTRef = false
	base = digest()
	cfg.FixedQEXTRef = true
	if got := digest(); got == base {
		t.Fatal("digest did not change when fixed-QEXT reference tree changed")
	}
}

func TestHelperConfigDigestTracksPreprocessedHeadersAndArchives(t *testing.T) {
	cc, err := libopustooling.FindCCompiler()
	if err != nil {
		HelperUnavailable(t, "helper cache digest", err)
		return
	}
	compiler, err := identifyCHelperCompiler(cc)
	if err != nil {
		HelperUnavailable(t, "helper cache digest compiler identity", err)
		return
	}

	root := t.TempDir()
	helperDir := filepath.Join(root, "helper inputs")
	refDir := filepath.Join(root, "reference")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperHeader := filepath.Join(helperDir, "helper header.h")
	refHeader := filepath.Join(refDir, "reference header.h")
	srcPath := filepath.Join(helperDir, "helper source.c")
	refSource := filepath.Join(refDir, "reference source.c")
	configPath := filepath.Join(refDir, "config.h")
	stampPath := filepath.Join(refDir, ".gopus-libopus-build")
	archivePath := filepath.Join(root, "libopus.a")
	for path, contents := range map[string]string{
		helperHeader: "#define HELPER_VALUE 1\n",
		refHeader:    "#define REFERENCE_VALUE 2\n",
		srcPath:      "#include \"helper header.h\"\n#include <stddef.h>\nint main(void) { return HELPER_VALUE + sizeof(size_t); }\n",
		refSource:    "#include \"reference header.h\"\nint reference(void) { return REFERENCE_VALUE; }\n",
		configPath:   "#define OPUS_VERSION \"test\"\n",
		stampPath:    "CFLAGS=-O3\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(archivePath, []byte("archive one"), 0o644); err != nil {
		t.Fatal(err)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeArchive, err := filepath.Rel(workingDir, archivePath)
	if err != nil {
		t.Fatal(err)
	}

	cfg := CHelperConfig{
		OutputBase:  "gopus_dependency_helper",
		SourceFile:  "helper.c",
		IncludeDirs: []string{helperDir},
		RefSources:  []string{"reference source.c"},
		Libs:        []string{relativeArchive},
	}
	digest := func() string {
		t.Helper()
		compileArgs := helperCCompileArgs(cfg, refDir, false)
		sourcePaths := helperCSourcePaths(cfg, refDir, srcPath)
		preprocessed, err := helperPreprocessedSource(cc, compileArgs, sourcePaths)
		if err != nil {
			t.Fatalf("preprocess test helper: %v", err)
		}
		if !strings.Contains(string(preprocessed), "stddef.h") {
			t.Fatal("preprocessed input omits the system header dependency")
		}
		return helperConfigDigest(cfg, refDir, srcPath, false, compileArgs, sourcePaths, compiler, preprocessed)
	}
	base := digest()
	if err := os.WriteFile(helperHeader, []byte("#define HELPER_VALUE 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if digest() == base {
		t.Fatal("digest did not change when a helper-local header changed")
	}
	base = digest()
	if err := os.WriteFile(refHeader, []byte("#define REFERENCE_VALUE 4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if digest() == base {
		t.Fatal("digest did not change when a libopus reference header changed")
	}
	base = digest()
	if err := os.WriteFile(archivePath, []byte("archive two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if digest() == base {
		t.Fatal("digest did not change when a linked archive changed")
	}
	base = digest()
	cfg.CFlags = []string{"-DHELPER_BUILD_VARIANT=1"}
	if digest() == base {
		t.Fatal("digest did not change when a compile flag changed")
	}
	if helperBuildFlagsAllowCache(CHelperConfig{LDFlags: []string{"-Wl,-Map,/tmp/helper.map"}}) {
		t.Fatal("linker output flags must bypass the cached build")
	}
	if helperBuildFlagsAllowCache(CHelperConfig{LDFlags: []string{"-Wl,--wrap=opus_encode_float"}}) {
		t.Fatal("custom linker flags must bypass the cached build")
	}
	if helperBuildFlagsAllowCache(CHelperConfig{CFlags: []string{"-ohelper"}}) {
		t.Fatal("joined output flags must bypass the cached build")
	}
	if helperBuildFlagsAllowCache(CHelperConfig{Libs: []string{"-Wl,-Map,/tmp/helper.map"}}) {
		t.Fatal("linker output options in Libs must bypass the cached build")
	}
	if helperBuildFlagsAllowCache(CHelperConfig{Libs: []string{"-lcustom"}}) {
		t.Fatal("untracked library options in Libs must bypass the cached build")
	}
	if !helperBuildFlagsAllowCache(CHelperConfig{Libs: []string{"/tmp/libopus.a", "-lm", "-ldl", "-lpthread"}}) {
		t.Fatal("explicit files and supported system libraries should allow cache reuse")
	}
}

func TestHelperRejectsArchiveFromDifferentHeaderTree(t *testing.T) {
	refDir := filepath.Join(t.TempDir(), "opus-1.6.1-scalar")
	otherDir := filepath.Join(t.TempDir(), "opus-1.6.1-simd")
	err := validateHelperReferenceArchives(
		[]string{filepath.Join(otherDir, ".libs", "libopus.a"), "-lm"},
		refDir,
		libopustooling.LibopusReferenceScalar,
	)
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error=%v, want LibopusReferenceConfigError", err)
	}
}
