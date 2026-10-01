//go:build gopus_fixed_point

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedHybridMultiframeInt24MatchesLibopusAndZeroAllocs(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, spec := range buildSurroundDecodeFuzzSweep() {
		if spec.channels != 1 || spec.frameSize < 1920 || spec.bitrate != 16000 || spec.int16Path {
			continue
		}
		t.Run(spec.name, func(t *testing.T) {
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
			ref, err := encodeLibopusSurround(sampleRate, spec.channels, 1, 2049,
				spec.bitrate, spec.vbr, spec.vbrConstraint, 10, -1000,
				spec.frameSize, spec.frameCount, 4000, pcm, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed Hybrid multiframe encode", err)
			}
			for i, packet := range ref.packets {
				if parseStreamTOC(packet[0]).mode != streamModeHybrid || packet[0]&3 == 0 {
					t.Fatalf("packet %d does not exercise multi-frame Hybrid: TOC=%02x", i, packet[0])
				}
			}
			want, err := decodeWithLibopusReferencePacketsInt24Gain(1, sampleRate, spec.channels,
				ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed Hybrid multiframe int24 decode", err)
			}
			dec, err := NewDecoder(sampleRate, spec.channels, ref.streams, ref.coupledStreams, ref.mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(spec.gainQ8); err != nil {
				t.Fatal(err)
			}
			for frame, packet := range ref.packets {
				got, err := dec.DecodeToInt24(packet, spec.frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != spec.frameSize*spec.channels {
					t.Fatalf("frame %d samples=%d, want %d", frame, len(got), spec.frameSize*spec.channels)
				}
				for i, sample := range got {
					if expected := want[frame*len(got)+i]; sample != expected {
						t.Fatalf("frame %d sample %d Go=%d C=%d", frame, i, sample, expected)
					}
				}
			}
			output := make([]float32, spec.frameSize*spec.channels)
			decode := func() {
				for _, packet := range ref.packets {
					if n, err := dec.DecodeIntoFloat32(packet, output, spec.frameSize); err != nil || n != spec.frameSize {
						t.Fatalf("DecodeIntoFloat32=%d, %v", n, err)
					}
				}
			}
			decode()
			if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
				t.Fatalf("warm Hybrid multi-frame allocations=%g, want 0", allocs)
			}
		})
	}
}
