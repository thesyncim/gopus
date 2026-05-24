package libopustooling

import (
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := libopusToolIsRunnable(tc.info, tc.goos); got != tc.want {
				t.Fatalf("runnable mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFindOrEnsureOpusDemoValidatesBeforeReturningExistingTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell validation hook is Unix-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no shell available for ensure script")
		}
	}

	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_demo")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stale but executable"), 0o755); err != nil {
		t.Fatalf("write stale tool: %v", err)
	}

	markerPath := filepath.Join(root, "ensure-ran")
	t.Setenv("GOPUS_TEST_ENSURE_MARKER", markerPath)
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	script := "#!/bin/sh\nprintf '%s' \"$LIBOPUS_VERSION\" > \"$GOPUS_TEST_ENSURE_MARKER\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write ensure script: %v", err)
	}

	got, ok := FindOrEnsureOpusDemo(DefaultVersion, []string{root})
	if !ok {
		t.Fatal("expected FindOrEnsureOpusDemo to return existing tool after validation")
	}
	if got != toolPath {
		t.Fatalf("tool path mismatch: got %q want %q", got, toolPath)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("ensure script did not run before returning existing tool: %v", err)
	}
	if string(marker) != DefaultVersion {
		t.Fatalf("ensure script LIBOPUS_VERSION=%q want %q", string(marker), DefaultVersion)
	}
}

func TestFindOrEnsureOpusDemoRejectsExistingToolWhenValidationFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell validation hook is Unix-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("no shell available for ensure script")
		}
	}

	root := t.TempDir()
	toolPath := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion, "opus_demo")
	if err := os.MkdirAll(filepath.Dir(toolPath), 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(toolPath, []byte("stale but executable"), 0o755); err != nil {
		t.Fatalf("write stale tool: %v", err)
	}

	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatalf("write failing ensure script: %v", err)
	}

	if got, ok := FindOrEnsureOpusDemo(DefaultVersion, []string{root}); ok {
		t.Fatalf("FindOrEnsureOpusDemo returned stale tool %q after validation failure", got)
	}
}

func TestFindOrEnsureOpusCompareAcceptsStampedBuildWhenValidationCannotRun(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion)
	if err := os.MkdirAll(filepath.Join(srcDir, ".libs"), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	for _, tool := range []string{"opus_demo", "opus_compare", "opus_demo.exe", "opus_compare.exe"} {
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

	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatalf("write failing ensure script: %v", err)
	}

	if !stampedLibopusBuildPresentForPlatform(DefaultVersion, []string{root}, false, "windows", "amd64") {
		t.Fatal("expected stamped build fallback to allow opus_compare discovery")
	}
	got, ok := findLibopusToolForOS(DefaultVersion, []string{root}, "opus_compare", "windows")
	if !ok {
		t.Fatal("expected windows opus_compare discovery")
	}
	if !strings.Contains(filepath.Base(got), "opus_compare") {
		t.Fatalf("got %q, want opus_compare tool", got)
	}
}

func TestFindOrEnsureOpusCompareRejectsStampedBuildWithForeignFlags(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion)
	if err := os.MkdirAll(filepath.Join(srcDir, ".libs"), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	for _, tool := range []string{"opus_demo", "opus_compare", "opus_demo.exe", "opus_compare.exe"} {
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
		"CFLAGS=-O0",
		"CPPFLAGS=",
		"LDFLAGS=",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(stamp), 0o644); err != nil {
		t.Fatalf("write build stamp: %v", err)
	}
	scriptPath := filepath.Join(root, "tools", "ensure_libopus.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir tools dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 17\n"), 0o755); err != nil {
		t.Fatalf("write failing ensure script: %v", err)
	}

	if stampedLibopusBuildPresentForPlatform(DefaultVersion, []string{root}, false, "windows", "amd64") {
		t.Fatal("expected foreign stamped build to be rejected")
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
