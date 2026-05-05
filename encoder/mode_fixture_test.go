package encoder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const modeFixturePath = "testdata/libopus_mode_fixture.json"

type modeFixtureFile struct {
	Version    int                    `json:"version"`
	SampleRate int                    `json:"sample_rate"`
	Generator  string                 `json:"generator"`
	Cases      []modeFixtureCase `json:"cases"`
	Variants   []string               `json:"variants"`
}

type modeFixtureCase struct {
	Name         string                  `json:"name"`
	Variant      string                  `json:"variant"`
	FrameSize    int                     `json:"frame_size"`
	Channels     int                     `json:"channels"`
	Bitrate      int                     `json:"bitrate"`
	Bandwidth    string                  `json:"bandwidth"`
	SignalFrames int                     `json:"signal_frames"`
	SignalSHA256 string                  `json:"signal_sha256"`
	Frames       []modeFixtureFrame `json:"frames"`
}

type modeFixtureFrame struct {
	Mode      string `json:"mode"`
	TOCConfig int    `json:"toc_config"`
}

var (
	modeFixtureOnce sync.Once
	modeFixtureData modeFixtureFile
	modeFixtureErr  error
)

func loadModeFixture() (modeFixtureFile, error) {
	modeFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(modeFixturePath))
		if err != nil {
			modeFixtureErr = err
			return
		}
		var fixture modeFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			modeFixtureErr = err
			return
		}
		if fixture.Version != 1 {
			modeFixtureErr = fmt.Errorf("unsupported mode fixture version %d", fixture.Version)
			return
		}
		if fixture.SampleRate != 48000 {
			modeFixtureErr = fmt.Errorf("unsupported mode fixture sample_rate %d", fixture.SampleRate)
			return
		}
		for i := range fixture.Cases {
			c := fixture.Cases[i]
			if c.FrameSize <= 0 || c.Channels <= 0 || c.Bitrate <= 0 || c.SignalFrames <= 0 {
				modeFixtureErr = fmt.Errorf("invalid metadata in mode fixture case[%d]", i)
				return
			}
			if len(c.Frames) != c.SignalFrames {
				modeFixtureErr = fmt.Errorf("frame count mismatch in mode fixture case[%d]", i)
				return
			}
			for j := range c.Frames {
				frame := c.Frames[j]
				if frame.TOCConfig < 0 || frame.TOCConfig > 31 {
					modeFixtureErr = fmt.Errorf("invalid toc config in mode fixture case[%d] frame[%d]", i, j)
					return
				}
				if frame.Mode != "silk" && frame.Mode != "hybrid" && frame.Mode != "celt" {
					modeFixtureErr = fmt.Errorf("invalid mode label in mode fixture case[%d] frame[%d]: %q", i, j, frame.Mode)
					return
				}
			}
		}
		modeFixtureData = fixture
	})
	return modeFixtureData, modeFixtureErr
}

func modeFixtureBandwidth(label string) (types.Bandwidth, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "narrowband":
		return types.BandwidthNarrowband, nil
	case "mediumband":
		return types.BandwidthMediumband, nil
	case "wideband":
		return types.BandwidthWideband, nil
	case "superwideband":
		return types.BandwidthSuperwideband, nil
	case "fullband":
		return types.BandwidthFullband, nil
	default:
		return 0, fmt.Errorf("unsupported bandwidth label %q", label)
	}
}

