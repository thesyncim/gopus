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

func TestLibopusAMD64TargetSelectionAndCompilerFlags(t *testing.T) {
	tests := []struct {
		target     string
		wantSuffix string
		wantCFlags []string
	}{
		{target: "v1", wantSuffix: "-amd64-v1-scalar", wantCFlags: []string{"-march=x86-64", "-mtune=generic"}},
		{target: "v2", wantSuffix: "-amd64-v2-scalar", wantCFlags: []string{"-march=x86-64-v2", "-mtune=generic"}},
		{target: "v3", wantSuffix: "-amd64-v3-scalar", wantCFlags: []string{"-march=x86-64-v3", "-mtune=generic"}},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			got, err := resolveLibopusAMD64TargetForPlatform(tc.target, "amd64")
			if err != nil || got != tc.target {
				t.Fatalf("target=%q err=%v, want %q", got, err, tc.target)
			}
			suffix, err := libopusReferenceSourceSuffixForTarget(LibopusReferenceScalar, got)
			if err != nil || suffix != tc.wantSuffix {
				t.Fatalf("suffix=%q err=%v, want %q", suffix, err, tc.wantSuffix)
			}
			flags, err := amd64TargetCFlags(got)
			if err != nil || strings.Join(flags, " ") != strings.Join(tc.wantCFlags, " ") {
				t.Fatalf("CFLAGS=%v err=%v, want %v", flags, err, tc.wantCFlags)
			}
			simdSuffix, err := libopusReferenceSourceSuffixForTarget(LibopusReferenceSIMD, got)
			if err != nil || simdSuffix != strings.TrimSuffix(tc.wantSuffix, "-scalar")+"-simd" {
				t.Fatalf("SIMD suffix=%q err=%v", simdSuffix, err)
			}
		})
	}
	if suffix, err := libopusReferenceSourceSuffixForTarget(LibopusReferenceScalar, ""); err != nil || suffix != "-scalar" {
		t.Fatalf("unset target suffix=%q err=%v, want legacy -scalar", suffix, err)
	}
}

func TestLibopusAMD64TargetRejectsInvalidArchitectureAndFeatureVariants(t *testing.T) {
	for _, tc := range []struct {
		value  string
		goarch string
	}{
		{value: "v4", goarch: "amd64"},
		{value: "v2", goarch: "arm64"},
	} {
		if _, err := resolveLibopusAMD64TargetForPlatform(tc.value, tc.goarch); err == nil {
			t.Errorf("accepted target %q for GOARCH=%s", tc.value, tc.goarch)
		} else {
			var configErr *LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Errorf("error=%T %v, want LibopusReferenceConfigError", err, err)
			}
		}
	}
	for _, variant := range []LibopusReferenceVariant{
		LibopusReferenceQEXTScalar,
		LibopusReferenceFixedScalar,
		LibopusReferenceCustomScalar,
		LibopusReferenceDREDQEXTScalar,
	} {
		if _, err := libopusReferenceSourceSuffixForTarget(variant, "v2"); err == nil {
			t.Errorf("accepted target v2 with optional variant %q", variant)
		} else {
			var configErr *LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Errorf("variant %q error=%T %v, want LibopusReferenceConfigError", variant, err, err)
			}
		}
	}
}

