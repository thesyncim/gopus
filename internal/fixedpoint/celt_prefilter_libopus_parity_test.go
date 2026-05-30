//go:build gopus_fixedpoint

package fixedpoint

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// prefilterSignal builds a deterministic celt_sig (int32, SIG_SHIFT=12) analysis
// buffer of length max_period+N: a periodic tone at the given lag plus noise,
// scaled into the regime the prefilter pitch analysis actually sees.
func prefilterSignal(rng *rand.Rand, length, lag int, amp float64) []int32 {
	out := make([]int32, length)
	for i := range out {
		v := amp*math.Sin(2*math.Pi*float64(i)/float64(lag)) +
			0.15*amp*math.Sin(2*math.Pi*float64(i)/float64(lag/3+1)) +
			0.1*amp*(rng.Float64()*2-1)
		// Scale to the int32 signal domain (Q12) used by celt_sig.
		out[i] = int32(v * 4096)
	}
	return out
}

func TestPrefilterAnalysisMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)

	const n = 960
	maxPeriod := combFilterMaxPeriod

	type tc struct {
		name             string
		cc               int
		complexity       int
		lossRate         int
		nbAvailableBytes int
		prefilterPeriod  int
		prefilterTapset  int
		prefilterGain    int16
		tfEstimate       int16
		enabled          bool
		hybrid           bool
		tell             int
		totalBits        int
		toneFreq         int16
		toneishness      int32
		analysisValid    bool
		maxPitchRatio    float32
		lag              int
		amp              float64
	}

	cases := []tc{
		{name: "mono_strong_pitch", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 12000, tfEstimate: 4000, enabled: true, totalBits: 8000, lag: 220, amp: 5.0},
		{name: "mono_low_complexity", cc: 1, complexity: 3, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 0, enabled: true, totalBits: 8000, lag: 200, amp: 5.0},
		{name: "mono_disabled", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 64, prefilterGain: 0, enabled: false, totalBits: 8000, lag: 150, amp: 4.0},
		{name: "mono_few_bytes", cc: 1, complexity: 10, nbAvailableBytes: 20, prefilterPeriod: 80, prefilterGain: 5000, enabled: true, totalBits: 2000, lag: 120, amp: 6.0},
		{name: "mono_high_prev_gain", cc: 1, complexity: 10, nbAvailableBytes: 300, prefilterPeriod: 300, prefilterGain: 20000, enabled: true, totalBits: 8000, lag: 305, amp: 5.0},
		{name: "mono_lossy", cc: 1, complexity: 10, lossRate: 5, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 12000, enabled: true, totalBits: 8000, lag: 220, amp: 5.0},
		{name: "mono_transient_disc", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 64, prefilterGain: 5000, tfEstimate: 16200, enabled: true, totalBits: 8000, lag: 500, amp: 5.0},
		{name: "mono_analysis", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 12000, enabled: true, totalBits: 8000, analysisValid: true, maxPitchRatio: 0.6, lag: 220, amp: 5.0},
		{name: "mono_tone", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 100, prefilterGain: 8000, enabled: true, totalBits: 8000, toneFreq: 800, toneishness: 540000000, lag: 100, amp: 5.0},
		{name: "mono_hybrid", cc: 1, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 0, enabled: true, hybrid: true, totalBits: 8000, lag: 220, amp: 1.5},
		{name: "stereo_strong_pitch", cc: 2, complexity: 10, nbAvailableBytes: 200, prefilterPeriod: 200, prefilterGain: 12000, tfEstimate: 4000, enabled: true, totalBits: 8000, lag: 220, amp: 5.0},
		{name: "stereo_low_gain", cc: 2, complexity: 8, nbAvailableBytes: 200, prefilterPeriod: 300, prefilterGain: 3000, enabled: true, totalBits: 8000, lag: 305, amp: 2.0},
	}

	for ci, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(0x9E3779B9 + ci)))
			pre := make([][]int32, 2)
			pre[0] = prefilterSignal(rng, maxPeriod+n, c.lag, c.amp)
			if c.cc == 2 {
				pre[1] = prefilterSignal(rng, maxPeriod+n, c.lag+7, c.amp*0.8)
			} else {
				pre[1] = nil
			}

			want, err := libopustest.ProbeCELTPrefilter(pre, libopustest.CELTPrefilterParams{
				CC:               c.cc,
				N:                n,
				Complexity:       c.complexity,
				LossRate:         c.lossRate,
				NbAvailableBytes: c.nbAvailableBytes,
				PrefilterPeriod:  c.prefilterPeriod,
				PrefilterTapset:  c.prefilterTapset,
				Enabled:          c.enabled,
				Hybrid:           c.hybrid,
				Tell:             c.tell,
				TotalBits:        c.totalBits,
				PrefilterGain:    c.prefilterGain,
				TFEstimate:       c.tfEstimate,
				ToneFreq:         c.toneFreq,
				Toneishness:      c.toneishness,
				AnalysisValid:    c.analysisValid,
				MaxPitchRatio:    c.maxPitchRatio,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT fixed prefilter", err)
			}

			got := PrefilterAnalysis(pre, c.cc, n, PrefilterParams{
				PrefilterPeriod:         c.prefilterPeriod,
				PrefilterGain:           c.prefilterGain,
				PrefilterTapset:         c.prefilterTapset,
				Enabled:                 c.enabled,
				Complexity:              c.complexity,
				LossRate:                c.lossRate,
				TFEstimate:              c.tfEstimate,
				NbAvailableBytes:        c.nbAvailableBytes,
				PrefilterTapsetDecision: c.prefilterTapset,
				ToneFreq:                c.toneFreq,
				Toneishness:             c.toneishness,
				AnalysisValid:           c.analysisValid,
				MaxPitchRatio:           c.maxPitchRatio,
			})

			if got.PitchIndex != want.PitchIndex {
				t.Fatalf("pitch_index=%d want %d", got.PitchIndex, want.PitchIndex)
			}
			if got.Gain != want.Gain {
				t.Fatalf("gain=%d want %d (pitch=%d)", got.Gain, want.Gain, got.PitchIndex)
			}
			if got.QG != want.QG {
				t.Fatalf("qg=%d want %d", got.QG, want.QG)
			}
			if got.PFOn != want.PFOn {
				t.Fatalf("pf_on=%v want %v", got.PFOn, want.PFOn)
			}
			if got.Tapset != want.Tapset {
				t.Fatalf("tapset=%d want %d", got.Tapset, want.Tapset)
			}

			// Emit the post-filter parameters and compare the produced bytes.
			// Size the buffer to the libopus byte count so the finalised
			// range/raw layout merges identically (matching the tight-buffer
			// ec_enc the reference uses).
			buf := make([]byte, len(want.Bytes))
			enc := &rangecoding.Encoder{}
			enc.Init(buf)
			EmitPrefilterParams(enc, got, c.hybrid, c.tell, c.totalBits)
			gotBytes := enc.Done()
			if len(gotBytes) != len(want.Bytes) {
				t.Fatalf("emitted %d bytes want %d (% x vs % x)", len(gotBytes), len(want.Bytes), gotBytes, want.Bytes)
			}
			for i := range gotBytes {
				if gotBytes[i] != want.Bytes[i] {
					t.Fatalf("emitted byte[%d]=%#02x want %#02x (% x vs % x)", i, gotBytes[i], want.Bytes[i], gotBytes, want.Bytes)
				}
			}
		})
	}
}
