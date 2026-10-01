package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// encodeModeSwitchSingleStreamPackets encodes one elementary-stream packet per
// requested mode, each from a FRESH encoder so the forced coding mode is honored
// exactly (a fresh encoder always codes its first frame in the requested mode,
// whereas a long-lived encoder may override the forced mode for rate/transient
// reasons). Concatenated, the packets present the decoder with genuine
// CELT<->SILK/Hybrid mode changes carrying no redundancy (a fresh encoder has no
// prior frame to prefill from), exactly the opus_decode_frame pcm_transition
// crossfade case. These single-stream Opus packets are valid 1-stream
// multistream packets, so decoding them through the multistream decoder
// exercises the per-stream streamState transition handling against the libopus
// multistream oracle.
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