func TestLibopusAMD64TargetMustMatchCompiledGoLevel(t *testing.T) {
	for _, target := range []string{"v1", "v2", "v3"} {
		for _, compiled := range []string{"v1", "v2", "v3"} {
			got, err := resolveLibopusAMD64TargetForBuild(target, "amd64", compiled)
			if target == compiled {
				if err != nil || got != target {
					t.Errorf("target=%s compiled=%s resolved=%q err=%v", target, compiled, got, err)
				}
				continue
			}
			var configErr *LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Errorf("target=%s compiled=%s error=%T %v, want LibopusReferenceConfigError", target, compiled, err, err)
			}
		}
	}
	if _, err := resolveLibopusAMD64TargetForBuild("v3", "amd64", "v4"); err == nil {
		t.Fatal("accepted v3 reference target for a Go v4 binary")
	}
	if target, err := resolveLibopusAMD64TargetForBuild("", "amd64", "v4"); err != nil || target != "" {
		t.Fatalf("unset opt-in target=%q err=%v, want empty success", target, err)
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

func TestResolveLibopusQEXTReferenceVariantMatchesBuildTags(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	want := LibopusReferenceQEXTScalar
	if goLibopusReferenceSIMD && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		want = LibopusReferenceQEXTSIMD
	}
	got, err := ResolveLibopusQEXTReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("QEXT variant=%q want %q", got, want)
	}
}

func TestResolveLibopusFixedQEXTReferenceVariantMatchesBuildTags(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	want := LibopusReferenceFixedQEXTScalar
	if goLibopusReferenceSIMD && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		want = LibopusReferenceFixedQEXTSIMD
	}
	got, err := ResolveLibopusFixedQEXTReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fixed QEXT variant=%q want %q", got, want)
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

func TestValidateCustomReferenceBuildRequiresFeatureAndPairedISA(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		for _, variant := range []LibopusReferenceVariant{LibopusReferenceCustomScalar, LibopusReferenceCustomSIMD} {
			t.Run(arch+"/"+string(variant), func(t *testing.T) {
				dir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", arch)
				validate := func(v LibopusReferenceVariant) error {
					return validateLibopusReferenceBuildForPlatform(dir, v, DefaultVersion, "linux", arch)
				}
				if err := validate(variant); err != nil {
					t.Fatal(err)
				}
				other := LibopusReferenceCustomScalar
				if variant == other {
					other = LibopusReferenceCustomSIMD
				}
				if err := validate(other); err == nil {
					t.Fatal("accepted the opposite instruction lane")
				}
				configPath := filepath.Join(dir, "config.h")
				config, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, []byte(strings.ReplaceAll(string(config), "#define CUSTOM_MODES 1\n", "")), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(variant); err == nil {
					t.Fatal("accepted a reference without CUSTOM_MODES")
				}
				wrongISA := "#define CUSTOM_MODES 1\n"
				if variant == LibopusReferenceCustomScalar {
					wrongISA += testSIMDConfig(arch)
				}
				if err := os.WriteFile(configPath, []byte(wrongISA), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(variant); err == nil {
					t.Fatal("accepted config macros from the opposite instruction lane")
				}
			})
		}
	}
}

