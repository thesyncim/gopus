package libopustooling

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDREDQEXTReferenceVariantPairsScalarAndSIMD(t *testing.T) {
	for _, tc := range []struct {
		base LibopusReferenceVariant
		want LibopusReferenceVariant
	}{
		{base: LibopusReferenceScalar, want: LibopusReferenceDREDQEXTScalar},
		{base: LibopusReferenceSIMD, want: LibopusReferenceDREDQEXTSIMD},
	} {
		got, err := dredQEXTVariantFor(tc.base)
		if err != nil {
			t.Fatalf("dredQEXTVariantFor(%q): %v", tc.base, err)
		}
		if got != tc.want {
			t.Fatalf("dredQEXTVariantFor(%q)=%q want %q", tc.base, got, tc.want)
		}
		suffix, err := LibopusReferenceSourceSuffix(got)
		if err != nil {
			t.Fatal(err)
		}
		wantSuffix := "-dred-qext-scalar"
		if tc.want == LibopusReferenceDREDQEXTSIMD {
			wantSuffix = "-dred-qext-simd"
		}
		if suffix != wantSuffix {
			t.Fatalf("source suffix(%q)=%q want %q", got, suffix, wantSuffix)
		}
	}
	if _, err := dredQEXTVariantFor(LibopusReferenceQEXTScalar); err == nil {
		t.Fatal("DRED-QEXT accepted a non-base reference variant")
	}
}

func TestValidateDREDQEXTReferenceBuildRequiresCombinedFlagsAndPairedISA(t *testing.T) {
	for _, goarch := range []string{"amd64", "arm64"} {
		for _, variant := range []LibopusReferenceVariant{LibopusReferenceDREDQEXTScalar, LibopusReferenceDREDQEXTSIMD} {
			t.Run(goarch+"/"+string(variant), func(t *testing.T) {
				dir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", goarch)
				validate := func() error {
					return validateLibopusReferenceBuildForPlatform(dir, variant, DefaultVersion, "linux", goarch)
				}
				if err := validate(); err != nil {
					t.Fatalf("matching combined build rejected: %v", err)
				}
				other := LibopusReferenceDREDQEXTScalar
				if variant == other {
					other = LibopusReferenceDREDQEXTSIMD
				}
				if err := validateLibopusReferenceBuildForPlatform(dir, other, DefaultVersion, "linux", goarch); err == nil {
					t.Fatalf("%s archive accepted as %s", variant, other)
				}
				for _, mutation := range []struct {
					name string
					edit func(string) string
				}{
					{name: "missing_model_stamp", edit: func(stamp string) string {
						return strings.Replace(stamp, "dnn_model_sources="+dredQEXTModelSourcesStamp+"\n", "", 1)
					}},
					{name: "wrong_model_stamp", edit: func(stamp string) string {
						return strings.Replace(stamp, "pitchdnn_data.c=", "pitchdnn_data.c=00", 1)
					}},
				} {
					t.Run(mutation.name, func(t *testing.T) {
						badDir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", goarch)
						path := filepath.Join(badDir, ".gopus-libopus-build")
						stamp, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte(mutation.edit(string(stamp))), 0o644); err != nil {
							t.Fatal(err)
						}
						if err := validateLibopusReferenceBuildForPlatform(badDir, variant, DefaultVersion, "linux", goarch); err == nil {
							t.Fatal("accepted missing or incorrect DNN model stamp")
						}
					})
				}
				for _, mutation := range []struct {
					name string
					edit func(string) error
				}{
					{name: "missing_pitch_model", edit: func(path string) error { return os.Remove(path) }},
					{name: "wrong_encoder_model", edit: func(path string) error { return os.WriteFile(path, []byte("changed model source"), 0o644) }},
				} {
					t.Run(mutation.name, func(t *testing.T) {
						badDir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", goarch)
						path := filepath.Join(badDir, "dnn", "pitchdnn_data.c")
						if mutation.name == "wrong_encoder_model" {
							path = filepath.Join(badDir, "dnn", "dred_rdovae_enc_data.c")
						}
						if err := mutation.edit(path); err != nil {
							t.Fatal(err)
						}
						if err := validateLibopusReferenceBuildForPlatform(badDir, variant, DefaultVersion, "linux", goarch); err == nil {
							t.Fatal("accepted missing or modified pinned DNN source")
						}
					})
				}

				configPath := filepath.Join(dir, "config.h")
				original, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, macro := range []string{"ENABLE_DRED", "ENABLE_DEEP_PLC", "ENABLE_QEXT"} {
					t.Run("missing_"+strings.ToLower(macro), func(t *testing.T) {
						config := strings.ReplaceAll(string(original), "#define "+macro+" 1\n", "")
						if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
							t.Fatal(err)
						}
						if err := validate(); err == nil {
							t.Fatalf("accepted config without %s", macro)
						}
					})
				}
				if err := os.WriteFile(configPath, append(append([]byte(nil), original...), []byte("#define ENABLE_OSCE 1\n")...), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(); err == nil {
					t.Fatal("accepted combined DRED-QEXT config with ENABLE_OSCE")
				}
			})
		}
	}
}

