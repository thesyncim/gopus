package testvectors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

const encoderComplianceRefQFixturePath = "testdata/encoder_compliance_libopus_ref_q.json"

type encoderComplianceRefQFixtureFile struct {
	Version int                               `json:"version"`
	Cases   []encoderComplianceRefQFixtureRow `json:"cases"`
}

type encoderComplianceRefQFixtureRow struct {
	Mode      string  `json:"mode"`
	Bandwidth string  `json:"bandwidth"`
	FrameSize int     `json:"frame_size"`
	Channels  int     `json:"channels"`
	Bitrate   int     `json:"bitrate"`
	LibQ      float64 `json:"lib_q"`
}

var (
	encoderComplianceRefQFixtureOnce sync.Once
	encoderComplianceRefQFixtureData encoderComplianceRefQFixtureFile
	encoderComplianceRefQFixtureErr  error
)

func libopusComplianceReferenceAvailable() bool {
	if _, err := loadEncoderCompliancePacketsFixture(); err == nil {
		return true
	}
	_, err := loadEncoderComplianceReferenceQFixture()
	return err == nil
}

// nativeLibopusComplianceReferenceAvailable reports whether the loaded libopus
// reference fixture was freshly regenerated on this runner for the runtime's own
// GOOS/GOARCH-and-toolchain. The encoder precision guard only enforces the
// (gopus Q - libopus Q) gap against such a native same-arch reference: gopus's
// single portable float path is <=1-ULP-correct vs the native-arch libopus,
// while comparing against a fixture built for a different arch/toolchain (or a
// stale committed one) inflates the gap by libopus's own cross-toolchain
// self-variance on knife-edge signals.
//
// Nativeness is keyed on GOPUS_REQUIRE_PLATFORM_FIXTURES, which is set only by
// the CI jobs that run `make fixtures-gen-platform` first (macOS, ubuntu-arm64,
// the pinned-Docker amd64 provenance job, Windows). That env also forces the
// read path to the just-generated platform fixture and a same-arch provenance
// match, so it is an unambiguous "fresh native reference on this runner" signal.
// Jobs without it (the fast/race/flake sweeps) skip the gap guard and rely on
// the absolute quality floor plus the native-fixture jobs above.
func nativeLibopusComplianceReferenceAvailable() bool {
	if os.Getenv(requirePlatformFixturesEnv) == "" {
		return false
	}
	if !provenanceMatchesRuntime(loadedComplianceReferenceProvenance()) {
		return false
	}
	return libopusComplianceReferenceAvailable()
}

// loadedComplianceReferenceProvenance returns the provenance of whichever
// reference fixture runLibopusComplianceReferenceTest would consult first.
func loadedComplianceReferenceProvenance() libopusFixtureProvenance {
	if fixture, err := loadEncoderComplianceVariantsFixture(); err == nil {
		return fixture.Provenance
	}
	if fixture, err := loadEncoderCompliancePacketsFixture(); err == nil {
		return fixture.Provenance
	}
	return libopusFixtureProvenance{}
}

func provenanceMatchesRuntime(p libopusFixtureProvenance) bool {
	return p.GOOS == runtime.GOOS && p.GOARCH == runtime.GOARCH
}

func lookupEncoderComplianceReferenceQ(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (float64, bool) {
	fixture, err := loadEncoderComplianceReferenceQFixture()
	if err != nil {
		return 0, false
	}
	modeName := fixtureModeName(mode)
	bwName := fixtureBandwidthName(bandwidth)
	for _, row := range fixture.Cases {
		if row.Mode == modeName &&
			row.Bandwidth == bwName &&
			row.FrameSize == frameSize &&
			row.Channels == channels &&
			row.Bitrate == bitrate {
			return row.LibQ, true
		}
	}
	return 0, false
}

func loadEncoderComplianceReferenceQFixture() (encoderComplianceRefQFixtureFile, error) {
	encoderComplianceRefQFixtureOnce.Do(func() {
		path := filepath.Join(encoderComplianceRefQFixturePath)
		data, err := os.ReadFile(path)
		if err != nil {
			encoderComplianceRefQFixtureErr = err
			return
		}
		var fixture encoderComplianceRefQFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			encoderComplianceRefQFixtureErr = err
			return
		}
		encoderComplianceRefQFixtureData = fixture
	})
	return encoderComplianceRefQFixtureData, encoderComplianceRefQFixtureErr
}