func TestValidateCustomCombinedReferenceRequiresEveryFeatureAndPairedISA(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		for _, tc := range []struct {
			variant, opposite, subset LibopusReferenceVariant
			featureMacro, stampField  string
		}{
			{LibopusReferenceCustomQEXTScalar, LibopusReferenceCustomQEXTSIMD, LibopusReferenceCustomScalar, "#define ENABLE_QEXT 1\n", "qext=1\n"},
			{LibopusReferenceCustomQEXTSIMD, LibopusReferenceCustomQEXTScalar, LibopusReferenceCustomSIMD, "#define ENABLE_QEXT 1\n", "qext=1\n"},
			{LibopusReferenceCustomFixedScalar, LibopusReferenceCustomFixedSIMD, LibopusReferenceCustomScalar, "#define FIXED_POINT 1\n", "fixed=1\n"},
			{LibopusReferenceCustomFixedSIMD, LibopusReferenceCustomFixedScalar, LibopusReferenceCustomSIMD, "#define FIXED_POINT 1\n", "fixed=1\n"},
			{LibopusReferenceCustomFixedQEXTScalar, LibopusReferenceCustomFixedQEXTSIMD, LibopusReferenceCustomFixedScalar, "#define ENABLE_QEXT 1\n", "qext=1\n"},
			{LibopusReferenceCustomFixedQEXTSIMD, LibopusReferenceCustomFixedQEXTScalar, LibopusReferenceCustomFixedSIMD, "#define ENABLE_QEXT 1\n", "qext=1\n"},
		} {
			t.Run(arch+"/"+string(tc.variant), func(t *testing.T) {
				dir := writePairedReferenceTree(t, t.TempDir(), tc.variant, "linux", arch)
				validate := func(v LibopusReferenceVariant) error {
					return validateLibopusReferenceBuildForPlatform(dir, v, DefaultVersion, "linux", arch)
				}
				if err := validate(tc.variant); err != nil {
					t.Fatal(err)
				}
				if err := validate(tc.opposite); err == nil {
					t.Fatal("accepted the opposite instruction lane")
				}
				if err := validate(tc.subset); err == nil {
					t.Fatal("accepted the custom-only feature subset")
				}
				configPath := filepath.Join(dir, "config.h")
				config, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				withoutFeature := strings.Replace(string(config), tc.featureMacro, "", 1)
				if withoutFeature == string(config) {
					t.Fatalf("synthetic config lacks %q", tc.featureMacro)
				}
				if err := os.WriteFile(configPath, []byte(withoutFeature), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(tc.variant); err == nil {
					t.Fatal("accepted config without the combined feature")
				}
				if err := os.WriteFile(configPath, config, 0o644); err != nil {
					t.Fatal(err)
				}
				stampPath := filepath.Join(dir, ".gopus-libopus-build")
				stamp, err := os.ReadFile(stampPath)
				if err != nil {
					t.Fatal(err)
				}
				withoutFeature = strings.Replace(string(stamp), tc.stampField, strings.Replace(tc.stampField, "=1", "=0", 1), 1)
				if withoutFeature == string(stamp) {
					t.Fatalf("synthetic stamp lacks %q", tc.stampField)
				}
				if err := os.WriteFile(stampPath, []byte(withoutFeature), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(tc.variant); err == nil {
					t.Fatal("accepted stamp without the combined feature")
				}
			})
		}
	}
}

func TestValidateQEXTReferenceBuildRequiresFeatureAndPairedISA(t *testing.T) {
	if runtime.GOARCH != "arm64" && runtime.GOARCH != "amd64" {
		t.Skip("paired SIMD reference is defined for arm64 and amd64")
	}
	for _, variant := range []LibopusReferenceVariant{LibopusReferenceQEXTScalar, LibopusReferenceQEXTSIMD} {
		dir := writePairedReferenceTree(t, t.TempDir(), variant, runtime.GOOS, runtime.GOARCH, "opus_demo")
		if err := ValidateLibopusReferenceBuild(dir, variant, DefaultVersion); err != nil {
			t.Fatalf("%s build: %v", variant, err)
		}
		other := LibopusReferenceQEXTScalar
		if variant == other {
			other = LibopusReferenceQEXTSIMD
		}
		if err := ValidateLibopusReferenceBuild(dir, other, DefaultVersion); err == nil {
			t.Fatalf("%s build accepted as %s", variant, other)
		}
		tool := filepath.Join(dir, "opus_demo")
		if runtime.GOOS == "windows" {
			tool += ".exe"
		}
		if err := ValidateLibopusReferenceToolOverride(tool, "opus_demo", variant, DefaultVersion); err != nil {
			t.Fatalf("%s tool: %v", variant, err)
		}
		if err := ValidateLibopusReferenceToolOverride(tool, "opus_demo", other, DefaultVersion); err == nil {
			t.Fatalf("%s tool accepted as %s", variant, other)
		}
	}

	for _, mutation := range []struct {
		name string
		file string
		edit func(string) string
	}{
		{"missing stamp", ".gopus-libopus-build", func(string) string { return "" }},
		{"wrong feature stamp", ".gopus-libopus-build", func(s string) string { return strings.Replace(s, "qext=1", "qext=0", 1) }},
		{"missing feature macro", "config.h", func(s string) string { return strings.Replace(s, "#define ENABLE_QEXT 1\n", "", 1) }},
		{"fixed-point config", "config.h", func(s string) string { return s + "#define FIXED_POINT 1\n" }},
		{"custom config", "config.h", func(s string) string { return s + "#define CUSTOM_MODES 1\n" }},
		{"wrong ISA macro", "config.h", func(s string) string { return "#define ENABLE_QEXT 1\n" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			dir := writePairedReferenceTree(t, t.TempDir(), LibopusReferenceQEXTSIMD, runtime.GOOS, runtime.GOARCH)
			path := filepath.Join(dir, mutation.file)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mutation.name == "missing stamp" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(mutation.edit(string(data))), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ValidateLibopusReferenceBuild(dir, LibopusReferenceQEXTSIMD, DefaultVersion); err == nil {
				t.Fatal("invalid QEXT build accepted")
			}
		})
	}
}

func TestValidateFixedQEXTReferenceBuildRequiresBothFeaturesAndPairedISA(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		for _, variant := range []LibopusReferenceVariant{LibopusReferenceFixedQEXTScalar, LibopusReferenceFixedQEXTSIMD} {
			t.Run(arch+"/"+string(variant), func(t *testing.T) {
				dir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", arch, "opus_demo")
				if err := validateLibopusReferenceBuildForPlatform(dir, variant, DefaultVersion, "linux", arch); err != nil {
					t.Fatalf("valid combined build: %v", err)
				}
				other := LibopusReferenceFixedQEXTScalar
				if variant == other {
					other = LibopusReferenceFixedQEXTSIMD
				}
				if err := validateLibopusReferenceBuildForPlatform(dir, other, DefaultVersion, "linux", arch); err == nil {
					t.Fatalf("accepted %s as %s", variant, other)
				}
				for _, mutation := range []struct {
					name string
					file string
					edit func(string) string
				}{
					{"missing fixed stamp", ".gopus-libopus-build", func(s string) string { return strings.Replace(s, "fixed=1", "fixed=0", 1) }},
					{"missing QEXT stamp", ".gopus-libopus-build", func(s string) string { return strings.Replace(s, "qext=1", "qext=0", 1) }},
					{"missing fixed macro", "config.h", func(s string) string { return strings.Replace(s, "#define FIXED_POINT 1\n", "", 1) }},
					{"missing RES24 macro", "config.h", func(s string) string { return strings.Replace(s, "#define ENABLE_RES24 1\n", "", 1) }},
					{"missing QEXT macro", "config.h", func(s string) string { return strings.Replace(s, "#define ENABLE_QEXT 1\n", "", 1) }},
					{"unexpected deep PLC", "config.h", func(s string) string { return s + "#define ENABLE_DEEP_PLC 1\n" }},
					{"unexpected custom modes", "config.h", func(s string) string { return s + "#define CUSTOM_MODES 1\n" }},
				} {
					t.Run(mutation.name, func(t *testing.T) {
						badDir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", arch)
						path := filepath.Join(badDir, mutation.file)
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte(mutation.edit(string(data))), 0o644); err != nil {
							t.Fatal(err)
						}
						if err := validateLibopusReferenceBuildForPlatform(badDir, variant, DefaultVersion, "linux", arch); err == nil {
							t.Fatal("accepted incomplete or mismatched fixed-QEXT reference")
						}
					})
				}
				// The Linux tool check reads Unix execute bits, which a
				// Windows filesystem does not store.
				if runtime.GOOS != "windows" {
					tool := filepath.Join(dir, "opus_demo")
					if err := validateLibopusReferenceToolOverrideForPlatform(tool, "opus_demo", variant, DefaultVersion, "linux", arch); err != nil {
						t.Fatalf("valid combined tool: %v", err)
					}
				}
			})
		}
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

func TestFindOrEnsureDefaultOpusDemoIgnoresGoLaneOverride(t *testing.T) {
	t.Setenv(LibopusAMD64TargetEnv, "")
	override := "simd"
	if goLibopusReferenceSIMD {
		override = "scalar"
	}
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", override)
	if _, err := ResolveLibopusReferenceVariant(); err == nil {
		t.Fatalf("conflicting GOPUS_LIBOPUS_REF_SCALAR=%q unexpectedly resolved for this Go build", override)
	}

	root := t.TempDir()
	srcDir := writeDefaultReferenceTree(t, root, runtime.GOOS, runtime.GOARCH, "opus_demo")
	got, err := FindOrEnsureDefaultOpusDemo(DefaultVersion, []string{root})
	if err != nil {
		t.Fatalf("find default fixture producer with conflicting Go-lane override: %v", err)
	}
	want := filepath.Join(srcDir, "opus_demo")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("default fixture producer=%q want recorded unsuffixed tool %q", got, want)
	}
}