func TestExistingReferenceBuildsRejectDREDAndDeepPLCFeatures(t *testing.T) {
	for _, variant := range []LibopusReferenceVariant{
		LibopusReferenceScalar,
		LibopusReferenceSIMD,
		LibopusReferenceCustomScalar,
		LibopusReferenceCustomSIMD,
	} {
		t.Run(string(variant), func(t *testing.T) {
			dir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", "amd64")
			if err := validateLibopusReferenceBuildForPlatform(dir, variant, DefaultVersion, "linux", "amd64"); err != nil {
				t.Fatalf("reference build rejected: %v", err)
			}
			configPath := filepath.Join(dir, "config.h")
			config, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, macro := range []string{"ENABLE_DRED", "ENABLE_DEEP_PLC"} {
				t.Run("unexpected_"+strings.ToLower(macro), func(t *testing.T) {
					if err := os.WriteFile(configPath, append(append([]byte(nil), config...), []byte("#define "+macro+" 1\n")...), 0o644); err != nil {
						t.Fatal(err)
					}
					var configErr *LibopusReferenceConfigError
					if err := validateLibopusReferenceBuildForPlatform(dir, variant, DefaultVersion, "linux", "amd64"); !errors.As(err, &configErr) {
						t.Fatalf("accepted unexpected %s: err=%v", macro, err)
					}
				})
			}
		})
	}
}

func TestDREDQEXTBuildEnvironmentClearsConflictingFeatureFlags(t *testing.T) {
	env := []string{
		"PATH=/bin",
		"LIBOPUS_VERSION=9.9.9",
		"LIBOPUS_ENABLE_FIXED_SCALAR=1",
		"LIBOPUS_ENABLE_QEXT_SIMD=1",
		"LIBOPUS_ENABLE_DRED_QEXT_SCALAR=1",
		"LIBOPUS_CFLAGS=-O0",
		"LIBOPUS_CPPFLAGS=-Iwrong",
		"CFLAGS=-O0",
		"CPPFLAGS=-Iwrong",
		"LDFLAGS=-Lwrong",
	}
	got := dredQEXTBuildEnvironment(env, "1.6.1", "LIBOPUS_ENABLE_DRED_QEXT_SIMD=1", DREDSIMDBuildCFLAGS)
	values := make(map[string]string, len(got))
	for _, item := range got {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			values[name] = value
		}
	}
	if values["LIBOPUS_VERSION"] != "1.6.1" || values["LIBOPUS_ENABLE_DRED_QEXT_SIMD"] != "1" {
		t.Fatalf("combined build identity was not pinned: %v", values)
	}
	for _, name := range []string{
		"LIBOPUS_ENABLE_FIXED_SCALAR", "LIBOPUS_ENABLE_QEXT_SIMD", "LIBOPUS_ENABLE_DRED_QEXT_SCALAR",
	} {
		if _, ok := values[name]; ok {
			t.Fatalf("combined build kept conflicting %s=%q", name, values[name])
		}
	}
	if values["LIBOPUS_CFLAGS"] != DREDSIMDBuildCFLAGS || values["LIBOPUS_CPPFLAGS"] != "" || values["CPPFLAGS"] != "" || values["LDFLAGS"] != "" || values["CFLAGS"] != "" {
		t.Fatalf("combined build kept inherited compiler flags: %v", values)
	}
}