func modeFixtureLabelFromConfig(cfg int) string {
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

func TestModeFixtureParityWithLibopus(t *testing.T) {
	requireLibopusExactness(t)
	requireTestTier(t, testTierFast)

	fixture, err := loadModeFixture()
	if err != nil {
		t.Fatalf("load mode fixture: %v", err)
	}
	const maxModeMismatchRatio = 0.02
	const maxConfigMismatchRatio = 0.02

	for _, c := range fixture.Cases {
		c := c
		caseName := fmt.Sprintf("%s/%s", c.Name, c.Variant)
		t.Run(caseName, func(t *testing.T) {
			t.Parallel()

			bw, err := modeFixtureBandwidth(c.Bandwidth)
			if err != nil {
				t.Fatalf("invalid fixture bandwidth: %v", err)
			}

			totalSamples := c.SignalFrames * c.FrameSize * c.Channels
			signal, err := testsignal.GenerateEncoderSignalVariant(c.Variant, fixture.SampleRate, totalSamples, c.Channels)
			if err != nil {
				t.Fatalf("generate signal: %v", err)
			}
			clampToOpusDemoF32InPlace(signal)
			hash := testsignal.HashFloat32LE(signal)
			if hash != c.SignalSHA256 {
				t.Fatalf("signal hash mismatch: got=%s want=%s", hash, c.SignalSHA256)
			}

			enc := NewEncoder(fixture.SampleRate, c.Channels)
			enc.SetMode(ModeAuto)
			enc.SetSignalType(types.SignalAuto)
			enc.SetBandwidth(bw)
			enc.SetBitrate(c.Bitrate)
			enc.SetBitrateMode(ModeVBR)
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			enc.SetPacketLoss(0)
			enc.SetFEC(false)
			enc.SetDTX(false)

			samplesPerFrame := c.FrameSize * c.Channels
			framePCM := make([]float64, samplesPerFrame)
			modeMismatch := 0
			configMismatch := 0
			firstModeMismatch := -1
			firstModeGot := ""
			firstModeWant := ""

			for i := 0; i < c.SignalFrames; i++ {
				start := i * samplesPerFrame
				end := start + samplesPerFrame
				frame := signal[start:end]
				for j := range frame {
					framePCM[j] = float64(frame[j])
				}

				packet, err := enc.Encode(framePCM, c.FrameSize)
				if err != nil {
					t.Fatalf("encode frame %d: %v", i, err)
				}
				if len(packet) == 0 {
					t.Fatalf("empty packet at frame %d", i)
				}

				cfg := int(packet[0] >> 3)
				gotMode := modeFixtureLabelFromConfig(cfg)
				want := c.Frames[i]
				if gotMode != want.Mode {
					modeMismatch++
					if firstModeMismatch < 0 {
						firstModeMismatch = i
						firstModeGot = gotMode
						firstModeWant = want.Mode
					}
				}
				if cfg != want.TOCConfig {
					configMismatch++
				}
			}

			modeRatio := float64(modeMismatch) / float64(c.SignalFrames)
			cfgRatio := float64(configMismatch) / float64(c.SignalFrames)
			t.Logf("modeMismatch=%d/%d (%.2f%%) configMismatch=%d/%d (%.2f%%)",
				modeMismatch, c.SignalFrames, modeRatio*100,
				configMismatch, c.SignalFrames, cfgRatio*100)
			if modeRatio > maxModeMismatchRatio {
				if firstModeMismatch >= 0 {
					t.Fatalf("mode fixture parity drift: mismatches=%d/%d first_mismatch_frame=%d got=%s want=%s",
						modeMismatch, c.SignalFrames, firstModeMismatch, firstModeGot, firstModeWant)
				}
				t.Fatalf("mode fixture parity drift: mismatches=%d/%d", modeMismatch, c.SignalFrames)
			}
			if cfgRatio > maxConfigMismatchRatio {
				t.Fatalf("toc config parity drift: mismatches=%d/%d", configMismatch, c.SignalFrames)
			}
		})
	}
}

func TestModeFixtureMetadata(t *testing.T) {
	requireTestTier(t, testTierFast)

	fixture, err := loadModeFixture()
	if err != nil {
		t.Fatalf("load mode fixture: %v", err)
	}

	if len(fixture.Cases) == 0 {
		t.Fatal("mode fixture has no cases")
	}
	if !strings.Contains(strings.ToLower(fixture.Generator), "libopus-1.6.1") {
		t.Fatalf("generator must reference libopus 1.6.1, got %q", fixture.Generator)
	}
	if runtime.GOARCH == "arm64" {
		if !strings.Contains(strings.ToLower(fixture.Generator), "arm64") {
			t.Fatalf("fixture generator architecture mismatch: %q", fixture.Generator)
		}
	}

	wantVariants := testsignal.EncoderSignalVariants()
	if len(fixture.Variants) != len(wantVariants) {
		t.Fatalf("variant count mismatch: got=%d want=%d", len(fixture.Variants), len(wantVariants))
	}
	for i := range wantVariants {
		if fixture.Variants[i] != wantVariants[i] {
			t.Fatalf("variant[%d] mismatch: got=%q want=%q", i, fixture.Variants[i], wantVariants[i])
		}
	}
}
