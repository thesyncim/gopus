package celt

import (
	"fmt"
	"math"
	"testing"
)

// Hybrid enters the same disabled transition regardless of the pitch-analysis
// enable decision. Check the complete live state, including a prior active gain.
func TestHybridPrefilterMatchesDisabledTransition(t *testing.T) {
	const frameSize = 480
	for _, channels := range []int{1, 2} {
		for _, gain := range []float32{0, .46875} {
			t.Run(fmt.Sprintf("ch%d/gain%g", channels, gain), func(t *testing.T) {
				got, want := NewEncoder(channels), NewEncoder(channels)
				got.SetHybrid(true)
				for _, e := range []*Encoder{got, want} {
					e.tapsetDecision = 2
					e.prefilterPeriod = 140
					e.prefilterGain = gain
					e.prefilterTapset = 1
					e.analysisValid = true
					e.analysisTonality = .8
					e.analysisMaxPitchRatio = .77
					for i := range e.prefilterMem {
						e.prefilterMem[i] = float32(i-120) / 512
					}
					for i := range e.overlapBuffer {
						e.overlapBuffer[i] = float32(60-i) / 256
					}
				}
				// runPrefilter reads and updates the planar in buffer: per
				// channel, the overlap head followed by the frame.
				input := make([]float32, (frameSize+Overlap)*channels)
				for i := range input {
					input[i] = float32((i%37)-18) / 64
				}
				gotPCM, wantPCM := append([]float32(nil), input...), append([]float32(nil), input...)
				checkBits := func(name string, a, b []float32) {
					t.Helper()
					if len(a) != len(b) {
						t.Fatalf("%s lengths %d != %d", name, len(a), len(b))
					}
					for i := range a {
						if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
							t.Fatalf("%s[%d] bits %08x != %08x", name, i, math.Float32bits(a[i]), math.Float32bits(b[i]))
						}
					}
				}
				// A generous budget and strong tone exercise the enable input. The
				// hybrid invariant must force the disabled branch for both gain states.
				for range 3 {
					copy(gotPCM, input)
					copy(wantPCM, input)
					oldPeriod, oldGain := got.prefilterPeriod, got.prefilterGain
					oldWantPeriod, oldWantGain := want.prefilterPeriod, want.prefilterGain
					a := got.runPrefilter(gotPCM, frameSize, 2, true, .1, 80, .22, .999, .77)
					b := want.runPrefilter(wantPCM, frameSize, 2, false, .1, 80, .22, .999, .77)
					if a != b || a.on || a.gain != 0 {
						t.Fatalf("hybrid result %+v != disabled %+v", a, b)
					}
					if got.pitchChanged(a, oldPeriod, oldGain) != want.pitchChanged(b, oldWantPeriod, oldWantGain) {
						t.Fatal("pitch-change decision differs")
					}
					checkBits("preemphasis", gotPCM, wantPCM)
					checkBits("history", got.prefilterMem, want.prefilterMem)
					checkBits("overlap", got.overlapBuffer, want.overlapBuffer)
					if got.prefilterPeriod != want.prefilterPeriod || got.prefilterGain != want.prefilterGain || got.prefilterTapset != want.prefilterTapset {
						t.Fatal("prefilter state differs")
					}
				}
				got.prefilterGain = gain
				got.runPrefilter(gotPCM, frameSize, 2, true, .1, 80, .22, .999, .77)
				if n := testing.AllocsPerRun(100, func() {
					got.prefilterGain = gain
					copy(gotPCM, input)
					got.runPrefilter(gotPCM, frameSize, 2, true, .1, 80, .22, .999, .77)
				}); n != 0 {
					t.Fatalf("allocations=%v want 0", n)
				}
			})
		}
	}
}
