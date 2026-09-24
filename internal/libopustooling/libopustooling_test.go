package libopustooling

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeFileInfo struct {
	mode os.FileMode
	dir  bool
}

func (f fakeFileInfo) Name() string       { return "stub" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

func TestFindLibopusToolForOSPrefersWindowsExe(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_compare.exe")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	got, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "windows")
	if !ok {
		t.Fatal("expected windows tool lookup to find .exe binary")
	}
	if got != toolPath {
		t.Fatalf("tool path mismatch: got %q want %q", got, toolPath)
	}
}

func TestLibopusBuildProvenanceForToolReadsStampedBuild(t *testing.T) {
	srcDir := t.TempDir()
	toolPath := filepath.Join(srcDir, "opus_demo")
	if err := os.WriteFile(toolPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	stamp := strings.Join([]string{
		"gopus libopus helper build v5",
		"version=" + DefaultVersion,
		"qext=0",
		"host_os=Linux",
		"host_arch=x86_64",
		"host_bits=64",
		"cc=cc",
		"cc_path=/usr/bin/cc",
		"cc_target=x86_64-linux-gnu",
		"cc_version=gcc test",
		"configure=--enable-static --disable-shared",
		"CFLAGS=-O3 -DNDEBUG",
		"CPPFLAGS=",
		"LDFLAGS=",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(stamp), 0o644); err != nil {
		t.Fatalf("write build stamp: %v", err)
	}

	got, ok := LibopusBuildProvenanceForTool(toolPath)
	if !ok {
		t.Fatal("expected stamped tool provenance")
	}
	if got.GOOS != runtime.GOOS || got.GOARCH != runtime.GOARCH {
		t.Fatalf("runtime target mismatch: got %s/%s want %s/%s", got.GOOS, got.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if got.LibopusVersion != DefaultVersion || got.QEXT != "0" {
		t.Fatalf("libopus identity mismatch: version=%q qext=%q", got.LibopusVersion, got.QEXT)
	}
	if got.HostOS != "Linux" || got.HostArch != "x86_64" || got.CCTarget != "x86_64-linux-gnu" || got.CCVersion != "gcc test" {
		t.Fatalf("stamp fields not propagated: %#v", got)
	}
	sum := sha256.Sum256([]byte(stamp))
	if got.LibopusBuildStampSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("stamp digest=%s want %s", got.LibopusBuildStampSHA256, hex.EncodeToString(sum[:]))
	}
}

func TestFindQEXTLibopusToolForOSUsesSeparateSourceTree(t *testing.T) {
	root := t.TempDir()
	defaultPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_demo.exe")
	qextPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion+"-qext", "opus_demo.exe")
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0o755); err != nil {
		t.Fatalf("mkdir default tool dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(qextPath), 0o755); err != nil {
		t.Fatalf("mkdir qext tool dir: %v", err)
	}
	if err := os.WriteFile(defaultPath, []byte("default"), 0o755); err != nil {
		t.Fatalf("write default tool: %v", err)
	}
	if err := os.WriteFile(qextPath, []byte("qext"), 0o755); err != nil {
		t.Fatalf("write qext tool: %v", err)
	}

	got, ok := findQEXTLibopusToolForOS(DefaultVersion, []string{root}, "opus_demo", "windows")
	if !ok {
		t.Fatal("expected qext tool lookup to find separate QEXT tree")
	}
	if got != qextPath {
		t.Fatalf("tool path mismatch: got %q want %q", got, qextPath)
	}
}

func TestFindLibopusToolForOSRejectsUnixFileWithoutExecBit(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_compare")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write tool: %v", err)
	}

	if _, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "linux"); ok {
		t.Fatal("expected unix tool lookup to reject non-executable file")
	}
}

