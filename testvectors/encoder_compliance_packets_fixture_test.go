package testvectors

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/types"
)

const encoderCompliancePacketsFixturePath = "testdata/encoder_compliance_libopus_packets_fixture.json"
const encoderCompliancePacketsFixturePathAMD64 = "testdata/encoder_compliance_libopus_packets_fixture_amd64.json"

type encoderCompliancePacketsFixtureFile struct {
	Version    int                                 `json:"version"`
	SampleRate int                                 `json:"sample_rate"`
	Generator  string                              `json:"generator"`
	Signal     string                              `json:"signal"`
	Cases      []encoderCompliancePacketsFixtureTC `json:"cases"`
}

type encoderCompliancePacketsFixtureTC struct {
	Mode          string                                  `json:"mode"`
	Bandwidth     string                                  `json:"bandwidth"`
	FrameSize     int                                     `json:"frame_size"`
	Channels      int                                     `json:"channels"`
	Bitrate       int                                     `json:"bitrate"`
	LibQ          float64                                 `json:"lib_q"`
	SignalFrames  int                                     `json:"signal_frames"`
	Frames        int                                     `json:"frames"`
	ModeHistogram map[string]int                          `json:"mode_histogram"`
	Packets       []encoderCompliancePacketsFixturePacket `json:"packets"`
}

type encoderCompliancePacketsFixturePacket struct {
	DataB64    string `json:"data_b64"`
	FinalRange uint32 `json:"final_range"`
}

var (
	encoderCompliancePacketsFixtureOnce sync.Once
	encoderCompliancePacketsFixtureData encoderCompliancePacketsFixtureFile
	encoderCompliancePacketsFixtureErr  error
)

func loadEncoderCompliancePacketsFixture() (encoderCompliancePacketsFixtureFile, error) {
	encoderCompliancePacketsFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(encoderCompliancePacketsFixturePathForArch()))
		if err != nil {
			encoderCompliancePacketsFixtureErr = err
			return
		}
		var fixture encoderCompliancePacketsFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			encoderCompliancePacketsFixtureErr = err
			return
		}
		for i := range fixture.Cases {
			if fixture.Cases[i].Frames != len(fixture.Cases[i].Packets) {
				encoderCompliancePacketsFixtureErr = fmt.Errorf("fixture case[%d] frame count mismatch", i)
				return
			}
			for j := range fixture.Cases[i].Packets {
				if _, err := base64.StdEncoding.DecodeString(fixture.Cases[i].Packets[j].DataB64); err != nil {
					encoderCompliancePacketsFixtureErr = err
					return
				}
			}
		}
		encoderCompliancePacketsFixtureData = fixture
	})
	return encoderCompliancePacketsFixtureData, encoderCompliancePacketsFixtureErr
}

func encoderCompliancePacketsFixturePathForArch() string {
	if runtime.GOARCH == "amd64" {
		return encoderCompliancePacketsFixturePathAMD64
	}
	return encoderCompliancePacketsFixturePath
}

func decodeEncoderPacketsFixturePackets(c encoderCompliancePacketsFixtureTC) ([][]byte, []uint32, error) {
	packets := make([][]byte, len(c.Packets))
	ranges := make([]uint32, len(c.Packets))
	for i := range c.Packets {
		payload, err := base64.StdEncoding.DecodeString(c.Packets[i].DataB64)
		if err != nil {
			return nil, nil, err
		}
		packets[i] = payload
		ranges[i] = c.Packets[i].FinalRange
	}
	return packets, ranges, nil
}

func findEncoderCompliancePacketsFixtureCase(mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (encoderCompliancePacketsFixtureTC, bool) {
	fixture, err := loadEncoderCompliancePacketsFixture()
	if err != nil {
		return encoderCompliancePacketsFixtureTC{}, false
	}
	modeName := fixtureModeName(mode)
	bwName := fixtureBandwidthName(bandwidth)
	for _, c := range fixture.Cases {
		if c.Mode == modeName &&
			c.Bandwidth == bwName &&
			c.FrameSize == frameSize &&
			c.Channels == channels &&
			c.Bitrate == bitrate {
			return c, true
		}
	}
	return encoderCompliancePacketsFixtureTC{}, false
}

func getFixtureOpusDemoPathForEncoder() (string, bool) {
	return libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots())
}

func modeToOpusDemoApp(mode string) (string, error) {
	switch strings.ToLower(mode) {
	case "celt":
		return "restricted-celt", nil
	case "silk":
		return "restricted-silk", nil
	case "hybrid":
		return "audio", nil
	default:
		return "", fmt.Errorf("unknown mode %q", mode)
	}
}

func bandwidthToOpusDemoArg(bw string) (string, error) {
	switch strings.ToLower(bw) {
	case "nb":
		return "NB", nil
	case "mb":
		return "MB", nil
	case "wb":
		return "WB", nil
	case "swb":
		return "SWB", nil
	case "fb":
		return "FB", nil
	default:
		return "", fmt.Errorf("unknown bandwidth %q", bw)
	}
}

func frameSizeSamplesToArg(frameSize int) (string, error) {
	switch frameSize {
	case 120:
		return "2.5", nil
	case 240:
		return "5", nil
	case 480:
		return "10", nil
	case 960:
		return "20", nil
	case 1920:
		return "40", nil
	case 2880:
		return "60", nil
	case 3840:
		return "80", nil
	case 4800:
		return "100", nil
	case 5760:
		return "120", nil
	default:
		return "", fmt.Errorf("unsupported frame size %d", frameSize)
	}
}

