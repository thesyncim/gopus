package multistream

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
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

func encodeModeSwitchSingleStreamPackets(t *testing.T, channels int, frameSize int, modes []encoder.Mode) [][]byte {
	return encodeModeSwitchSingleStreamPacketsWithSILKBandwidth(t, channels, frameSize, modes, types.BandwidthFullband)
}

func encodeModeSwitchSingleStreamPacketsWithSILKBandwidth(t *testing.T, channels int, frameSize int, modes []encoder.Mode, silkBandwidth types.Bandwidth) [][]byte {
	t.Helper()
	const sampleRate = 48000

	packets := make([][]byte, 0, len(modes))
	phase := 0.0
	for f, m := range modes {
		enc := encoder.NewEncoder(sampleRate, channels)
		enc.SetFrameSize(frameSize)
		enc.SetBandwidth(types.BandwidthFullband)
		if m == encoder.ModeSILK && silkBandwidth != types.BandwidthFullband {
			enc.SetBandwidth(silkBandwidth)
		}
		enc.SetBitrate(96000)
		if err := enc.SetInBandFEC(0); err != nil {
			t.Fatalf("SetInBandFEC: %v", err)
		}
		if channels == 2 {
			enc.SetForceChannels(2)
		}
		enc.SetMode(m)

		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
				0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
					0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// streamModeOfPacket classifies the per-stream coding mode from the TOC of a
// single-stream Opus packet.
func streamModeOfPacket(pkt []byte) int {
	if len(pkt) == 0 {
		return -1
	}
	return parseStreamTOC(pkt[0]).mode
}

func perStreamModes(packets [][]byte) []int {
	modes := make([]int, len(packets))
	for i, p := range packets {
		modes[i] = streamModeOfPacket(p)
	}
	return modes
}

// TestMultistreamPerStreamModeTransitionMatchesLibopus compares every output
// sample, including the 5 ms PLC crossfade, with the selected libopus build.
// Fresh encoders create valid packets whose mode changes carry no redundancy.
// The sequence exercises CELT resets and transitions in both directions.
func TestMultistreamPerStreamModeTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960 // 20 ms
	)

	// Mode walk crossing the CELT_ONLY boundary in every direction.
	modeWalk := []encoder.Mode{
		encoder.ModeSILK,
		encoder.ModeCELT,
		encoder.ModeHybrid,
		encoder.ModeCELT,
		encoder.ModeHybrid,
		encoder.ModeCELT,
		encoder.ModeCELT,
	}

	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize, modeWalk)
			modes := perStreamModes(packets)

			// Require an actual SILK/Hybrid-to-CELT transition in the input.
			sawCeltTarget := false
			prev := -1
			for i, m := range modes {
				if i > 0 && m == streamModeCELT && prev != streamModeCELT {
					sawCeltTarget = true
				}
				prev = m
			}
			if !sawCeltTarget {
				t.Fatalf("encoder did not produce a CELT-target transition; modes=%v", modes)
			}

			streams := 1
			coupled := 0
			if channels == 2 {
				coupled = 1
			}
			mapping := trivialMapping(channels)

			dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			var got []float32
			for i, p := range packets {
				frame, derr := dec.DecodeToFloat32(p, frameSize)
				if derr != nil {
					t.Fatalf("frame %d gopus decode: %v", i, derr)
				}
				got = append(got, frame...)
			}

			want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream mode-transition reference decode", err)
			}
			if len(got) != len(want) {
				t.Fatalf("length mismatch got=%d want=%d", len(got), len(want))
			}

			perFrame := frameSize * channels
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("frame %d (mode=%d) sample %d bits=%08x want=%08x",
						i/perFrame, modes[i/perFrame], i%perFrame,
						math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}
