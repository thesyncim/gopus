//go:build gopus_fixedpoint

package fixedpoint

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// transientSignal describes a deterministic time-domain test waveform. The
// generator fills C*length int32 celt_sig samples laid out as in[c*length+i].
type transientSignal struct {
	name string
	gen  func(rng *rand.Rand, length, c int) []int32
}

func transientSignals() []transientSignal {
	return []transientSignal{
		{
			name: "silence",
			gen: func(_ *rand.Rand, length, c int) []int32 {
				return make([]int32, c*length)
			},
		},
		{
			name: "steady_tone",
			gen: func(_ *rand.Rand, length, c int) []int32 {
				out := make([]int32, c*length)
				for ch := 0; ch < c; ch++ {
					phase := 0.13 + 0.07*float64(ch)
					for i := 0; i < length; i++ {
						v := math.Sin(2*math.Pi*phase*float64(i)) * 8.0e6
						out[ch*length+i] = int32(v)
					}
				}
				return out
			},
		},
		{
			name: "low_tone",
			gen: func(_ *rand.Rand, length, c int) []int32 {
				// Very low frequency, near-full amplitude: exercises the
				// toneishness/tone_freq false-transient guard inputs.
				out := make([]int32, c*length)
				for ch := 0; ch < c; ch++ {
					for i := 0; i < length; i++ {
						v := math.Sin(2*math.Pi*0.004*float64(i)) * 2.0e8
						out[ch*length+i] = int32(v)
					}
				}
				return out
			},
		},
		{
			name: "sharp_onset",
			gen: func(_ *rand.Rand, length, c int) []int32 {
				// Quiet then a loud burst in the second half: a clear transient.
				out := make([]int32, c*length)
				onset := length * 3 / 5
				for ch := 0; ch < c; ch++ {
					for i := 0; i < length; i++ {
						amp := 1.0e5
						if i >= onset {
							amp = 1.5e8
						}
						v := math.Sin(2*math.Pi*0.21*float64(i)) * amp
						out[ch*length+i] = int32(v)
					}
				}
				return out
			},
		},
		{
			name: "impulse",
			gen: func(_ *rand.Rand, length, c int) []int32 {
				out := make([]int32, c*length)
				for ch := 0; ch < c; ch++ {
					out[ch*length+length/2] = 4.0e8
				}
				return out
			},
		},
		{
			name: "noise",
			gen: func(rng *rand.Rand, length, c int) []int32 {
				out := make([]int32, c*length)
				for i := range out {
					out[i] = rng.Int31n(2*30000000) - 30000000
				}
				return out
			},
		},
		{
			name: "ramp_burst",
			gen: func(rng *rand.Rand, length, c int) []int32 {
				// Mild noise floor with several sample-aligned spikes to stress
				// both the high-pass filter and the envelope follower.
				out := make([]int32, c*length)
				for i := range out {
					out[i] = rng.Int31n(2*2000000) - 2000000
				}
				for ch := 0; ch < c; ch++ {
					for _, p := range []int{length / 4, length / 2, 3 * length / 4} {
						out[ch*length+p] = 2.0e8
					}
				}
				return out
			},
		},
	}
}

func TestTransientAnalysisMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x7A11E5))

	// len = N + overlap, with shortMdctSize 120 and overlap 120 for the
	// 48 kHz mode: LM0..3 -> N 120,240,480,960 -> len 240,360,600,1080.
	lengths := []int{240, 360, 600, 1080}
	channels := []int{1, 2}
	weakModes := []bool{false, true}
	// tone_freq (Q13) / toneishness (Q29) combinations, including the guard
	// region (toneishness > .98, tone_freq < 0.026).
	tones := []struct {
		freq int16
		tone int32
	}{
		{0, 0},
		{100, 600000000},  // high toneishness, low freq -> guard active
		{500, 600000000},  // high toneishness, higher freq -> guard inactive
		{4096, 100000000}, // mid freq, low toneishness
	}

	for _, sig := range transientSignals() {
		for _, length := range lengths {
			for _, c := range channels {
				for _, weak := range weakModes {
					for _, tn := range tones {
						in := sig.gen(rng, length, c)
						want, err := libopustest.ProbeCELTTransientAnalysis(in, length, c, weak, tn.freq, tn.tone)
						if err != nil {
							t.Fatalf("oracle %s len=%d C=%d weak=%v freq=%d tone=%d: %v",
								sig.name, length, c, weak, tn.freq, tn.tone, err)
						}
						got := TransientAnalysis(in, length, c, weak, tn.freq, tn.tone, nil)
						if got.IsTransient != want.IsTransient ||
							got.TFEstimate != want.TFEstimate ||
							got.TFChan != want.TFChan ||
							got.WeakTransient != want.WeakTransient {
							t.Fatalf("transient %s len=%d C=%d weak=%v freq=%d tone=%d:\n got  is_transient=%v tf_estimate=%d tf_chan=%d weak=%v\n want is_transient=%v tf_estimate=%d tf_chan=%d weak=%v",
								sig.name, length, c, weak, tn.freq, tn.tone,
								got.IsTransient, got.TFEstimate, got.TFChan, got.WeakTransient,
								want.IsTransient, want.TFEstimate, want.TFChan, want.WeakTransient)
						}
					}
				}
			}
		}
	}
}

func TestPatchTransientDecisionMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x9A7C4))

	const nbEBands = 21
	const dbShift = 24
	// Band energies are celt_glog Q24; typical magnitudes span a few dB
	// either side of zero, so sample within +/- ~30 dB.
	span := int32(30) << dbShift

	cases := []struct {
		name  string
		start int
		end   int
	}{
		{"full", 0, 21},
		{"narrow", 0, 17},
		{"offset_start", 3, 21},
	}

	randE := func(n int) []int32 {
		out := make([]int32, n)
		for i := range out {
			out[i] = rng.Int31n(2*span+1) - span
		}
		return out
	}

	for _, c := range []int{1, 2} {
		for _, tc := range cases {
			for iter := 0; iter < 64; iter++ {
				newE := randE(2 * nbEBands)
				oldE := randE(2 * nbEBands)
				// Half the time, bias newE upward to force a positive decision.
				if iter%2 == 0 {
					for i := range newE {
						newE[i] += int32(5) << dbShift
					}
				}
				want, err := libopustest.ProbeCELTPatchTransientDecision(newE, oldE, nbEBands, tc.start, tc.end, c)
				if err != nil {
					t.Fatalf("oracle patch %s C=%d iter=%d: %v", tc.name, c, iter, err)
				}
				got := PatchTransientDecision(newE, oldE, nbEBands, tc.start, tc.end, c)
				if got != want {
					t.Fatalf("patch %s C=%d iter=%d: got %v want %v", tc.name, c, iter, got, want)
				}
			}
		}
	}
}
