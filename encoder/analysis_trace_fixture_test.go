package encoder

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
)

const analysisTraceFixturePath = "testdata/libopus_analysis_trace_fixture.json"

type analysisTraceFixtureFile struct {
	Version    int                        `json:"version"`
	SampleRate int                        `json:"sample_rate"`
	Generator  string                     `json:"generator"`
	Cases      []analysisTraceFixtureCase `json:"cases"`
}

type analysisTraceFixtureCase struct {
	Name         string                     `json:"name"`
	Variant      string                     `json:"variant"`
	FrameSize    int                        `json:"frame_size"`
	Channels     int                        `json:"channels"`
	Bitrate      int                        `json:"bitrate"`
	SignalFrames int                        `json:"signal_frames"`
	SignalSHA256 string                     `json:"signal_sha256"`
	Frames       []analysisTraceFixtureInfo `json:"frames"`
}

type analysisTraceFixtureInfo struct {
	Valid         bool    `json:"valid"`
	Tonality      float32 `json:"tonality"`
	TonalitySlope float32 `json:"tonality_slope"`
	Noisiness     float32 `json:"noisiness"`
	Activity      float32 `json:"activity"`
	MusicProb     float32 `json:"music_prob"`
	MusicProbMin  float32 `json:"music_prob_min"`
	MusicProbMax  float32 `json:"music_prob_max"`
	Bandwidth     int     `json:"bandwidth"`
	ActivityProb  float32 `json:"activity_probability"`
	MaxPitchRatio float32 `json:"max_pitch_ratio"`
	LeakBoostB64  string  `json:"leak_boost_b64"`
}

var (
	analysisTraceFixtureOnce sync.Once
	analysisTraceFixtureData analysisTraceFixtureFile
	analysisTraceFixtureErr  error
)

func loadAnalysisTraceFixture() (analysisTraceFixtureFile, error) {
	analysisTraceFixtureOnce.Do(func() {
		data, err := os.ReadFile(filepath.Join(analysisTraceFixturePath))
		if err != nil {
			analysisTraceFixtureErr = err
			return
		}
		var fixture analysisTraceFixtureFile
		if err := json.Unmarshal(data, &fixture); err != nil {
			analysisTraceFixtureErr = err
			return
		}
		if fixture.Version != 1 {
			analysisTraceFixtureErr = fmt.Errorf("unsupported analysis fixture version %d", fixture.Version)
			return
		}
		if fixture.SampleRate != 48000 {
			analysisTraceFixtureErr = fmt.Errorf("unsupported analysis fixture sample_rate %d", fixture.SampleRate)
			return
		}
		for i := range fixture.Cases {
			c := fixture.Cases[i]
			if c.FrameSize <= 0 || c.Channels <= 0 || c.SignalFrames <= 0 {
				analysisTraceFixtureErr = fmt.Errorf("invalid metadata in fixture case[%d]", i)
				return
			}
			if len(c.Frames) != c.SignalFrames {
				analysisTraceFixtureErr = fmt.Errorf("frame count mismatch in fixture case[%d]", i)
				return
			}
			for j := range c.Frames {
				if _, err := base64.StdEncoding.DecodeString(c.Frames[j].LeakBoostB64); err != nil {
					analysisTraceFixtureErr = fmt.Errorf("invalid leak_boost_b64 case[%d] frame[%d]: %w", i, j, err)
					return
				}
			}
		}
		analysisTraceFixtureData = fixture
	})
	return analysisTraceFixtureData, analysisTraceFixtureErr
}