func TestLibopusToolIsRunnableUsesPlatformSemantics(t *testing.T) {
	tests := []struct {
		name string
		info fakeFileInfo
		goos string
		want bool
	}{
		{
			name: "unix requires exec bit",
			info: fakeFileInfo{mode: 0o644},
			goos: "linux",
			want: false,
		},
		{
			name: "unix accepts exec bit",
			info: fakeFileInfo{mode: 0o755},
			goos: "linux",
			want: true,
		},
		{
			name: "windows ignores exec bit",
			info: fakeFileInfo{mode: 0o644},
			goos: "windows",
			want: true,
		},
		{
			name: "directories are never runnable",
			info: fakeFileInfo{mode: 0o755, dir: true},
			goos: "windows",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := libopusToolIsRunnable(tc.info, tc.goos); got != tc.want {
				t.Fatalf("runnable mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestResolveLibopusReferenceVariantBuildMatrix(t *testing.T) {
	tests := []struct {
		name     string
		goarch   string
		goSIMD   bool
		override string
		want     LibopusReferenceVariant
		wantErr  bool
	}{
		{name: "default scalar", goarch: "amd64", want: LibopusReferenceScalar},
		{name: "auto scalar", goarch: "arm64", override: " auto ", want: LibopusReferenceScalar},
		{name: "confirm scalar name", goarch: "amd64", override: "scalar", want: LibopusReferenceScalar},
		{name: "confirm scalar numeric", goarch: "amd64", override: "1", want: LibopusReferenceScalar},
		{name: "simd build", goarch: "amd64", goSIMD: true, want: LibopusReferenceSIMD},
		{name: "confirm simd name", goarch: "arm64", goSIMD: true, override: "simd", want: LibopusReferenceSIMD},
		{name: "confirm simd numeric", goarch: "arm64", goSIMD: true, override: "0", want: LibopusReferenceSIMD},
		{name: "reject scalar boolean alias", goarch: "amd64", override: "true", wantErr: true},
		{name: "reject simd boolean alias", goarch: "amd64", goSIMD: true, override: "false", wantErr: true},
		{name: "reject scalar on simd build", goarch: "amd64", goSIMD: true, override: "1", wantErr: true},
		{name: "reject simd on scalar build", goarch: "arm64", override: "0", wantErr: true},
		{name: "reject unsupported value", goarch: "amd64", override: "maybe", wantErr: true},
		{name: "simd tag on unsupported arch remains scalar", goarch: "386", goSIMD: true, want: LibopusReferenceScalar},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLibopusReferenceVariantFor(tc.goarch, tc.goSIMD, tc.override)
			if tc.wantErr {
				var configErr *LibopusReferenceConfigError
				if !errors.As(err, &configErr) {
					t.Fatalf("error=%v, want LibopusReferenceConfigError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve variant: %v", err)
			}
			if got != tc.want {
				t.Fatalf("variant=%q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveLibopusReferenceVariantMatchesBuildTags(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	want := LibopusReferenceScalar
	if goLibopusReferenceSIMD && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		want = LibopusReferenceSIMD
	}
	got, err := ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("variant=%q want %q for GOARCH=%s simd=%v", got, want, runtime.GOARCH, goLibopusReferenceSIMD)
	}
}

func TestScalarReferenceCompilerPolicyMatchesEnsureScript(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "tools", "ensure_libopus.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const exactFlags = "SCALAR_C_VECTOR_FLAGS=(-fno-tree-vectorize -fno-tree-slp-vectorize)"
	if !strings.Contains(string(script), exactFlags) {
		t.Fatalf("ensure_libopus.sh does not contain the scalar compiler policy %q", exactFlags)
	}
	if strings.Contains(LibopusScalarCFLAGS, "-ffp-contract=off") {
		t.Fatalf("scalar reference disables normal FMA contraction: %q", LibopusScalarCFLAGS)
	}
}

func TestValidateLibopusReferenceBuildAcceptsCustomScalarStamp(t *testing.T) {
	dir := writePairedReferenceTree(t, t.TempDir(), LibopusReferenceCustomScalar, runtime.GOOS, runtime.GOARCH)
	if err := ValidateLibopusReferenceBuild(dir, LibopusReferenceCustomScalar, DefaultVersion); err != nil {
		t.Fatal(err)
	}
}

func TestFindValidatedReferenceToolUsesExplicitTree(t *testing.T) {
	root := t.TempDir()
	variant := LibopusReferenceScalar
	srcDir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_compare")
	unsuffixed := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_compare")
	if err := os.MkdirAll(filepath.Dir(unsuffixed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unsuffixed, []byte("wrong tree"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findValidatedReferenceTool(DefaultVersion, []string{root}, "opus_compare", variant, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(srcDir, "opus_compare")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("tool=%q want explicit paired tree %q", got, want)
	}
}

func TestDefaultSearchRootsIncludeGitHubWorkspace(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "")
	t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "")
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	srcDir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_demo", "opus_compare")
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	t.Setenv("GITHUB_WORKSPACE", root)

	got, err := FindOrEnsureOpusCompare(DefaultVersion, DefaultSearchRoots())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(srcDir, "opus_compare")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("tool=%q want GITHUB_WORKSPACE paired tool %q", got, want)
	}
}

func TestValidateLibopusReferenceToolOverrideAcceptsSelectedTree(t *testing.T) {
	root := t.TempDir()
	variant := LibopusReferenceScalar
	dir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_demo")
	tool := filepath.Join(dir, "opus_demo")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	if err := ValidateLibopusReferenceToolOverride(tool, "opus_demo", variant, DefaultVersion); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWindowsExeOverrideIgnoresPOSIXExecBit(t *testing.T) {
	root := t.TempDir()
	variant := LibopusReferenceScalar
	dir := writePairedReferenceTree(t, root, variant, "windows", runtime.GOARCH, "opus_demo")
	tool := filepath.Join(dir, "opus_demo.exe")
	if err := os.Chmod(tool, 0o644); err != nil {
		t.Fatal(err)
	}
	// Exercise Windows file-mode semantics on this host; this does not claim the
	// synthetic executable can run natively.
	if err := validateLibopusReferenceToolOverrideForPlatform(tool, "opus_demo", variant, DefaultVersion, "windows", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}

func TestFindOrEnsureOpusCompareAcceptsStampedBuildWhenValidationCannotRun(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "")
	variant := LibopusReferenceScalar
	if goLibopusReferenceSIMD {
		variant = LibopusReferenceSIMD
	}
	root := t.TempDir()
	srcDir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_compare")
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findOrEnsureReferenceTool(DefaultVersion, []string{root}, "opus_compare", variant, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(srcDir, "opus_compare")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("tool=%q want %q", got, want)
	}
}

func TestFindOrEnsureOpusDemoRejectsExistingToolWhenValidationFails(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "")
	variant := LibopusReferenceScalar
	if goLibopusReferenceSIMD {
		variant = LibopusReferenceSIMD
	}
	root := t.TempDir()
	suffix, _ := LibopusReferenceSourceSuffix(variant)
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion+suffix)
	if err := os.MkdirAll(filepath.Join(srcDir, ".libs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "opus_demo"), []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".libs", "libopus.a"), []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findOrEnsureReferenceTool(DefaultVersion, []string{root}, "opus_demo", variant, runtime.GOOS, runtime.GOARCH)
	var configErr *LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error=%v, want invalid paired-reference configuration", err)
	}
}

func TestFindOrEnsureOpusCompareRejectsStampedBuildWithForeignFlags(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "")
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	srcDir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_compare")
	stampPath := filepath.Join(srcDir, ".gopus-libopus-build")
	stamp, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := parseLibopusBuildStamp(string(stamp))
	if !ok {
		t.Fatal("test stamp did not parse")
	}
	if fields["CFLAGS"] == "-O0" {
		t.Fatal("test stamp unexpectedly already has foreign flags")
	}
	updated := strings.Replace(string(stamp), "CFLAGS="+fields["CFLAGS"], "CFLAGS=-O0", 1)
	if err := os.WriteFile(stampPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = findOrEnsureReferenceTool(DefaultVersion, []string{root}, "opus_compare", variant, runtime.GOOS, runtime.GOARCH)
	var configErr *LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error=%v, want LibopusReferenceConfigError for foreign compiler flags", err)
	}
}

func TestFindOrEnsureOpusDemoValidatesBeforeReturningExistingTool(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "")
	t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "")
	if runtime.GOOS == "windows" {
		t.Skip("ensure shell setup is Unix-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no shell available for ensure script")
		}
	}
	variant, err := ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	templateRoot := t.TempDir()
	templateDir := writePairedReferenceTree(t, templateRoot, variant, runtime.GOOS, runtime.GOARCH, "opus_demo")
	suffix, err := LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion+suffix)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleTool := filepath.Join(targetDir, "opus_demo")
	if err := os.WriteFile(staleTool, []byte("stale but executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	markerPath := filepath.Join(root, "ensure-ran")
	t.Setenv("GOPUS_TEST_ENSURE_MARKER", markerPath)
	t.Setenv("GOPUS_TEST_PAIRED_TEMPLATE", templateDir)
	t.Setenv("GOPUS_TEST_PAIRED_TARGET", targetDir)
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nmkdir -p \"$GOPUS_TEST_PAIRED_TARGET\"\ncp -R \"$GOPUS_TEST_PAIRED_TEMPLATE/.\" \"$GOPUS_TEST_PAIRED_TARGET/\"\nprintf '%s' \"$LIBOPUS_VERSION\" > \"$GOPUS_TEST_ENSURE_MARKER\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindOrEnsureOpusDemo(DefaultVersion, []string{root})
	if err != nil {
		t.Fatalf("find or ensure paired opus_demo: %v", err)
	}
	want := filepath.Join(targetDir, "opus_demo")
	if got != want {
		t.Fatalf("tool=%q want validated paired tool %q", got, want)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("ensure script did not run before returning the existing tool: %v", err)
	}
	if string(marker) != DefaultVersion {
		t.Fatalf("ensure script LIBOPUS_VERSION=%q want %q", string(marker), DefaultVersion)
	}
}

func TestValidateLibopusReferenceBuildRejectsMismatchedArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "foreign compiler target", mutate: func(t *testing.T, dir string) {
			p := filepath.Join(dir, ".gopus-libopus-build")
			stamp, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			foreignArch := "x86_64"
			if runtime.GOARCH == "amd64" {
				foreignArch = "aarch64"
			}
			updated := strings.Replace(string(stamp), "cc_target="+testTargetTriple(runtime.GOOS, runtime.GOARCH), "cc_target="+testTargetTriple(runtime.GOOS, foreignArch), 1)
			if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "simd macro in scalar config", mutate: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "config.h"), []byte("#define OPUS_HAVE_RTCD 1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing archive", mutate: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, ".libs", "libopus.a")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := writePairedReferenceTree(t, root, LibopusReferenceScalar, runtime.GOOS, runtime.GOARCH)
			tc.mutate(t, dir)
			err := ValidateLibopusReferenceBuild(dir, LibopusReferenceScalar, DefaultVersion)
			var configErr *LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("error=%v, want LibopusReferenceConfigError", err)
			}
		})
	}
}