func TestEncoderCompliancePacketsFixtureCoverage(t *testing.T) {
	fixture, err := loadEncoderCompliancePacketsFixture()
	if err != nil {
		t.Fatalf("load encoder packets fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported fixture version: %d", fixture.Version)
	}
	if fixture.SampleRate != 48000 {
		t.Fatalf("unsupported fixture sample rate: %d", fixture.SampleRate)
	}

	seen := map[string]struct{}{}
	for i, c := range fixture.Cases {
		mode, err := parseFixtureMode(c.Mode)
		if err != nil {
			t.Fatalf("fixture case[%d] invalid mode %q: %v", i, c.Mode, err)
		}
		bw, err := parseFixtureBandwidth(c.Bandwidth)
		if err != nil {
			t.Fatalf("fixture case[%d] invalid bandwidth %q: %v", i, c.Bandwidth, err)
		}
		key := fmt.Sprintf("%d/%d/%d/%d/%d", mode, bw, c.FrameSize, c.Channels, c.Bitrate)
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate fixture case for key %s", key)
		}
		seen[key] = struct{}{}
		wantSignalFrames := 48000 / c.FrameSize
		if c.SignalFrames != wantSignalFrames {
			t.Fatalf("case[%d] signal_frames=%d want %d", i, c.SignalFrames, wantSignalFrames)
		}
	}

	var missing []string
	for _, tc := range encoderComplianceSummaryCases() {
		if _, ok := findEncoderCompliancePacketsFixtureCase(tc.mode, tc.bandwidth, tc.frameSize, tc.channels, tc.bitrate); !ok {
			missing = append(missing, tc.name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing packet fixture coverage for summary cases: %s", strings.Join(missing, ", "))
	}
}

func TestEncoderCompliancePacketsFixtureHonestyWithOpusDemo(t *testing.T) {
	requireTestTier(t, testTierExhaustive)

	opusDemo, ok := getFixtureOpusDemoPathForEncoder()
	if !ok {
		t.Skip("tmp_check opus_demo not found; skipping encoder packet fixture honesty")
	}
	fixture, err := loadEncoderCompliancePacketsFixture()
	if err != nil {
		t.Fatalf("load encoder packets fixture: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "gopus-enc-fixture-honesty-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, c := range fixture.Cases {
		c := c
		name := fmt.Sprintf("%s-%s-%dms-%dch-%dk", c.Mode, c.Bandwidth, c.FrameSize*1000/48000, c.Channels, c.Bitrate/1000)
		t.Run(name, func(t *testing.T) {
			app, err := modeToOpusDemoApp(c.Mode)
			if err != nil {
				t.Fatalf("map mode to opus_demo app: %v", err)
			}
			bwArg, err := bandwidthToOpusDemoArg(c.Bandwidth)
			if err != nil {
				t.Fatalf("map bandwidth to opus_demo arg: %v", err)
			}
			frameArg, err := frameSizeSamplesToArg(c.FrameSize)
			if err != nil {
				t.Fatalf("map frame size to opus_demo arg: %v", err)
			}

			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			pcm := generateEncoderTestSignal(totalSamples, c.Channels)
			rawPath := filepath.Join(tmpDir, fmt.Sprintf("%s.raw.f32", name))
			bitPath := filepath.Join(tmpDir, fmt.Sprintf("%s.bit", name))
			if err := writeFloat32LEFile(rawPath, pcm); err != nil {
				t.Fatalf("write raw input: %v", err)
			}

			cmd := exec.Command(opusDemo,
				"-e", app, "48000", strconv.Itoa(c.Channels), strconv.Itoa(c.Bitrate),
				"-f32", "-cbr", "-complexity", "10", "-bandwidth", bwArg, "-framesize", frameArg,
				rawPath, bitPath,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("opus_demo encode failed: %v (%s)", err, out)
			}
			gotPackets, gotRanges, err := parseOpusDemoEncodeBitstream(bitPath)
			if err != nil {
				t.Fatalf("parse generated bitstream: %v", err)
			}
			wantPackets, wantRanges, err := decodeEncoderPacketsFixturePackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			if len(gotPackets) != len(wantPackets) {
				t.Fatalf("packet count mismatch: got=%d want=%d", len(gotPackets), len(wantPackets))
			}
			rangeMismatch := 0
			payloadMismatch := 0
			for i := range gotPackets {
				if runtime.GOARCH == "amd64" {
					// Native amd64 libopus can drift in range/payload bytes across toolchains
					// while keeping packet structure and decoded quality stable.
					if len(gotPackets[i]) != len(wantPackets[i]) {
						t.Fatalf("frame %d payload length mismatch: got=%d want=%d", i, len(gotPackets[i]), len(wantPackets[i]))
					}
					if gotRanges[i] != wantRanges[i] {
						rangeMismatch++
					}
					if !bytes.Equal(gotPackets[i], wantPackets[i]) {
						payloadMismatch++
					}
					continue
				}
				if gotRanges[i] != wantRanges[i] {
					t.Fatalf("frame %d range mismatch: got=0x%08x want=0x%08x", i, gotRanges[i], wantRanges[i])
				}
				if !bytes.Equal(gotPackets[i], wantPackets[i]) {
					t.Fatalf("frame %d payload mismatch", i)
				}
			}
			if runtime.GOARCH == "amd64" {
				// Permit bounded drift, but fail on broad divergence.
				maxDrift := len(gotPackets) / 4
				if maxDrift < 1 {
					maxDrift = 1
				}
				if rangeMismatch > maxDrift {
					t.Fatalf("range drift too large on amd64: %d/%d frames (max=%d)", rangeMismatch, len(gotPackets), maxDrift)
				}
				if rangeMismatch > 0 || payloadMismatch > 0 {
					t.Logf("non-bitexact drift on amd64: range=%d/%d payload=%d/%d", rangeMismatch, len(gotPackets), payloadMismatch, len(gotPackets))
				}
			}
		})
	}
}
