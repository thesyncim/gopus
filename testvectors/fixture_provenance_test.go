package testvectors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

type libopusFixtureProvenance struct {
	GOOS                    string `json:"goos"`
	GOARCH                  string `json:"goarch"`
	LibopusVersion          string `json:"libopus_version,omitempty"`
	QEXT                    string `json:"qext,omitempty"`
	HostOS                  string `json:"host_os,omitempty"`
	HostArch                string `json:"host_arch,omitempty"`
	HostBits                string `json:"host_bits,omitempty"`
	CC                      string `json:"cc,omitempty"`
	CCPath                  string `json:"cc_path,omitempty"`
	CCTarget                string `json:"cc_target,omitempty"`
	CCVersion               string `json:"cc_version,omitempty"`
	Configure               string `json:"configure,omitempty"`
	CFLAGS                  string `json:"cflags,omitempty"`
	CPPFLAGS                string `json:"cppflags,omitempty"`
	LDFLAGS                 string `json:"ldflags,omitempty"`
	LibopusBuildStampSHA256 string `json:"libopus_build_stamp_sha256,omitempty"`
}

func validateLibopusFixtureProvenance(p libopusFixtureProvenance) error {
	if p.GOOS == "" || p.GOARCH == "" {
		return fmt.Errorf("missing goos/goarch provenance")
	}
	if os.Getenv(requirePlatformFixturesEnv) != "" && (p.GOOS != runtime.GOOS || p.GOARCH != runtime.GOARCH) {
		return fmt.Errorf("platform provenance=%s/%s want %s/%s", p.GOOS, p.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if p.LibopusVersion != "" && p.LibopusVersion != libopustooling.DefaultVersion {
		return fmt.Errorf("libopus_version=%q want %q", p.LibopusVersion, libopustooling.DefaultVersion)
	}
	if p.QEXT != "" && p.QEXT != "0" && p.QEXT != "1" {
		return fmt.Errorf("qext=%q want 0 or 1", p.QEXT)
	}
	hasCompiler := p.HostOS != "" || p.HostArch != "" || p.CCTarget != "" || p.CCVersion != "" || p.LibopusBuildStampSHA256 != ""
	if !hasCompiler {
		return nil
	}
	for name, value := range map[string]string{
		"host_os":                    p.HostOS,
		"host_arch":                  p.HostArch,
		"host_bits":                  p.HostBits,
		"cc":                         p.CC,
		"cc_path":                    p.CCPath,
		"cc_target":                  p.CCTarget,
		"cc_version":                 p.CCVersion,
		"configure":                  p.Configure,
		"cflags":                     p.CFLAGS,
		"libopus_build_stamp_sha256": p.LibopusBuildStampSHA256,
	} {
		if value == "" {
			return fmt.Errorf("compiler provenance is missing %s", name)
		}
	}
	if !isLowerHexSHA256(p.LibopusBuildStampSHA256) {
		return fmt.Errorf("libopus_build_stamp_sha256 must be lowercase sha256 hex")
	}
	return nil
}

func isLowerHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func TestGeneratedLibopusFixturesCarryProvenance(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierFast)

	paths := []string{
		"testdata/libopus_decoder_matrix_fixture.json",
		"testdata/libopus_decoder_matrix_fixture_linux_amd64.json",
		"testdata/libopus_decoder_rate_matrix_fixture.json",
		"testdata/libopus_decoder_loss_fixture.json",
		"testdata/libopus_decoder_loss_fixture_linux_amd64.json",
		"testdata/encoder_compliance_libopus_packets_fixture.json",
		"testdata/encoder_compliance_libopus_packets_fixture_linux_amd64.json",
		"testdata/encoder_compliance_libopus_variants_fixture.json",
		"testdata/encoder_compliance_libopus_variants_fixture_linux_amd64.json",
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var fixture struct {
				Provenance libopusFixtureProvenance `json:"provenance"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatalf("unmarshal fixture: %v", err)
			}
			if err := validateLibopusFixtureProvenance(fixture.Provenance); err != nil {
				t.Fatalf("invalid provenance: %v", err)
			}
		})
	}
}

func TestRequiredPlatformFixtureProvenanceMatchesRuntime(t *testing.T) {
	t.Setenv(requirePlatformFixturesEnv, "1")

	if err := validateLibopusFixtureProvenance(libopusFixtureProvenance{
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
	}); err != nil {
		t.Fatalf("runtime platform provenance rejected: %v", err)
	}

	if err := validateLibopusFixtureProvenance(libopusFixtureProvenance{
		GOOS:   "other",
		GOARCH: runtime.GOARCH,
	}); err == nil {
		t.Fatalf("mismatched platform provenance accepted")
	}
}
