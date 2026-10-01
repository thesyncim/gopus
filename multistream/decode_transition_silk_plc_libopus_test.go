package multistream

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// These fixed packet histories exercise SILK's saved 20 ms excitation state
// when a CELT target requests 10 ms of SILK PLC for its 5 ms transition.
var silkToCELTTransitionPackets = []struct {
	name      string
	frameSize int
	target    int
	packets   []string
}{
	{
		name:      "celt_silk_silk_celt",
		frameSize: 960,
		target:    3,
		packets: []string{
			"987e1a9df12c1f5762337294a37f",
			"0b0185a6d366f69adef15ecaf892e0",
			"08858cdb858cd482b2a5486aa940",
			"98024c8c2bb47c4e3fc402cf",
			"9817311653c9705554eaa1bf41",
		},
	},
	{
		name:      "silk_silk_celt",
		frameSize: 2880,
		target:    2,
		packets: []string{
			"18e127ad6528c2bb9d1b995b365bd8c074d634d0ac110cbe7ed96d9907ef2696b371e8c70379d79ef336e50f19",
			"1b4101e5c949891be191b90f13dcd161178a4073bbeb47a874359cff57f349986af44f9b7972538b35b000",
			"9b43057daac888d9d95e0b018c91f80ef203c25bd365748ce8b80a37497b959966f8a6d2dffa780000000000",
			"9b43044775c3919b5877da4031c3274794a87fc523d5570696dd2c4a6bf044532788267796e19a00000000",
			"18e5c580bf1bfc1f79ee5abb88c809a2d5316fd8ab1b61703f8ed3c3608d8055d0bb082520ba2ca26278eab5b6cbc01c",
		},
	},
}

func TestSILKToCELTTransitionPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, tc := range silkToCELTTransitionPackets {
		t.Run(tc.name, func(t *testing.T) {
			packets := make([][]byte, len(tc.packets))
			steps := make([]transitionDecodeStep, len(packets))
			for i, encoded := range tc.packets {
				packet, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				packets[i] = packet
				steps[i] = transitionDecodeStep{packet: packet, frameSize: tc.frameSize}
			}

			want := decodeTransitionSequenceWithLibopus(t, sampleRate, 1, 0, tc.frameSize, steps)
			dec := newStreamDecoder(sampleRate, 1)
			for i, packet := range packets {
				got, err := dec.Decode(packet, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if want[i].samples != tc.frameSize || len(got) != want[i].samples {
					t.Fatalf("frame %d length Go=%d C=%d want=%d", i, len(got), want[i].samples, tc.frameSize)
				}
				if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
					t.Fatalf("frame %d range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
				}
				assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("frame %d", i))
			}

			for _, plcSize := range []int{sampleRate / 200, sampleRate / 100} {
				t.Run(fmt.Sprintf("plc%d", plcSize), func(t *testing.T) {
					stageSteps := append([]transitionDecodeStep(nil), steps[:tc.target]...)
					stageSteps = append(stageSteps, transitionDecodeStep{frameSize: plcSize})
					stageWant := decodeTransitionSequenceWithLibopus(t, sampleRate, 1, 0, tc.frameSize, stageSteps)
					stageDec := newStreamDecoder(sampleRate, 1)
					for i := range tc.target {
						got, err := stageDec.Decode(packets[i], tc.frameSize)
						if err != nil {
							t.Fatalf("history frame %d: %v", i, err)
						}
						if stageWant[i].samples != tc.frameSize || len(got) != tc.frameSize || stageDec.FinalRange() != stageWant[i].finalRange {
							t.Fatalf("history frame %d length/range Go=(%d,%08x) C=(%d,%08x)",
								i, len(got), stageDec.FinalRange(), stageWant[i].samples, stageWant[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, stageWant[i].pcm, fmt.Sprintf("history frame %d", i))
					}
					if stageDec.lastMode != streamModeSILK {
						t.Fatalf("previous mode=%d want SILK", stageDec.lastMode)
					}
					got, err := stageDec.transitionPLCToFloat32(plcSize, int(stageDec.lastMode), int(stageDec.lastBandwidth), stageDec.lastPacketStereo)
					if err != nil {
						t.Fatal(err)
					}
					if stageWant[tc.target].samples != plcSize || len(got) != plcSize {
						t.Fatalf("PLC length Go=%d C=%d want=%d", len(got), stageWant[tc.target].samples, plcSize)
					}
					assertTransitionStagePCMExact(t, got, stageWant[tc.target].pcm, "full SILK PLC stage")
				})
			}
		})
	}
}

// TestSILKPLCDurationChangesMatchLibopus checks current loss-frame geometry
// against previous-good excitation geometry in both duration directions. The
// next good packet also checks the state carried out of concealment.
func TestSILKPLCDurationChangesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, channels := range []int{1, 2} {
		for _, bandwidth := range []types.Bandwidth{types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband} {
			for _, sizes := range [][2]int{{960, 480}, {480, 960}, {480, 720}} {
				goodSize, lossSize := sizes[0], sizes[1]
				t.Run(fmt.Sprintf("ch%d/bw%d/good%d/loss%d", channels, bandwidth, goodSize, lossSize), func(t *testing.T) {
					packets := encodeModeSwitchSingleStreamPacketsWithSILKBandwidth(
						t, channels, goodSize, []encoder.Mode{encoder.ModeSILK, encoder.ModeSILK}, bandwidth)
					for i, packet := range packets {
						if got := streamModeOfPacket(packet); got != streamModeSILK {
							t.Fatalf("packet %d mode=%d want SILK", i, got)
						}
					}
					steps := []transitionDecodeStep{
						{packet: packets[0], frameSize: goodSize},
						{frameSize: lossSize},
						{packet: packets[1], frameSize: goodSize},
					}
					want := decodeTransitionSequenceWithLibopus(t, sampleRate, channels, 0, max(goodSize, lossSize), steps)
					dec := newStreamDecoder(sampleRate, channels)
					for i, step := range steps {
						got, err := dec.Decode(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("step %d: %v", i, err)
						}
						if want[i].samples != step.frameSize || len(got) != want[i].samples*channels {
							t.Fatalf("step %d length Go=%d C=%d want=%d", i, len(got), want[i].samples*channels, step.frameSize*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != want[i].finalRange {
							t.Fatalf("step %d range Go=%08x C=%08x", i, gotRange, want[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, want[i].pcm, fmt.Sprintf("step %d", i))
					}
				})
			}
		}
	}
}