func TestValidateLibopusConfigSIMDRequiresMatchingInstructionMacro(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		arch   string
	}{
		{name: "RTCD without instructions", config: "#define OPUS_HAVE_RTCD 1\n", arch: "amd64"},
		{name: "foreign architecture macro", config: "#define OPUS_X86_MAY_HAVE_SSE2 1\n", arch: "arm64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateLibopusConfigSIMD(tc.config, LibopusReferenceSIMD, tc.arch); err == nil {
				t.Fatal("SIMD config without the matching architecture instruction macro was accepted")
			}
		})
	}
}

func TestValidateLibopusReferenceArchiveRejectsWrongVariantAndUnstampedOverride(t *testing.T) {
	root := t.TempDir()
	scalarDir := writePairedReferenceTree(t, root, LibopusReferenceScalar, runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(scalarDir, ".libs", "libopus.a")
	if err := ValidateLibopusReferenceArchive(archive, LibopusReferenceScalar, DefaultVersion); err != nil {
		t.Fatal(err)
	}
	var configErr *LibopusReferenceConfigError
	if err := ValidateLibopusReferenceArchive(archive, LibopusReferenceSIMD, DefaultVersion); !errors.As(err, &configErr) {
		t.Fatalf("wrong-variant error=%v, want LibopusReferenceConfigError", err)
	}
	if err := ValidateLibopusReferenceArchive(filepath.Join(root, "libopus.a"), LibopusReferenceScalar, DefaultVersion); !errors.As(err, &configErr) {
		t.Fatalf("unrooted archive error=%v, want LibopusReferenceConfigError", err)
	}
}

func writePairedReferenceTree(t *testing.T, root string, variant LibopusReferenceVariant, goos, goarch string, tools ...string) string {
	t.Helper()
	suffix, err := LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion+suffix)
	if err := os.MkdirAll(filepath.Join(srcDir, ".libs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		name := tool
		if goos == "windows" && !strings.HasSuffix(name, ".exe") {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte("stub"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".libs", "libopus.a"), []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := ""
	configure := "--enable-static --disable-shared --disable-asm --disable-rtcd --disable-intrinsics"
	cflags := LibopusScalarCFLAGS
	custom := "0"
	switch variant {
	case LibopusReferenceScalar:
	case LibopusReferenceSIMD:
		config = testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
	case LibopusReferenceCustomScalar:
		configure = "--enable-static --disable-shared --enable-custom-modes --disable-asm --disable-rtcd --disable-intrinsics"
		custom = "1"
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config.h"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := strings.Join([]string{
		"gopus libopus helper build v5",
		"version=" + DefaultVersion,
		"qext=0",
		"fixed=0",
		"custom=" + custom,
		"host_os=" + testHostOS(goos),
		"host_arch=" + testHostArch(goarch),
		"host_bits=" + testHostBits(goarch),
		"cc=cc",
		"cc_path=/usr/bin/cc",
		"cc_target=" + testTargetTriple(goos, goarch),
		"cc_version=cc test",
		"configure=" + configure,
		"CFLAGS=" + cflags,
		"CPPFLAGS=",
		"LDFLAGS=",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

func testSIMDConfig(goarch string) string {
	switch goarch {
	case "amd64":
		return "#define OPUS_X86_MAY_HAVE_SSE2 1\n#define OPUS_HAVE_RTCD 1\n"
	case "arm64":
		return "#define OPUS_ARM_MAY_HAVE_NEON_INTR 1\n#define OPUS_HAVE_RTCD 1\n"
	default:
		return ""
	}
}

func testHostOS(goos string) string {
	switch goos {
	case "darwin":
		return "Darwin"
	case "linux":
		return "Linux"
	case "windows":
		return "MINGW64_NT-10.0"
	default:
		return goos
	}
}

func testHostArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return goarch
	}
}

func testHostBits(goarch string) string {
	if goarch == "386" || goarch == "arm" {
		return "32"
	}
	return "64"
}

func testTargetTriple(goos, goarch string) string {
	arch := testHostArch(goarch)
	switch goos {
	case "darwin":
		return arch + "-apple-darwin24.0.0"
	case "linux":
		return arch + "-unknown-linux-gnu"
	case "windows":
		return arch + "-w64-mingw32"
	default:
		return arch + "-" + goos
	}
}

func TestStampedLibopusBuildFallbackRejectsWrongPlatformOrArch(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion)
	if err := os.MkdirAll(filepath.Join(srcDir, ".libs"), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	for _, tool := range []string{"opus_demo.exe", "opus_compare.exe"} {
		toolPath := filepath.Join(srcDir, tool)
		if err := os.WriteFile(toolPath, []byte("stub"), 0o755); err != nil {
			t.Fatalf("write %s: %v", tool, err)
		}
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".libs", "libopus.a"), []byte("archive"), 0o644); err != nil {
		t.Fatalf("write libopus archive: %v", err)
	}
	stamp := strings.Join([]string{
		"gopus libopus helper build v5",
		"version=" + DefaultVersion,
		"qext=0",
		"host_os=MINGW64_NT-10.0",
		"host_arch=x86_64",
		"host_bits=64",
		"cc=gcc",
		"cc_path=/usr/bin/gcc",
		"cc_target=x86_64-w64-mingw32",
		"cc_version=gcc test",
		"configure=--enable-static --disable-shared",
		"CFLAGS=-O3 -DNDEBUG",
		"CPPFLAGS=",
		"LDFLAGS=",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(stamp), 0o644); err != nil {
		t.Fatalf("write build stamp: %v", err)
	}

	if stampedLibopusBuildPresentForPlatform(DefaultVersion, []string{root}, false, "linux", "amd64") {
		t.Fatal("expected non-windows fallback to be rejected")
	}
	if stampedLibopusBuildPresentForPlatform(DefaultVersion, []string{root}, false, "windows", "arm64") {
		t.Fatal("expected wrong-arch fallback to be rejected")
	}
}

func TestScalarDNNBuildEnvPinsCompilerAndClearsUnsafeOverrides(t *testing.T) {
	t.Setenv("CC", "/tmp/not-the-compiler")
	t.Setenv("CFLAGS", "bad-cflags")
	t.Setenv("CPPFLAGS", "bad-cppflags")
	t.Setenv("LDFLAGS", "bad-ldflags")

	env, err := ScalarDNNBuildEnv()
	if err != nil {
		t.Skipf("no local C compiler available: %v", err)
	}
	values := envMap(env)
	if values["CC"] == "" || values["CC"] == "/tmp/not-the-compiler" {
		t.Fatalf("CC override was not replaced: %q", values["CC"])
	}
	if values["CFLAGS"] != ScalarDNNBuildCFLAGS {
		t.Fatalf("CFLAGS=%q want scalar flags", values["CFLAGS"])
	}
	if values["CPPFLAGS"] != "" {
		t.Fatalf("CPPFLAGS=%q want empty", values["CPPFLAGS"])
	}
	if values["LDFLAGS"] != "" {
		t.Fatalf("LDFLAGS=%q want empty", values["LDFLAGS"])
	}
}

func TestScalarDNNBuildStampIncludesNativeCompilerIdentity(t *testing.T) {
	stamp, err := scalarDNNBuildStamp(ScalarDNNBuildCFLAGS)
	if err != nil {
		t.Skipf("no local C compiler available: %v", err)
	}
	for _, want := range []string{
		"gopus scalar libopus DNN helper build v4\n",
		"GOOS=",
		"GOARCH=",
		"CC=",
		"CC_TARGET=",
		"CC_VERSION=",
		"CFLAGS=" + ScalarDNNBuildCFLAGS,
		"CPPFLAGS=\n",
		"LDFLAGS=\n",
	} {
		if !strings.Contains(stamp, want) {
			t.Fatalf("stamp missing %q:\n%s", want, stamp)
		}
	}
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if ok {
			out[name] = value
		}
	}
	return out
}
