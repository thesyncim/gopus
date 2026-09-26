package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestHybridToSILKFadeRequiresDecodedHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960
	)

	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			packets := encodeModeSwitchSingleStreamPacketsWithSILKBandwidth(
				t,
				channels,
				frameSize,
				[]encoder.Mode{encoder.ModeHybrid, encoder.ModeSILK},
				types.BandwidthWideband,
			)
			modes := perStreamModes(packets)
			if modes[0] != streamModeHybrid || modes[1] != streamModeSILK {
				t.Fatalf("encoder did not produce Hybrid then SILK packets: modes=%v", modes)
			}
			if got := parseStreamTOC(packets[1][0]).bandwidth; got != 2 {
				t.Fatalf("SILK packet bandwidth=%d, want wideband (2)", got)
			}
			wantStereo := channels == 2
			for i, packet := range packets {
				if got := parseStreamTOC(packet[0]).stereo; got != wantStereo {
					t.Fatalf("packet %d stereo=%t, want %t", i, got, wantStereo)
				}
			}

			coupled := channels - 1
			mapping := trivialMapping(channels)
			freshSILK, err := decodeWithLibopusReferencePackets(
				1, sampleRate, channels, 1, coupled, frameSize, mapping, nil, packets[1:],
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "fresh SILK reference decode", err)
			}
			wholeSequence, err := decodeWithLibopusReferencePackets(
				1, sampleRate, channels, 1, coupled, frameSize, mapping, nil, packets,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "Hybrid-to-SILK reference decode", err)
			}

			newDecoder := func() *Decoder {
				d, derr := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if derr != nil {
					t.Fatalf("NewDecoder: %v", derr)
				}
				return d
			}
			streamStateOf := func(t *testing.T, d *Decoder) *streamState {
				t.Helper()
				state, ok := d.decoders[0].(*streamState)
				if !ok {
					t.Fatal("decoder stream does not use streamState")
				}
				return state
			}
			decodeOne := func(t *testing.T, d *Decoder, packet []byte) []float32 {
				t.Helper()
				got, derr := d.DecodeToFloat32(packet, frameSize)
				if derr != nil {
					t.Fatalf("DecodeToFloat32: %v", derr)
				}
				return got
			}
			assertPCMExact := func(label string, got, want []float32) {
				t.Helper()
				if len(got) != len(want) {
					t.Fatalf("%s output length=%d, reference length=%d", label, len(got), len(want))
				}
				for i := range want {
					if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
						t.Fatalf("%s first difference at sample %d: Go=%08x C=%08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
					}
				}
			}

			// A new decoder has no previous mode even though lastMode's sentinel is Hybrid.
			fresh := newDecoder()
			assertPCMExact("fresh SILK", decodeOne(t, fresh, packets[1]), freshSILK)

			// Reset clears decoded history after a real non-silent Hybrid frame.
			reset := newDecoder()
			hybridPCM := decodeOne(t, reset, packets[0])
			resetState := streamStateOf(t, reset)
			if len(hybridPCM) == 0 || !resetState.haveDecoded || int(resetState.lastMode) != streamModeHybrid {
				t.Fatal("Hybrid packet did not establish decoded history before Reset")
			}
			nonzero := false
			for _, sample := range hybridPCM {
				nonzero = nonzero || sample != 0
			}
			if !nonzero {
				t.Fatal("Hybrid packet did not establish nonzero decoder history")
			}
			reset.Reset()
			if streamStateOf(t, reset).haveDecoded {
				t.Fatal("Reset retained decoded history")
			}
			assertPCMExact("reset SILK", decodeOne(t, reset, packets[1]), freshSILK)

			// The guard must retain the true Hybrid-to-SILK CELT fade-out.
			transition := newDecoder()
			gotSequence := make([]float32, 0, len(wholeSequence))
			for i, packet := range packets {
				gotSequence = append(gotSequence, decodeOne(t, transition, packet)...)
				state := streamStateOf(t, transition)
				if i == 0 && (!state.haveDecoded || int(state.lastMode) != streamModeHybrid) {
					t.Fatal("Hybrid frame did not establish history before the real transition")
				}
			}
			assertPCMExact("Hybrid-to-SILK sequence", gotSequence, wholeSequence)
		})
	}
}
