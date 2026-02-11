package celt

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"testing"
)

const prefilterLibopusFixturePath = "testdata/prefilter_libopus_ref_fixture.json"

type prefilterFixtureFile struct {
	Version int                  `json:"version"`
	Cases   []prefilterFixtureCase `json:"cases"`
}

type prefilterFixtureCase struct {
	ID               int     `json:"id"`
	Channels         int     `json:"channels"`
	FrameSize        int     `json:"frame_size"`
	Complexity       int     `json:"complexity"`
	PrevPeriod       int     `json:"prev_period"`
	PrevGain         float64 `json:"prev_gain"`
	PrevTapset       int     `json:"prev_tapset"`
	Tapset           int     `json:"tapset"`
	Enabled          bool    `json:"enabled"`
	TFEstimate       float64 `json:"tf_estimate"`
	NBAvailableBytes int     `json:"nb_available_bytes"`
	ToneFreq         float64 `json:"tone_freq"`
	Toneishness      float64 `json:"toneishness"`
	MaxPitchRatio    float64 `json:"max_pitch_ratio"`
	SignalSeed       int64   `json:"signal_seed"`
	ExpectedOn       bool    `json:"expected_on"`
	ExpectedPitch    int     `json:"expected_pitch"`
	ExpectedQG       int     `json:"expected_qg"`
	ExpectedGain     float64 `json:"expected_gain"`
}

func generatePrefilterFixtureCases(masterSeed int64, iters int) []prefilterFixtureCase {
	rng := rand.New(rand.NewSource(masterSeed))
	frameSizes := []int{120, 240, 480, 960}
	cases := make([]prefilterFixtureCase, 0, iters)
	for i := 0; i < iters; i++ {
		channels := 1
		if rng.Intn(2) == 1 {
			channels = 2
		}
		frameSize := frameSizes[rng.Intn(len(frameSizes))]
		toneFreq := -1.0
		if rng.Intn(4) != 0 {
			toneFreq = rng.Float64() * math.Pi
		}
		cases = append(cases, prefilterFixtureCase{
			ID:               i,
			Channels:         channels,
			FrameSize:        frameSize,
			Complexity:       rng.Intn(11),
			// Keep previous period in the same safe range as libopus run_prefilter state.
			PrevPeriod:       rng.Intn(combFilterMaxPeriod - 1), // [0, COMBFILTER_MAXPERIOD-2]
			PrevGain:         rng.Float64() * 0.8,
			PrevTapset:       rng.Intn(3),
			Tapset:           rng.Intn(3),
			Enabled:          rng.Intn(2) == 1,
			TFEstimate:       rng.Float64(),
			NBAvailableBytes: 5 + rng.Intn(90),
			ToneFreq:         toneFreq,
			Toneishness:      rng.Float64(),
			MaxPitchRatio:    rng.Float64(),
			SignalSeed:       rng.Int63(),
		})
	}
	return cases
}

func buildPrefilterFixtureSignal(seed int64, channels, frameSize int) (prefilterMem, preemph, pre []float64) {
	rng := rand.New(rand.NewSource(seed))
	prefilterMem = make([]float64, combFilterMaxPeriod*channels)
	for i := range prefilterMem {
		prefilterMem[i] = (rng.Float64()*2 - 1) * CELTSigScale
	}

	preemph = make([]float64, frameSize*channels)
	for i := range preemph {
		preemph[i] = (rng.Float64()*2 - 1) * CELTSigScale
	}

	pre = make([]float64, channels*(combFilterMaxPeriod+frameSize))
	perChanLen := combFilterMaxPeriod + frameSize
	for ch := 0; ch < channels; ch++ {
		copy(pre[ch*perChanLen:ch*perChanLen+combFilterMaxPeriod],
			prefilterMem[ch*combFilterMaxPeriod:(ch+1)*combFilterMaxPeriod])
		for i := 0; i < frameSize; i++ {
			pre[ch*perChanLen+combFilterMaxPeriod+i] = preemph[i*channels+ch]
		}
	}
	return prefilterMem, preemph, pre
}

func loadPrefilterLibopusFixture() (prefilterFixtureFile, error) {
	data, err := os.ReadFile(prefilterLibopusFixturePath)
	if err != nil {
		return prefilterFixtureFile{}, err
	}
	var fixture prefilterFixtureFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		return prefilterFixtureFile{}, err
	}
	return fixture, nil
}

func TestRunPrefilterParityAgainstLibopusFixture(t *testing.T) {
	fixture, err := loadPrefilterLibopusFixture()
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported fixture version %d", fixture.Version)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("empty fixture")
	}

	var (
		onMismatch    int
		pitchMismatch int
		qgMismatch    int
		gainMismatch  int
		maxPitchDiff  int
		maxGainDiff   float64
	)

	for _, c := range fixture.Cases {
		enc := NewEncoder(c.Channels)
		enc.prefilterPeriod = c.PrevPeriod
		enc.prefilterGain = c.PrevGain
		enc.prefilterTapset = c.PrevTapset
		enc.complexity = c.Complexity

		prefilterMem, preemph, _ := buildPrefilterFixtureSignal(c.SignalSeed, c.Channels, c.FrameSize)
		copy(enc.prefilterMem, prefilterMem)
		goInput := make([]float64, len(preemph))
		copy(goInput, preemph)

		got := enc.runPrefilter(goInput, c.FrameSize, c.Tapset, c.Enabled, c.TFEstimate, c.NBAvailableBytes, c.ToneFreq, c.Toneishness, c.MaxPitchRatio)

		if got.on != c.ExpectedOn {
			onMismatch++
		}
		pd := int(math.Abs(float64(got.pitch - c.ExpectedPitch)))
		if pd > 1 {
			pitchMismatch++
			if pd > maxPitchDiff {
				maxPitchDiff = pd
			}
		}
		if got.qg != c.ExpectedQG {
			qgMismatch++
		}
		gd := math.Abs(got.gain - c.ExpectedGain)
		if gd > 1e-5 {
			gainMismatch++
			if gd > maxGainDiff {
				maxGainDiff = gd
			}
		}
	}

	t.Logf("cases=%d onMismatch=%d pitchMismatch=%d qgMismatch=%d gainMismatch=%d maxPitchDiff=%d maxGainDiff=%.6f",
		len(fixture.Cases), onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)

	if onMismatch != 0 || pitchMismatch != 0 || qgMismatch != 0 || gainMismatch != 0 {
		t.Fatalf("runPrefilter libopus fixture mismatch: on=%d pitch=%d qg=%d gain=%d maxPitchDiff=%d maxGainDiff=%.6f",
			onMismatch, pitchMismatch, qgMismatch, gainMismatch, maxPitchDiff, maxGainDiff)
	}
}
