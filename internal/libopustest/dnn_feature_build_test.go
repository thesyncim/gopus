package libopustest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestDNNHelperIncludesPinnedSourceRootAfterBuildConfig(t *testing.T) {
	cc, err := libopustooling.FindCCompiler()
	if err != nil {
		t.Skipf("C compiler unavailable: %v", err)
	}
	root := t.TempDir()
	buildDir := filepath.Join(root, "build")
	sourceDir := filepath.Join(root, "source")
	for _, dir := range []string{
		buildDir,
		filepath.Join(sourceDir, "celt"),
		filepath.Join(sourceDir, "include"),
		filepath.Join(sourceDir, "dnn"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(buildDir, "config.h"), []byte("#define DNN_HELPER_CONFIG 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "config.h"), []byte("#error source-root config must not override build config\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "celt", "cpu_support.h"), []byte("#include \"config.h\"\n#if !defined(DNN_HELPER_CONFIG)\n#error generated build config was not selected\n#endif\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "helper.c")
	if err := os.WriteFile(helper, []byte("#include <celt/cpu_support.h>\nint main(void) { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"-std=c99", "-fsyntax-only"}, dnnHelperIncludeArgs(buildDir, sourceDir)...)
	args = append(args, helper)
	if output, err := exec.Command(cc, args...).CombinedOutput(); err != nil {
		t.Fatalf("compile DNN helper source-root include probe: %v (%s)", err, output)
	}
}

func TestDNNHelperCompileFlagsPreserveCompilerDefaultDialectAndContraction(t *testing.T) {
	for _, cflags := range []string{
		libopustooling.LibopusBaseCFLAGS,
		libopustooling.LibopusScalarCFLAGS,
		libopustooling.DREDSIMDBuildCFLAGS,
		libopustooling.ScalarDNNBuildCFLAGS,
		libopustooling.OSCEScalarDNNBuildCFLAGS,
	} {
		for _, flag := range dnnHelperCompileFlags(cflags) {
			if strings.HasPrefix(flag, "-std=") || strings.HasPrefix(flag, "-ffp-contract=") {
				t.Errorf("DNN helper flag %q in CFLAGS %q overrides the compiler's default reference policy", flag, cflags)
			}
		}
	}
}

func TestDNNFeatureBuildConfigAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dred   bool
		osce   bool
		qext   bool
		custom bool
	}{
		{"dred", true, false, false, false},
		{"osce", false, true, false, false},
		{"osce_qext", false, true, true, false},
		{"dred_osce", true, true, false, false},
		{"dred_osce_qext", true, true, true, false},
		{"dred_custom", true, false, false, true},
		{"osce_custom_qext", false, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := featureDNNBuildConfig(tc.dred, tc.osce, tc.qext, tc.custom, libopustooling.LibopusReferenceScalar)
			args := strings.Join(dnnConfigureArgs(cfg), " ")
			for _, flag := range []struct {
				name string
				want bool
			}{
				{"--enable-dred", tc.dred},
				{"--enable-osce", tc.osce},
				{"--enable-osce-bwe", tc.osce},
				{"--enable-qext", tc.qext},
				{"--enable-custom-modes", tc.custom},
				{"--disable-intrinsics", true},
			} {
				if strings.Contains(args, flag.name) != flag.want {
					t.Fatalf("configure args %q: %s presence=%t want %t", args, flag.name, strings.Contains(args, flag.name), flag.want)
				}
			}
			config := "#define ENABLE_DEEP_PLC 1\n"
			if tc.dred {
				config += "#define ENABLE_DRED 1\n"
			}
			if tc.osce {
				config += "#define ENABLE_OSCE 1\n#define ENABLE_OSCE_BWE 1\n"
			}
			if tc.qext {
				config += "#define ENABLE_QEXT 1\n"
			}
			if tc.custom {
				config += "#define CUSTOM_MODES 1\n"
			}
			buildDir := t.TempDir()
			writeConfig := func(value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(buildDir, "config.h"), []byte(value), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeConfig(config)
			if err := validateDREDInstructionBuild(buildDir, cfg); err != nil {
				t.Fatalf("paired config rejected: %v", err)
			}
			for _, name := range []string{"ENABLE_DEEP_PLC", "ENABLE_DRED", "ENABLE_OSCE", "ENABLE_OSCE_BWE", "ENABLE_QEXT", "CUSTOM_MODES"} {
				if !strings.Contains(config, "#define "+name+" 1\n") {
					continue
				}
				writeConfig(strings.Replace(config, "#define "+name+" 1\n", "#define "+name+" 0\n", 1))
				if err := validateDREDInstructionBuild(buildDir, cfg); err == nil {
					t.Fatalf("disabled %s macro accepted", name)
				}
			}
			for _, wrong := range []string{
				config + "#define FIXED_POINT 1\n",
				config + "#define CUSTOM_MODES 1\n",
				config + "#define ENABLE_OSCE_TRAINING_DATA 1\n",
				config + "#define ENABLE_DRED 0\n",
				config + "#define ENABLE_OSCE 0\n",
				config + "#define ENABLE_QEXT 0\n",
			} {
				// A second definition is treated as enabled; only run the
				// contradictory optional-feature cases when absent originally.
				if (strings.Contains(wrong, "#define ENABLE_DRED 0") && tc.dred) ||
					(strings.Contains(wrong, "#define ENABLE_OSCE 0") && tc.osce) ||
					(strings.Contains(wrong, "#define ENABLE_QEXT 0") && tc.qext) ||
					(strings.Contains(wrong, "#define CUSTOM_MODES 1") && tc.custom) {
					continue
				}
				writeConfig(wrong)
				if err := validateDREDInstructionBuild(buildDir, cfg); err == nil {
					t.Fatalf("mismatched config accepted: %q", wrong)
				}
			}
		})
	}
}

func TestDNNFeatureBuildStampAndInstructionLane(t *testing.T) {
	if _, err := libopustooling.FindCCompiler(); err != nil {
		t.Skipf("C compiler unavailable: %v", err)
	}
	cfg := featureDNNBuildConfig(true, true, true, true, libopustooling.LibopusReferenceSIMD)
	args := strings.Join(dnnConfigureArgs(cfg), " ")
	if !strings.Contains(args, "--enable-rtcd --enable-intrinsics") || strings.Contains(args, "--disable-asm") {
		t.Fatalf("SIMD configure args=%q", args)
	}
	buildDir := t.TempDir()
	if cfg.buildCurrent(buildDir) {
		t.Fatal("unstamped build accepted")
	}
	if err := cfg.writeStamp(buildDir); err != nil {
		t.Fatal(err)
	}
	if !cfg.buildCurrent(buildDir) {
		t.Fatal("current build stamp rejected")
	}
	stamp, err := os.ReadFile(filepath.Join(buildDir, featureDNNBuildStampFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{
		"LIBOPUS_VERSION=" + libopustooling.DefaultVersion,
		"DRED_MODEL_SOURCES=" + libopustooling.DREDModelSourcesStamp(),
		"CUSTOM_MODES=true",
	} {
		if !strings.Contains(string(stamp), identity) {
			t.Fatalf("DNN build stamp %q lacks source identity %q", stamp, identity)
		}
	}
	staleVersion := strings.Replace(string(stamp), "LIBOPUS_VERSION="+libopustooling.DefaultVersion, "LIBOPUS_VERSION=0.0.0", 1)
	if err := os.WriteFile(filepath.Join(buildDir, featureDNNBuildStampFile), []byte(staleVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg.buildCurrent(buildDir) {
		t.Fatal("build stamp for a different source version accepted")
	}
	staleModels := strings.Replace(string(stamp), libopustooling.DREDModelSourcesStamp(), "untrusted-model-sources", 1)
	if err := os.WriteFile(filepath.Join(buildDir, featureDNNBuildStampFile), []byte(staleModels), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg.buildCurrent(buildDir) {
		t.Fatal("build stamp for different DRED model sources accepted")
	}
	if err := os.WriteFile(filepath.Join(buildDir, featureDNNBuildStampFile), []byte("wrong features or compiler"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg.buildCurrent(buildDir) {
		t.Fatal("wrong build stamp accepted")
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
	features := "#define ENABLE_DEEP_PLC 1\n#define ENABLE_DRED 1\n#define ENABLE_OSCE 1\n#define ENABLE_OSCE_BWE 1\n#define ENABLE_QEXT 1\n#define CUSTOM_MODES 1\n"
	for _, tc := range []struct {
		name   string
		config string
		wantOK bool
	}{
		{"paired", features + simdMacro, true},
		{"missing_simd", features, false},
		{"missing_osce_bwe", strings.Replace(features+simdMacro, "#define ENABLE_OSCE_BWE 1\n", "", 1), false},
		{"extra_fixed", features + simdMacro + "#define FIXED_POINT 1\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(buildDir, "config.h"), []byte(tc.config), 0o644); err != nil {
				t.Fatal(err)
			}
			err := validateDREDInstructionBuild(buildDir, cfg)
			if (err == nil) != tc.wantOK {
				t.Fatalf("validateDREDInstructionBuild() error=%v wantOK=%v", err, tc.wantOK)
			}
		})
	}
}

func TestDNNBuildRejectsUnpinnedModelSources(t *testing.T) {
	root := t.TempDir()
	dnnDir := filepath.Join(root, "dnn")
	if err := os.MkdirAll(dnnDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pitchdnn_data.c", "dred_rdovae_enc_data.c"} {
		if err := os.WriteFile(filepath.Join(dnnDir, name), []byte("untrusted model source"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := libopustooling.ValidateDREDModelSources(root)
	var configErr *libopustooling.LibopusReferenceConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("unpinned DRED model sources error=%v, want LibopusReferenceConfigError", err)
	}
}

func TestLibopusLinkOverrideValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"archive", []string{"/tmp/libopus.a"}, true},
		{"shared library", []string{"/usr/lib/libopus.so.0"}, true},
		{"direct linker name", []string{"-lopus"}, true},
		{"colon linker name", []string{"-l:libopus.dylib"}, true},
		{"split linker name", []string{"-l", "opus"}, true},
		{"split library name", []string{"-l", "libopus"}, true},
		{"forwarded linker name", []string{"-Wl,-l,opus"}, true},
		{"forwarded library name", []string{"-Wl,-l,libopus"}, true},
		{"forwarded framework", []string{"-Wl,-framework,opus"}, true},
		{"forwarded archive", []string{"-Wl,-force_load,/tmp/libopus.a"}, true},
		{"split Xlinker", []string{"-Xlinker", "-l", "-Xlinker", "opus"}, true},
		{"include path", []string{"-I/usr/include/libopus-data"}, false},
		{"unrelated library", []string{"-lm"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNoLibopusLibraryOverride(tc.args)
			var configErr *libopustooling.LibopusReferenceConfigError
			if got := errors.As(err, &configErr); got != tc.want {
				t.Fatalf("validateNoLibopusLibraryOverride(%v) error=%v, config error=%t want %t", tc.args, err, got, tc.want)
			}
		})
	}
}

func TestDNNCompilerTargetMustMatchGoArchitecture(t *testing.T) {
	for _, tc := range []struct {
		target string
		goarch string
		wantOK bool
	}{
		{"arm64-apple-darwin", "arm64", true},
		{"aarch64-unknown-linux-gnu", "arm64", true},
		{"x86_64-pc-linux-gnu", "amd64", true},
		{"x86_64-pc-linux-gnu", "arm64", false},
		{"arm64-apple-darwin", "amd64", false},
	} {
		err := validateDNNCompilerTarget(tc.target, tc.goarch)
		if (err == nil) != tc.wantOK {
			t.Fatalf("compiler target=%q GOARCH=%s error=%v wantOK=%t", tc.target, tc.goarch, err, tc.wantOK)
		}
	}
}