func TestFindOrEnsureOpusDemoForVariantIgnoresGoLaneOverride(t *testing.T) {
	t.Setenv(LibopusAMD64TargetEnv, "")
	override := "simd"
	if goLibopusReferenceSIMD {
		override = "scalar"
	}
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", override)
	if _, err := ResolveLibopusReferenceVariant(); err == nil {
		t.Fatalf("conflicting GOPUS_LIBOPUS_REF_SCALAR=%q unexpectedly resolved for this Go build", override)
	}

	variant := LibopusReferenceScalar
	if goLibopusReferenceSIMD {
		variant = LibopusReferenceSIMD
	}
	root := t.TempDir()
	srcDir := writePairedReferenceTree(t, root, variant, runtime.GOOS, runtime.GOARCH, "opus_demo")
	got, err := FindOrEnsureOpusDemoForVariant(DefaultVersion, []string{root}, variant)
	if err != nil {
		t.Fatalf("find explicit %s fixture producer with conflicting Go-lane override: %v", variant, err)
	}
	want := filepath.Join(srcDir, "opus_demo")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Fatalf("explicit %s fixture producer=%q want %q", variant, got, want)
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

func TestValidateLibopusAMD64TargetStampAndCompilerFlags(t *testing.T) {
	t.Setenv(LibopusAMD64TargetEnv, "")
	root := t.TempDir()
	legacyDir := writePairedReferenceTree(t, root, LibopusReferenceScalar, "linux", "amd64")
	targetDir := filepath.Join(filepath.Dir(legacyDir), "opus-"+DefaultVersion+"-amd64-v2-scalar")
	if err := os.Rename(legacyDir, targetDir); err != nil {
		t.Fatal(err)
	}
	stampPath := filepath.Join(targetDir, ".gopus-libopus-build")
	stamp, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}
	baseCFlags := "CFLAGS=" + LibopusScalarCFLAGS
	targetCFlags := baseCFlags + " -march=x86-64-v2 -mtune=generic"
	updated := strings.Replace(string(stamp), baseCFlags, targetCFlags, 1)
	updated = strings.Replace(updated, "CPPFLAGS=", "amd64_target=v2\nCPPFLAGS=", 1)
	if updated == string(stamp) {
		t.Fatal("target test stamp did not include the expected CFLAGS and target identity")
	}
	if err := os.WriteFile(stampPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateLibopusReferenceBuildForPlatformAndTarget(targetDir, LibopusReferenceScalar, DefaultVersion, "linux", "amd64", "v2"); err != nil {
		t.Fatalf("validate v2 target tree: %v", err)
	}
	if err := validateLibopusReferenceBuildForPlatformAndTarget(targetDir, LibopusReferenceScalar, DefaultVersion, "linux", "amd64", "v3"); err == nil {
		t.Fatal("accepted a v2 stamp while selecting v3")
	} else {
		var configErr *LibopusReferenceConfigError
		if !errors.As(err, &configErr) {
			t.Fatalf("mismatched target error=%T %v, want LibopusReferenceConfigError", err, err)
		}
	}
	if err := validateLibopusReferenceBuildForPlatformAndTarget(targetDir, LibopusReferenceScalar, DefaultVersion, "linux", "amd64", ""); err == nil {
		t.Fatal("accepted a target-stamped tree when target selection was unset")
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
	qext := "0"
	fixed := "0"
	switch variant {
	case LibopusReferenceScalar:
	case LibopusReferenceSIMD:
		config = testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
	case LibopusReferenceFixedScalar:
		config = "#define FIXED_POINT 1\n#define ENABLE_RES24 1\n"
		configure = "--enable-static --disable-shared --enable-fixed-point --disable-asm --disable-rtcd --disable-intrinsics"
		fixed = "1"
	case LibopusReferenceFixedSIMD:
		config = "#define FIXED_POINT 1\n#define ENABLE_RES24 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-fixed-point --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		fixed = "1"
	case LibopusReferenceFixedQEXTScalar:
		config = "#define FIXED_POINT 1\n#define ENABLE_RES24 1\n#define ENABLE_QEXT 1\n"
		configure = "--enable-static --disable-shared --enable-fixed-point --enable-qext --disable-asm --disable-rtcd --disable-intrinsics"
		fixed = "1"
		qext = "1"
	case LibopusReferenceFixedQEXTSIMD:
		config = "#define FIXED_POINT 1\n#define ENABLE_RES24 1\n#define ENABLE_QEXT 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-fixed-point --enable-qext --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		fixed = "1"
		qext = "1"
	case LibopusReferenceQEXTScalar:
		config = "#define ENABLE_QEXT 1\n"
		configure = "--enable-static --disable-shared --enable-qext --disable-asm --disable-rtcd --disable-intrinsics"
		qext = "1"
	case LibopusReferenceQEXTSIMD:
		config = "#define ENABLE_QEXT 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-qext --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		qext = "1"
	case LibopusReferenceDREDQEXTScalar:
		config = "#define ENABLE_DRED 1\n#define ENABLE_DEEP_PLC 1\n#define ENABLE_QEXT 1\n"
		configure = "--enable-static --disable-shared --enable-qext --enable-dred --disable-asm --disable-rtcd --disable-intrinsics"
		cflags = ScalarDNNBuildCFLAGS
		qext = "1"
	case LibopusReferenceDREDQEXTSIMD:
		config = "#define ENABLE_DRED 1\n#define ENABLE_DEEP_PLC 1\n#define ENABLE_QEXT 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-qext --enable-dred --enable-rtcd --enable-intrinsics"
		cflags = DREDSIMDBuildCFLAGS
		qext = "1"
	case LibopusReferenceCustomScalar:
		config = "#define CUSTOM_MODES 1\n"
		configure = "--enable-static --disable-shared --enable-custom-modes --disable-asm --disable-rtcd --disable-intrinsics"
		custom = "1"
	case LibopusReferenceCustomSIMD:
		config = "#define CUSTOM_MODES 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		custom = "1"
	case LibopusReferenceCustomQEXTScalar:
		config = "#define CUSTOM_MODES 1\n#define ENABLE_QEXT 1\n"
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-qext --disable-asm --disable-rtcd --disable-intrinsics"
		custom = "1"
		qext = "1"
	case LibopusReferenceCustomQEXTSIMD:
		config = "#define CUSTOM_MODES 1\n#define ENABLE_QEXT 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-qext --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		custom = "1"
		qext = "1"
	case LibopusReferenceCustomFixedScalar:
		config = "#define CUSTOM_MODES 1\n#define FIXED_POINT 1\n#define ENABLE_RES24 1\n"
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-fixed-point --disable-asm --disable-rtcd --disable-intrinsics"
		custom = "1"
		fixed = "1"
	case LibopusReferenceCustomFixedSIMD:
		config = "#define CUSTOM_MODES 1\n#define FIXED_POINT 1\n#define ENABLE_RES24 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-fixed-point --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		custom = "1"
		fixed = "1"
	case LibopusReferenceCustomFixedQEXTScalar:
		config = "#define CUSTOM_MODES 1\n#define FIXED_POINT 1\n#define ENABLE_RES24 1\n#define ENABLE_QEXT 1\n"
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-fixed-point --enable-qext --disable-asm --disable-rtcd --disable-intrinsics"
		custom = "1"
		fixed = "1"
		qext = "1"
	case LibopusReferenceCustomFixedQEXTSIMD:
		config = "#define CUSTOM_MODES 1\n#define FIXED_POINT 1\n#define ENABLE_RES24 1\n#define ENABLE_QEXT 1\n" + testSIMDConfig(goarch)
		configure = "--enable-static --disable-shared --enable-custom-modes --enable-fixed-point --enable-qext --enable-rtcd --enable-intrinsics"
		cflags = LibopusBaseCFLAGS
		custom = "1"
		fixed = "1"
		qext = "1"
	}
	if err := os.WriteFile(filepath.Join(srcDir, "config.h"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if variant == LibopusReferenceDREDQEXTScalar || variant == LibopusReferenceDREDQEXTSIMD {
		_, testFile, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("locate pinned source fixtures")
		}
		pinnedRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", "tmp_check", "opus-"+DefaultVersion))
		if err := os.MkdirAll(filepath.Join(srcDir, "dnn"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"pitchdnn_data.c", "dred_rdovae_enc_data.c"} {
			data, err := os.ReadFile(filepath.Join(pinnedRoot, "dnn", name))
			if errors.Is(err, os.ErrNotExist) && os.Getenv("GOPUS_STRICT_LIBOPUS_REF") != "1" {
				// The DNN model sources appear only after the pinned model
				// archive is extracted; strict reference runs require them.
				t.Skipf("pinned DNN model source %s is not extracted: %v", name, err)
			}
			if err != nil {
				t.Fatalf("read pinned test source %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(srcDir, "dnn", name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stampLines := []string{
		"gopus libopus helper build v5",
		"version=" + DefaultVersion,
		"qext=" + qext,
		"fixed=" + fixed,
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
	}
	if variant == LibopusReferenceDREDQEXTScalar || variant == LibopusReferenceDREDQEXTSIMD {
		stampLines = append(stampLines, "dnn_model_sources="+dredQEXTModelSourcesStamp)
	}
	stampLines = append(stampLines, "")
	stamp := strings.Join(stampLines, "\n")
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

func writeDefaultReferenceTree(t *testing.T, root, goos, goarch string, tools ...string) string {
	t.Helper()
	srcDir := filepath.Join(root, "tmp_check", "opus-"+DefaultVersion)
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
	if err := os.WriteFile(filepath.Join(srcDir, "config.h"), []byte(testSIMDConfig(goarch)), 0o644); err != nil {
		t.Fatal(err)
	}
	stampLines := []string{
		"gopus libopus helper build v5",
		"version=" + DefaultVersion,
		"qext=0",
		"fixed=0",
		"custom=0",
		"host_os=" + testHostOS(goos),
		"host_arch=" + testHostArch(goarch),
		"host_bits=" + testHostBits(goarch),
		"cc=cc",
		"cc_path=/usr/bin/cc",
		"cc_target=" + testTargetTriple(goos, goarch),
		"cc_version=cc test",
		"configure=--enable-static --disable-shared",
		"CFLAGS=" + LibopusBaseCFLAGS,
		"CPPFLAGS=",
		"LDFLAGS=",
		"",
	}
	if err := os.WriteFile(filepath.Join(srcDir, ".gopus-libopus-build"), []byte(strings.Join(stampLines, "\n")), 0o644); err != nil {
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
