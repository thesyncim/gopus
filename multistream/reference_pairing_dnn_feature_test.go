//go:build (gopus_dred || gopus_osce) && !gopus_fixed_point

package multistream

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestMultistreamReferenceFeaturePairing(t *testing.T) {
	cfg := libopustest.CHelperConfig{
		Label:      "multistream DNN feature pairing",
		OutputBase: "gopus_multistream_dnn_pairing",
		SourceFile: "libopus_qext_decode96k_info.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{"-lm"},
		DeadStrip:  true,
	}
	bin, err := buildMultistreamReferenceHelper(cfg)
	if err != nil {
		t.Fatalf("build paired multistream DNN helper: %v", err)
	}
	cachedBin, err := buildMultistreamReferenceHelper(cfg)
	if err != nil {
		t.Fatalf("rebuild paired multistream DNN helper: %v", err)
	}
	if cachedBin != bin {
		t.Fatalf("same helper config returned a different cache path: first=%q second=%q", bin, cachedBin)
	}
	helperName := strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
	if !strings.Contains(helperName, "_"+runtime.GOOS+"_"+runtime.GOARCH+"_") ||
		!regexp.MustCompile(`_[0-9a-f]{12,16}$`).MatchString(helperName) {
		t.Fatalf("helper path %q does not identify the current platform and content cache key", bin)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat selected DNN helper %q: %v", bin, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("selected DNN helper %q is not a regular file", bin)
	}

	identity, err := libopustest.ResolvePublicAPIReferenceIdentity()
	if err != nil {
		t.Fatalf("resolve selected public reference identity: %v", err)
	}
	buildDir := identity.BuildDir
	archive := identity.ArchivePath
	if identity.DNN && filepath.Dir(bin) != buildDir {
		t.Fatalf("helper %q is outside selected reference build %q", bin, buildDir)
	}
	if !identity.DNN && !strings.Contains(filepath.Base(bin), string(identity.Variant)) {
		t.Fatalf("helper %q does not identify selected reference variant %q", bin, identity.Variant)
	}
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("selected DNN archive is missing beside helper %q: %v", bin, err)
	}
	if err := identity.Validate(); err != nil {
		t.Fatalf("validate selected public archive identity: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(buildDir, "config.h"))
	if err != nil {
		t.Fatalf("read selected DNN build config: %v", err)
	}
	for _, feature := range []struct {
		name string
		want bool
	}{
		{"ENABLE_DRED", identity.DRED},
		{"ENABLE_OSCE", identity.OSCE},
		{"ENABLE_OSCE_BWE", identity.OSCE},
		{"ENABLE_QEXT", identity.QEXT},
		{"ENABLE_DEEP_PLC", true},
		{"FIXED_POINT", false},
		{"CUSTOM_MODES", identity.Custom},
	} {
		if got := dnnConfigDefines(string(config), feature.name); got != feature.want {
			t.Errorf("selected DNN config %s=%t, want %t", feature.name, got, feature.want)
		}
	}
	if err := libopustooling.ValidateLibopusInstructionConfig(string(config), identity.Variant, runtime.GOARCH); err != nil {
		t.Fatalf("selected DNN config does not match Go instruction lane %q: %v", identity.Variant, err)
	}

	// Execute the exact returned helper. The empty request checks that this
	// binary links the selected archive and speaks the expected C oracle format.
	payload := libopustest.NewOraclePayloadVersion("GQDI", 4, 0, 1, 480, 0, 0, 48000)
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "multistream DNN feature pairing", "GQDO", 4)
	if err != nil {
		t.Fatalf("run selected DNN helper: %v", err)
	}
	reader.Count(0)
	reader.Count(0)
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("selected DNN helper output: %v", err)
	}
}

