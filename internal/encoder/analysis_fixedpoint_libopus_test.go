//go:build gopus_fixed_point

package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedPointTonalityAnalysisStagesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, fs := range []int{48000, 24000, 16000} {
		for _, channels := range []int{1, 2} {
			for _, frameMS := range []int{5, 20, 40, 60} {
				frameSize := fs * frameMS / 1000
				const frameCount = 12
				pcm := make([]float32, frameSize*frameCount*channels)
				for frame := range frameCount {
					for i := range frameSize {
						time := float64(frame*frameSize+i) / float64(fs)
						for ch := range channels {
							sample := 0.23*math.Sin(2*math.Pi*(173+float64(ch)*91)*time) +
								0.07*math.Sin(2*math.Pi*(2711-float64(ch)*137)*time+0.19*float64(frame))
							pcm[(frame*frameSize+i)*channels+ch] = float32(sample)
						}
					}
				}

				t.Run(fmt.Sprintf("%s/%dms", analysisName(fs, channels), frameMS), func(t *testing.T) {
					want := runLibopusFixedAnalysisOracle(t, fs, channels, frameSize, 24, pcm)
					an := NewTonalityAnalysisState(fs)
					an.SetLSBDepth(24)
					compareSequence := func(label string) {
						t.Helper()
						for frame := range frameCount {
							input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
							gotInfo := analysisInfoToOracle(an.RunAnalysis(input, frameSize, channels))
							if len(want[frame].inmem) != AnalysisBufSize {
								t.Fatalf("%s frame %d fixed oracle inmem length=%d, want %d", label, frame, len(want[frame].inmem), AnalysisBufSize)
							}
							for i, expected := range want[frame].downmixState {
								if got := an.fixed.downmixState[i]; got != expected {
									t.Fatalf("%s frame %d downmix_state[%d]=%d, fixed C=%d", label, frame, i, got, expected)
								}
							}
							for i, expected := range want[frame].inmem {
								if got := an.fixed.inMem[i]; got != expected {
									t.Fatalf("%s frame %d inmem[%d]=%d, fixed C=%d", label, frame, i, got, expected)
								}
							}
							if d := diffAnalysisInfo(gotInfo, want[frame].ret); d != "" {
								t.Fatalf("%s frame %d AnalysisInfo differs: %s%s", label, frame, d, analysisStateDiff(an, want[frame]))
							}
							if d := analysisStateDiff(an, want[frame]); d != "" {
								t.Fatalf("%s frame %d fixed analysis state differs:%s", label, frame, d)
							}
						}
					}
					compareSequence("initial")

					an.Reset()
					compareSequence("after reset")

					input := pcm[(frameCount-1)*frameSize*channels:]
					if allocs := testing.AllocsPerRun(20, func() {
						an.RunAnalysis(input, frameSize, channels)
					}); allocs != 0 {
						t.Fatalf("warmed fixed analysis allocated %g objects per frame", allocs)
					}
				})
			}
		}
	}
}

func analysisName(fs, channels int) string {
	return fmt.Sprintf("%dk/ch%d", fs/1000, channels)
}