func TestAnalysisTraceFixtureParityWithLibopus(t *testing.T) {
	requireTestTier(t, testTierFast)

	fixture, err := loadAnalysisTraceFixture()
	if err != nil {
		t.Fatalf("load analysis trace fixture: %v", err)
	}

	const (
		floatTol         = 0.08
		musicBoundTol    = 0.10
		pitchRatioTol    = 0.18
		maxBadFrameRatio = 0.15
	)

	for _, c := range fixture.Cases {
		c := c
		caseName := fmt.Sprintf("%s/%s", c.Name, c.Variant)
		t.Run(caseName, func(t *testing.T) {
			t.Parallel()

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

			an := NewTonalityAnalysisState(fixture.SampleRate)
			an.SetLSBDepth(24)

			samplesPerFrame := c.FrameSize * c.Channels
			badFrames := 0
			for i := 0; i < c.SignalFrames; i++ {
				start := i * samplesPerFrame
				end := start + samplesPerFrame
				info := an.RunAnalysis(signal[start:end], c.FrameSize, c.Channels)
				want := c.Frames[i]

				if info.Valid != want.Valid {
					badFrames++
					continue
				}
				if !want.Valid {
					continue
				}

				mismatch := false
				mismatch = mismatch || absf(info.Tonality-want.Tonality) > floatTol
				mismatch = mismatch || absf(info.TonalitySlope-want.TonalitySlope) > floatTol
				mismatch = mismatch || absf(info.NoisySpeech-want.Noisiness) > floatTol
				mismatch = mismatch || absf(info.Activity-want.Activity) > floatTol
				mismatch = mismatch || absf(info.VADProb-want.ActivityProb) > floatTol
				mismatch = mismatch || absf(info.MusicProb-want.MusicProb) > floatTol
				mismatch = mismatch || absf(info.MusicProbMin-want.MusicProbMin) > musicBoundTol
				mismatch = mismatch || absf(info.MusicProbMax-want.MusicProbMax) > musicBoundTol
				mismatch = mismatch || absf(info.MaxPitchRatio-want.MaxPitchRatio) > pitchRatioTol
				mismatch = mismatch || (info.BandwidthIndex != want.Bandwidth)

				wantLeak, err := base64.StdEncoding.DecodeString(want.LeakBoostB64)
				if err != nil {
					t.Fatalf("decode leak boost fixture: %v", err)
				}
				for b := 0; b < 19 && b < len(wantLeak); b++ {
					if diff := int(info.LeakBoost[b]) - int(wantLeak[b]); diff < -3 || diff > 3 {
						mismatch = true
						break
					}
				}

				if mismatch {
					badFrames++
				}
			}

			if c.SignalFrames == 0 {
				t.Fatalf("invalid fixture case: zero frames")
			}
			badRatio := float64(badFrames) / float64(c.SignalFrames)
			t.Logf("badFrames=%d/%d (%.2f%%)", badFrames, c.SignalFrames, badRatio*100)
			if badRatio > maxBadFrameRatio {
				t.Fatalf("analysis parity drift: bad frame ratio %.2f%% > %.2f%%", badRatio*100, maxBadFrameRatio*100)
			}
		})
	}
}

func TestAnalysisTraceFixtureMetadata(t *testing.T) {
	requireTestTier(t, testTierFast)

	fixture, err := loadAnalysisTraceFixture()
	if err != nil {
		t.Fatalf("load analysis trace fixture: %v", err)
	}

	if len(fixture.Cases) == 0 {
		t.Fatal("analysis trace fixture has no cases")
	}

	if !strings.Contains(strings.ToLower(fixture.Generator), "libopus-1.6.1") {
		t.Fatalf("generator must reference libopus 1.6.1, got %q", fixture.Generator)
	}
	if runtime.GOARCH == "arm64" {
		if !strings.Contains(strings.ToLower(fixture.Generator), "arm64") {
			t.Fatalf("fixture generator architecture mismatch: %q", fixture.Generator)
		}
	}
}

func TestAnalysisTraceFixtureProfileCoverage(t *testing.T) {
	requireTestTier(t, testTierFast)

	fixture, err := loadAnalysisTraceFixture()
	if err != nil {
		t.Fatalf("load analysis trace fixture: %v", err)
	}

	want := map[string]struct{}{
		"CELT-FB-10ms-mono-64k":     {},
		"CELT-FB-20ms-mono-64k":     {},
		"CELT-FB-20ms-stereo-128k":  {},
		"HYBRID-FB-10ms-mono-64k":   {},
		"HYBRID-FB-20ms-mono-64k":   {},
		"HYBRID-FB-20ms-stereo-96k": {},
		"HYBRID-FB-60ms-mono-64k":   {},
		"HYBRID-SWB-10ms-mono-48k":  {},
		"HYBRID-SWB-20ms-mono-48k":  {},
		"HYBRID-SWB-40ms-mono-48k":  {},
		"SILK-MB-20ms-mono-24k":     {},
		"SILK-NB-10ms-mono-16k":     {},
		"SILK-NB-20ms-mono-16k":     {},
		"SILK-NB-40ms-mono-16k":     {},
		"SILK-WB-10ms-mono-32k":     {},
		"SILK-WB-20ms-mono-32k":     {},
		"SILK-WB-20ms-stereo-48k":   {},
		"SILK-WB-40ms-mono-32k":     {},
		"SILK-WB-60ms-mono-32k":     {},
	}

	got := make(map[string]struct{}, len(want))
	for _, c := range fixture.Cases {
		got[c.Name] = struct{}{}
	}

	for name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("missing fixture profile %q", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Fatalf("unexpected fixture profile %q", name)
		}
	}
}

func clampToOpusDemoF32InPlace(in []float32) {
	const inv24 = 1.0 / 8388608.0
	for i, s := range in {
		q := float32(int64(0.5 + float64(s)*8388608.0))
		in[i] = q * float32(inv24)
	}
}

func absf(v float32) float64 {
	if v < 0 {
		v = -v
	}
	return float64(v)
}

func requireTestTier(t *testing.T, tier string) {
	t.Helper()
	if testTier() != tier {
		t.Skipf("test tier %q required", tier)
	}
}

func testTier() string {
	if v := strings.TrimSpace(os.Getenv("GOPUS_TEST_TIER")); v != "" {
		return strings.ToLower(v)
	}
	return testTierFast
}

const (
	testTierFast = "fast"
)
