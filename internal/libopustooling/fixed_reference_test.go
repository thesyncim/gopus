package libopustooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixedReferenceRequiresFeatureAndPairedISA(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		for _, variant := range []LibopusReferenceVariant{LibopusReferenceFixedScalar, LibopusReferenceFixedSIMD} {
			t.Run(arch+"/"+string(variant), func(t *testing.T) {
				dir := writePairedReferenceTree(t, t.TempDir(), variant, "linux", arch)
				validate := func(v LibopusReferenceVariant) error {
					return validateLibopusReferenceBuildForPlatform(dir, v, DefaultVersion, "linux", arch)
				}
				if err := validate(variant); err != nil {
					t.Fatal(err)
				}
				other := LibopusReferenceFixedScalar
				if variant == other {
					other = LibopusReferenceFixedSIMD
				}
				if err := validate(other); err == nil {
					t.Fatal("accepted the opposite fixed instruction lane")
				}
				configPath := filepath.Join(dir, "config.h")
				config, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, invalid := range []string{
					strings.ReplaceAll(string(config), "#define FIXED_POINT 1\n", ""),
					strings.ReplaceAll(string(config), "#define ENABLE_RES24 1\n", ""),
					string(config) + "#define ENABLE_DRED 1\n",
					string(config) + "#define ENABLE_DEEP_PLC 1\n",
					string(config) + "#define ENABLE_OSCE 1\n",
					string(config) + "#define ENABLE_QEXT 1\n",
					string(config) + "#define CUSTOM_MODES 1\n",
				} {
					if err := os.WriteFile(configPath, []byte(invalid), 0o644); err != nil {
						t.Fatal(err)
					}
					if err := validate(variant); err == nil {
						t.Fatalf("accepted mismatched fixed feature config %q", invalid)
					}
				}
				wrongISA := "#define FIXED_POINT 1\n#define ENABLE_RES24 1\n"
				if variant == LibopusReferenceFixedScalar {
					wrongISA += testSIMDConfig(arch)
				}
				if err := os.WriteFile(configPath, []byte(wrongISA), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(variant); err == nil {
					t.Fatal("accepted opposite instruction macros")
				}
				if err := os.WriteFile(configPath, config, 0o644); err != nil {
					t.Fatal(err)
				}
				stampPath := filepath.Join(dir, ".gopus-libopus-build")
				stamp, err := os.ReadFile(stampPath)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stampPath, []byte(strings.ReplaceAll(string(stamp), "fixed=1", "fixed=0")), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := validate(variant); err == nil {
					t.Fatal("accepted float build stamp")
				}
			})
		}
	}
}
