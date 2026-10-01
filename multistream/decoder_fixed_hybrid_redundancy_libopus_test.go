//go:build gopus_fixed_point

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedProjectionHybridRedundancyMatchesLibopusInt24(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 9
		frameSize  = 480
		frameCount = 6
		bitrate    = 256000
	)
	ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
	parts, err := parseMultistreamPacket(ref.packets[1], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) <= 4 || len(parts[4]) == 0 || parseStreamTOC(parts[4][0]).mode != streamModeHybrid {
		t.Fatalf("frame 1 stream 4 does not carry the expected Hybrid frame")
	}
	streamPackets := make([][][]byte, ref.streams)
	for frame, packet := range ref.packets {
		parts, err := parseMultistreamPacket(packet, ref.streams)
		if err != nil {
			t.Fatalf("frame %d parse multistream packet: %v", frame, err)
		}
		for stream, part := range parts {
			streamPackets[stream] = append(streamPackets[stream], part)
		}
	}
	for stream, packets := range streamPackets {
		childChannels := streamChannels(stream, ref.coupledStreams)
		childCoupled := 0
		childMapping := []byte{0}
		if childChannels == 2 {
			childCoupled = 1
			childMapping = []byte{0, 1}
		}
		childWant, err := decodeWithLibopusReferencePacketsInt24Gain(1, sampleRate, childChannels,
			1, childCoupled, frameSize, 0, childMapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "fixed Hybrid child int24 decode", err)
		}
		child, err := NewDecoder(sampleRate, childChannels, 1, childCoupled, childMapping)
		if err != nil {
			t.Fatal(err)
		}
		for frame, packet := range packets {
			got, err := child.DecodeToInt24(packet, frameSize)
			if err != nil {
				t.Fatalf("stream %d frame %d DecodeToInt24: %v", stream, frame, err)
			}
			for i, sample := range got {
				if expected := childWant[frame*len(got)+i]; sample != expected {
					t.Fatalf("stream %d frame %d sample %d Go=%d C=%d", stream, frame, i, sample, expected)
				}
			}
		}
	}
	probe, err := NewDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, trivialMapping(channels))
	if err != nil {
		t.Fatal(err)
	}
	for frame, packet := range ref.packets {
		if _, handled, err := probe.DecodeToResFixed(packet, frameSize); err != nil || !handled {
			t.Fatalf("frame %d fixed stream decode handled=%v err=%v", frame, handled, err)
		}
		if frame == 1 {
			st := probe.decoders[4].(*streamState)
			if !st.fixedHybridRedundant || !st.fixedHybridRedundantToSilk || len(st.fixedHybridRedundantData) != 15 || !st.fixedHybridRedundantValid {
				t.Fatalf("frame 1 stream 4 redundancy state: active=%v toSilk=%v bytes=%d decoded=%v",
					st.fixedHybridRedundant, st.fixedHybridRedundantToSilk, len(st.fixedHybridRedundantData), st.fixedHybridRedundantValid)
			}
		}
	}

	want, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, channels,
		ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(channels), ref.demixing, ref.packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed Hybrid redundancy projection int24 decode", err)
	}
	dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int32, 0, len(want))
	for frame, packet := range ref.packets {
		out, err := dec.DecodeToInt24(packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt24: %v", frame, err)
		}
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}

	allocDec, err := NewDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, trivialMapping(channels))
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, frameSize*channels)
	decodeCycle := func() {
		for frame, packet := range ref.packets {
			if n, err := allocDec.DecodeIntoFloat32(packet, output, frameSize); err != nil || n != frameSize {
				t.Fatalf("allocation cycle frame %d DecodeIntoFloat32=(%d,%v)", frame, n, err)
			}
		}
	}
	decodeCycle()
	if allocs := testing.AllocsPerRun(5, decodeCycle); allocs != 0 {
		t.Fatalf("warm projection Hybrid redundancy allocations=%g, want 0", allocs)
	}
}
