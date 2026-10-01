package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestProjectionDecodePCMAndFinalRangeMatchesSelectedLibopus checks the public
// projection output and the XOR of the elementary decoders' range states. The
// projection PCM oracle retains one C projection decoder across the sequence;
// each range oracle retains one C decoder for its elementary stream.
func TestProjectionDecodePCMAndFinalRangeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	tests := []struct {
		name     string
		channels int
		bitrate  int
	}{
		{name: "first_order", channels: 4, bitrate: 128000},
		{name: "second_order_mixed_modes", channels: 9, bitrate: 256000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const frameSize = 480
			ref := projectionDecodeRef(t, tc.channels, frameSize, 2, tc.bitrate)
			if len(ref.packets) != 2 || ref.streams < 2 {
				t.Fatalf("projection fixture has %d packets and %d streams", len(ref.packets), ref.streams)
			}
			if tc.channels == 9 && !projectionPacketsHaveMixedStreamModes(ref.packets, ref.streams) {
				t.Fatal("second-order fixture has no mixed-mode stream packet")
			}
			sequence := [][]byte{ref.packets[0], ref.packets[1], nil}
			wantPCM, err := decodeWithLibopusReferencePackets(3, sampleRate, tc.channels,
				ref.streams, ref.coupledStreams, frameSize, trivialMapping(tc.channels), ref.demixing, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection sequence reference decode", err)
			}
			if len(wantPCM) != len(sequence)*frameSize*tc.channels {
				t.Fatalf("reference projection PCM length=%d, want %d", len(wantPCM), len(sequence)*frameSize*tc.channels)
			}

			wantRanges := make([]uint32, len(sequence))
			for stream := 0; stream < ref.streams; stream++ {
				cases := make([]libopustest.DecodeDiffCase, len(sequence))
				for step, packet := range sequence {
					cases[step] = libopustest.DecodeDiffCase{
						Format:    libopustest.DecodeDiffFormatFloat32,
						FrameSize: frameSize,
					}
					if len(packet) == 0 {
						continue
					}
					parts, err := parseMultistreamPacket(packet, ref.streams)
					if err != nil {
						t.Fatalf("step %d split: %v", step, err)
					}
					cases[step].Packet = parts[stream]
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, streamChannels(stream, ref.coupledStreams), cases)
				if err != nil {
					libopustest.HelperUnavailable(t, "elementary stream reference decode", err)
				}
				if len(want) != len(sequence) {
					t.Fatalf("stream %d reference steps=%d, want %d", stream, len(want), len(sequence))
				}
				for step, result := range want {
					if result.Code != frameSize {
						t.Fatalf("stream %d step %d C samples=%d, want %d", stream, step, result.Code, frameSize)
					}
					wantRanges[step] ^= result.FinalRange
				}
			}

			dec, err := NewProjectionDecoder(sampleRate, tc.channels, ref.streams, ref.coupledStreams, ref.demixing)
			if err != nil {
				t.Fatal(err)
			}
			checkStep := func(step int, packet []byte) {
				t.Helper()
				got, err := dec.DecodeToFloat32(packet, frameSize)
				if err != nil {
					t.Fatalf("step %d projection decode: %v", step, err)
				}
				want := wantPCM[step*frameSize*tc.channels : (step+1)*frameSize*tc.channels]
				if len(got) != len(want) {
					t.Fatalf("step %d PCM length Go=%d C=%d", step, len(got), len(want))
				}
				for i, sample := range got {
					if math.Float32bits(sample) != math.Float32bits(want[i]) {
						t.Fatalf("step %d PCM sample %d Go=%08x C=%08x", step, i,
							math.Float32bits(sample), math.Float32bits(want[i]))
					}
				}
				if gotRange := dec.FinalRange(); gotRange != wantRanges[step] {
					t.Fatalf("step %d final range Go=%08x C=%08x", step, gotRange, wantRanges[step])
				}
			}
			for step, packet := range sequence {
				checkStep(step, packet)
			}
			dec.Reset()
			if got := dec.FinalRange(); got != 0 {
				t.Fatalf("reset final range=%08x, want 0", got)
			}
			checkStep(0, sequence[0])
		})
	}
}
