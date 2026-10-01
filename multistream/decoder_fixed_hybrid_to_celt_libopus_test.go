//go:build gopus_fixed_point

package multistream

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestFixedHybridToCELTTransitionMatchesLibopus checks the public fixed decoder
// across a no-redundancy Hybrid-to-CELT boundary. The target CELT frame starts
// with PLC decoded in the previous Hybrid mode, then fades into the main frame.
func TestFixedHybridToCELTTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		if len(packets) != len(modes) {
			t.Fatalf("encoded %d packets, want %d", len(packets), len(modes))
		}
		if got := streamModeOfPacket(packets[0]); got != streamModeHybrid {
			t.Fatalf("preceding packet mode=%d, want Hybrid", got)
		}
		if got := streamModeOfPacket(packets[1]); got != streamModeCELT {
			t.Fatalf("target packet mode=%d, want CELT", got)
		}

		coupled := 0
		if channels == 2 {
			coupled = 1
		}
		mapping := trivialMapping(channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			steps := []transitionDecodeStep{
				{packet: packets[0], frameSize: frameSize},
				{packet: packets[1], frameSize: frameSize},
			}
			for _, gainQ8 := range []int{0, 768, -768} {
				t.Run(fmt.Sprintf("ch%d/fs%d/g%d", channels, sampleRate, gainQ8), func(t *testing.T) {
					wantFixed := decodePublicTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
					if len(wantFixed) != len(steps) {
						t.Fatalf("C returned %d fixed frames, want %d", len(wantFixed), len(steps))
					}

					dec, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatalf("SetGain(%d): %v", gainQ8, err)
					}
					pathProbe, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder path probe: %v", err)
					}
					if err := pathProbe.SetGain(gainQ8); err != nil {
						t.Fatalf("path probe SetGain(%d): %v", gainQ8, err)
					}
					for i, step := range steps {
						_, fixedHandled, err := pathProbe.DecodeToResFixed(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("fixed path probe frame %d: %v", i, err)
						}
						if !fixedHandled {
							t.Fatalf("frame %d did not use the public fixed path", i)
						}
						got, err := dec.DecodeToFloat32(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("Go frame %d: %v", i, err)
						}
						if wantFixed[i].samples != frameSize || len(got) != frameSize*channels {
							t.Fatalf("frame %d sample count Go=%d fixed C=%d want=%d",
								i, len(got), wantFixed[i].samples*channels, frameSize*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != wantFixed[i].finalRange {
							t.Fatalf("frame %d final range Go=%08x fixed C=%08x", i, gotRange, wantFixed[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, wantFixed[i].pcm, fmt.Sprintf("frame %d", i))
					}

					allocDec, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("allocation decoder: %v", err)
					}
					if err := allocDec.SetGain(gainQ8); err != nil {
						t.Fatalf("allocation SetGain(%d): %v", gainQ8, err)
					}
					output := make([]float32, frameSize*channels)
					decodeSequence := func() error {
						for _, step := range steps {
							if _, err := allocDec.DecodeIntoFloat32(step.packet, output, step.frameSize); err != nil {
								return err
							}
						}
						return nil
					}
					for range 2 {
						if err := decodeSequence(); err != nil {
							t.Fatalf("warm allocation sequence: %v", err)
						}
					}
					var allocErr error
					allocs := testing.AllocsPerRun(10, func() {
						allocErr = decodeSequence()
					})
					if allocErr != nil {
						t.Fatalf("allocation sequence: %v", allocErr)
					}
					if allocs != 0 {
						t.Fatalf("warm Hybrid-to-CELT decode allocated %g times/op", allocs)
					}
				})
			}
		}
	}
}
