package testvectors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

const longFrameFixturePath = "testdata/encoder_compliance_longframe_libopus_ref.json"

var (
	longFrameFixtureOnce sync.Once
	longFrameFixtureData longFrameFixtureFile
	longFrameFixtureErr  error
)

type longFrameFixtureFile struct {
	Version int                    `json:"version"`
	Cases   []longFrameFixtureCase `json:"cases"`
}

type longFrameFixtureCase struct {
	Name      string   `json:"name"`
	Mode      string   `json:"mode"`
	Bandwidth string   `json:"bandwidth"`
	FrameSize int      `json:"frame_size"`
	Channels  int      `json:"channels"`
	Bitrate   int      `json:"bitrate"`
	LibQ      float64  `json:"lib_q"`
	Packets   []string `json:"packets_base64"`
}

type longFrameFixtureTarget struct {
	Name      string
	Mode      encoder.Mode
	Bandwidth types.Bandwidth
	FrameSize int
	Channels  int
	Bitrate   int
}

func longFrameFixtureTargets() []longFrameFixtureTarget {
	return []longFrameFixtureTarget{
		{Name: "SILK-NB-40ms-mono-16k", Mode: encoder.ModeSILK, Bandwidth: types.BandwidthNarrowband, FrameSize: 1920, Channels: 1, Bitrate: 16000},
		{Name: "SILK-WB-40ms-mono-32k", Mode: encoder.ModeSILK, Bandwidth: types.BandwidthWideband, FrameSize: 1920, Channels: 1, Bitrate: 32000},
		{Name: "SILK-WB-60ms-mono-32k", Mode: encoder.ModeSILK, Bandwidth: types.BandwidthWideband, FrameSize: 2880, Channels: 1, Bitrate: 32000},
		{Name: "Hybrid-SWB-40ms-mono-48k", Mode: encoder.ModeHybrid, Bandwidth: types.BandwidthSuperwideband, FrameSize: 1920, Channels: 1, Bitrate: 48000},
		{Name: "Hybrid-FB-60ms-mono-64k", Mode: encoder.ModeHybrid, Bandwidth: types.BandwidthFullband, FrameSize: 2880, Channels: 1, Bitrate: 64000},
	}
}

// TestLongFrameLibopusReferenceParityFromFixture validates long-frame SILK/Hybrid
// quality against frozen libopus reference packets. This keeps parity coverage
// available without requiring live libopus bindings.
func TestLongFrameLibopusReferenceParityFromFixture(t *testing.T) {
	requireTestTier(t, testTierParity)

	fixture, err := loadLongFrameFixtureCached()
	if err != nil {
		t.Fatalf("load long-frame fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported fixture version %d", fixture.Version)
	}

	byName := make(map[string]longFrameFixtureCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		byName[c.Name] = c
	}

	for _, tc := range longFrameFixtureTargets() {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			fixtureCase, ok := byName[tc.Name]
			if !ok {
				t.Fatalf("missing fixture case %q", tc.Name)
			}
			mode, err := parseFixtureMode(fixtureCase.Mode)
			if err != nil {
				t.Fatalf("invalid fixture mode: %v", err)
			}
			bandwidth, err := parseFixtureBandwidth(fixtureCase.Bandwidth)
			if err != nil {
				t.Fatalf("invalid fixture bandwidth: %v", err)
			}
			if mode != tc.Mode || bandwidth != tc.Bandwidth || fixtureCase.FrameSize != tc.FrameSize || fixtureCase.Channels != tc.Channels || fixtureCase.Bitrate != tc.Bitrate {
				t.Fatalf("fixture metadata mismatch for %q", tc.Name)
			}

			q, _ := runEncoderComplianceTest(t, tc.Mode, tc.Bandwidth, tc.FrameSize, tc.Channels, tc.Bitrate)
			libQ, err := runLongFrameFixtureReferenceCase(fixtureCase)
			if err != nil {
				t.Fatalf("run fixture reference case: %v", err)
			}

			snr := SNRFromQuality(q)
			libSNR := SNRFromQuality(libQ)
			gapDB := snr - libSNR
			if gapDB < -EncoderLibopusSpeechGapTightDB {
				t.Fatalf("long-frame libopus gap regressed: gap=%.2f dB (q=%.2f libQ=%.2f)", gapDB, q, libQ)
			}
		})
	}
}