func TestDNNHelperRejectsUnpairedLibopusLinkInputs(t *testing.T) {
	for _, lib := range []string{
		"-lopus",
		"-l:libopus.so.0",
		"/usr/lib/libopus.dylib",
		"-Wl,-l,opus",
		"-Wl,-force_load,/tmp/libopus.a",
	} {
		t.Run(lib, func(t *testing.T) {
			_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
				OutputBase: "gopus_multistream_dnn_pairing_reject",
				SourceFile: "libopus_qext_decode96k_info.c",
				Libs:       []string{lib},
			})
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("BuildDNNCHelper(%q) error=%v, want LibopusReferenceConfigError", lib, err)
			}
		})
	}
	for _, ldflag := range []string{"-Wl,-l,opus", "-Wl,-force_load,/tmp/libopus.dylib"} {
		t.Run("ldflag/"+ldflag, func(t *testing.T) {
			_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
				OutputBase: "gopus_multistream_dnn_pairing_reject_ldflag",
				SourceFile: "libopus_qext_decode96k_info.c",
				LDFlags:    []string{ldflag},
			})
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("BuildDNNCHelper LDFlag %q error=%v, want LibopusReferenceConfigError", ldflag, err)
			}
		})
	}
	for _, cflag := range []string{"-lopus", "-Wl,-l,opus", "-Wl,-framework,opus", "/usr/lib/libopus.so.0"} {
		t.Run("cflag/"+cflag, func(t *testing.T) {
			_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
				OutputBase: "gopus_multistream_dnn_pairing_reject_cflag",
				SourceFile: "libopus_qext_decode96k_info.c",
				CFlags:     []string{cflag},
			})
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("BuildDNNCHelper CFlag %q error=%v, want LibopusReferenceConfigError", cflag, err)
			}
		})
	}
	t.Run("cflag/-Xlinker -l -Xlinker opus", func(t *testing.T) {
		_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			OutputBase: "gopus_multistream_dnn_pairing_reject_xlinker",
			SourceFile: "libopus_qext_decode96k_info.c",
			CFlags:     []string{"-Xlinker", "-l", "-Xlinker", "opus"},
		})
		var configErr *libopustooling.LibopusReferenceConfigError
		if !errors.As(err, &configErr) {
			t.Fatalf("BuildDNNCHelper split -Xlinker error=%v, want LibopusReferenceConfigError", err)
		}
	})
	t.Run("split -l opus", func(t *testing.T) {
		_, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			OutputBase: "gopus_multistream_dnn_pairing_reject_split_library",
			SourceFile: "libopus_qext_decode96k_info.c",
			Libs:       []string{"-l", "opus"},
		})
		var configErr *libopustooling.LibopusReferenceConfigError
		if !errors.As(err, &configErr) {
			t.Fatalf("BuildDNNCHelper split library arguments error=%v, want LibopusReferenceConfigError", err)
		}
	})
}

func TestDNNHelperRejectsReferenceSelectorOverrides(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  libopustest.CHelperConfig
	}{
		{"base qext", libopustest.CHelperConfig{QEXTRef: true}},
		{"fixed", libopustest.CHelperConfig{FixedRef: true}},
		{"fixed qext", libopustest.CHelperConfig{FixedQEXTRef: true}},
		{"dred qext", libopustest.CHelperConfig{DREDQEXTRef: true}},
		{"custom", libopustest.CHelperConfig{CustomRef: true}},
		{"custom qext", libopustest.CHelperConfig{CustomQEXTRef: true}},
		{"custom fixed", libopustest.CHelperConfig{CustomFixedRef: true}},
		{"custom fixed qext", libopustest.CHelperConfig{CustomFixedQEXTRef: true}},
		{"simd", libopustest.CHelperConfig{SIMDRef: true}},
		{"force scalar", libopustest.CHelperConfig{ForceScalarRef: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.OutputBase = "gopus_multistream_dnn_selector_reject"
			tc.cfg.SourceFile = "libopus_qext_decode96k_info.c"
			_, err := buildMultistreamReferenceHelper(tc.cfg)
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("DNN helper selector %s error=%v, want LibopusReferenceConfigError", tc.name, err)
			}
		})
	}
}

func dnnConfigDefines(config, name string) bool {
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "#define" && fields[1] == name {
			return len(fields) < 3 || fields[2] != "0"
		}
	}
	return false
}