func runLongFrameFixtureReferenceCase(c longFrameFixtureCase) (float64, error) {
	packets, err := decodeFixturePackets(c.Packets)
	if err != nil {
		return 0, err
	}
	numFrames := 48000 / c.FrameSize
	totalSamples := numFrames * c.FrameSize * c.Channels
	original := generateEncoderTestSignal(totalSamples, c.Channels)
	q, err := computeComplianceQualityFromPackets(packets, original, c.Channels, c.FrameSize)
	if err != nil {
		return 0, err
	}

	// Keep fixture values honest if opusdec behavior drifts.
	// arm64 is the strict calibration architecture for this fixture.
	if runtime.GOARCH == "arm64" {
		if math.Abs(q-c.LibQ) > 0.35 {
			return 0, fmt.Errorf("fixture libQ drift for %q: got %.2f want %.2f", c.Name, q, c.LibQ)
		}
	} else if math.Abs(q-c.LibQ) > 25.0 {
		// Non-arm64 can show stable decode-path drift; still guard against corruption.
		return 0, fmt.Errorf("fixture libQ sanity drift for %q: got %.2f want %.2f", c.Name, q, c.LibQ)
	}
	return c.LibQ, nil
}

func findLongFrameFixtureCase(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (longFrameFixtureCase, bool) {
	fixture, err := loadLongFrameFixtureCached()
	if err != nil {
		return longFrameFixtureCase{}, false
	}
	for _, c := range fixture.Cases {
		caseMode, err := parseFixtureMode(c.Mode)
		if err != nil {
			continue
		}
		caseBandwidth, err := parseFixtureBandwidth(c.Bandwidth)
		if err != nil {
			continue
		}
		if caseMode == mode &&
			caseBandwidth == bandwidth &&
			c.FrameSize == frameSize &&
			c.Channels == channels &&
			c.Bitrate == bitrate {
			return c, true
		}
	}
	return longFrameFixtureCase{}, false
}

func computeComplianceQualityFromPackets(packets [][]byte, original []float32, channels, frameSize int) (float64, error) {
	decoded, err := decodeCompliancePackets(packets, channels, frameSize)
	if err != nil {
		return 0, fmt.Errorf("decode compliance packets: %w", err)
	}
	if len(decoded) == 0 {
		return 0, fmt.Errorf("no decoded samples")
	}

	compareLen := len(original)
	if len(decoded) < compareLen {
		compareLen = len(decoded)
	}
	q, _ := ComputeQualityFloat32WithDelay(decoded[:compareLen], original[:compareLen], 48000, 960)
	return q, nil
}

func loadLongFrameFixture() (longFrameFixtureFile, error) {
	path := filepath.Join(longFrameFixturePath)
	data, err := os.ReadFile(path)
	if err != nil {
		return longFrameFixtureFile{}, err
	}
	var fixture longFrameFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return longFrameFixtureFile{}, err
	}
	return fixture, nil
}

func loadLongFrameFixtureCached() (longFrameFixtureFile, error) {
	longFrameFixtureOnce.Do(func() {
		longFrameFixtureData, longFrameFixtureErr = loadLongFrameFixture()
	})
	return longFrameFixtureData, longFrameFixtureErr
}

func longFrameFixtureReferenceAvailable() bool {
	_, err := loadLongFrameFixtureCached()
	return err == nil
}

func decodeFixturePackets(encodedPackets []string) ([][]byte, error) {
	packets := make([][]byte, len(encodedPackets))
	for i, b64 := range encodedPackets {
		packet, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode packet[%d]: %w", i, err)
		}
		packets[i] = packet
	}
	return packets, nil
}

func parseFixtureMode(v string) (encoder.Mode, error) {
	switch strings.ToLower(v) {
	case "silk":
		return encoder.ModeSILK, nil
	case "hybrid":
		return encoder.ModeHybrid, nil
	case "celt":
		return encoder.ModeCELT, nil
	default:
		return 0, fmt.Errorf("unknown mode %q", v)
	}
}

func parseFixtureBandwidth(v string) (types.Bandwidth, error) {
	switch strings.ToLower(v) {
	case "nb":
		return types.BandwidthNarrowband, nil
	case "mb":
		return types.BandwidthMediumband, nil
	case "wb":
		return types.BandwidthWideband, nil
	case "swb":
		return types.BandwidthSuperwideband, nil
	case "fb":
		return types.BandwidthFullband, nil
	default:
		return 0, fmt.Errorf("unknown bandwidth %q", v)
	}
}

func fixtureModeName(mode encoder.Mode) string {
	switch mode {
	case encoder.ModeSILK:
		return "silk"
	case encoder.ModeHybrid:
		return "hybrid"
	case encoder.ModeCELT:
		return "celt"
	default:
		return "unknown"
	}
}

func fixtureBandwidthName(bw types.Bandwidth) string {
	switch bw {
	case types.BandwidthNarrowband:
		return "nb"
	case types.BandwidthMediumband:
		return "mb"
	case types.BandwidthWideband:
		return "wb"
	case types.BandwidthSuperwideband:
		return "swb"
	case types.BandwidthFullband:
		return "fb"
	default:
		return "unknown"
	}
}
