package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type projectionDecodeFormats struct {
	float32 []float32
	int16   []int16
	int24   []int32
}

func decodeProjectionFormatsWithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, demixing []byte, packets [][]byte) (projectionDecodeFormats, error) {
	decFloat, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("NewProjectionDecoder float: %w", err)
	}
	if err := decFloat.SetGain(gainQ8); err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("SetGain float: %w", err)
	}
	decInt16, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("NewProjectionDecoder int16: %w", err)
	}
	if err := decInt16.SetGain(gainQ8); err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("SetGain int16: %w", err)
	}
	decInt24, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("NewProjectionDecoder int24: %w", err)
	}
	if err := decInt24.SetGain(gainQ8); err != nil {
		return projectionDecodeFormats{}, fmt.Errorf("SetGain int24: %w", err)
	}

	var got projectionDecodeFormats
	for frame, packet := range packets {
		f, err := decFloat.DecodeToFloat32(packet, frameSize)
		if err != nil {
			return projectionDecodeFormats{}, fmt.Errorf("frame %d float32: %w", frame, err)
		}
		got.float32 = append(got.float32, f...)
		i16, err := decInt16.DecodeToInt16(packet, frameSize)
		if err != nil {
			return projectionDecodeFormats{}, fmt.Errorf("frame %d int16: %w", frame, err)
		}
		got.int16 = append(got.int16, i16...)
		i24, err := decInt24.DecodeToInt24(packet, frameSize)
		if err != nil {
			return projectionDecodeFormats{}, fmt.Errorf("frame %d int24: %w", frame, err)
		}
		got.int24 = append(got.int24, i24...)
	}
	return got, nil
}

func decodeProjectionSourceInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, packets [][]byte) ([]int32, error) {
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, trivialMapping(channels))
	if err != nil {
		return nil, fmt.Errorf("NewDecoder source Int24: %w", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		return nil, fmt.Errorf("SetGain source Int24: %w", err)
	}
	var out []int32
	for frame, packet := range packets {
		pcm, err := dec.DecodeToInt24(packet, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d source Int24: %w", frame, err)
		}
		out = append(out, pcm...)
	}
	return out, nil
}

func projectionPacketsHaveMixedStreamModes(packets [][]byte, streams int) bool {
	for _, packet := range packets {
		streamPackets, err := parseMultistreamPacket(packet, streams)
		if err != nil {
			return false
		}
		var seen uint8
		for _, streamPacket := range streamPackets {
			if len(streamPacket) == 0 {
				continue
			}
			config := int(streamPacket[0] >> 3)
			var mode uint8
			if config < 12 {
				mode = 1 // SILK
			} else if config < 16 {
				mode = 2 // Hybrid
			} else {
				mode = 4 // CELT
			}
			seen |= mode
		}
		if seen&(seen-1) != 0 {
			return true
		}
	}
	return false
}

// TestProjectionDecodeHigherOrderAndGainMatchesLibopus checks projection
// demixing after the larger second- and third-order channel layouts, including
// both all-CELT and mixed per-stream coding. The same packets are decoded by
// each Go output API and the matching live projection decoder; gain values
// cover positive, negative, and both signed Q8 limits.
func TestProjectionDecodeHigherOrderAndGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameCount = 3
	)
	tests := []struct {
		name       string
		channels   int
		frameSize  int
		bitrate    int
		allCELT    bool
		mixedModes bool
	}{
		{name: "soa9_all_celt", channels: 9, frameSize: 480, bitrate: 1000000, allCELT: true},
		{name: "toa16_all_celt", channels: 16, frameSize: 960, bitrate: 1500000, allCELT: true},
		{name: "soa9_mixed_modes", channels: 9, frameSize: 960, bitrate: 256000, allCELT: false, mixedModes: true},
	}
	gainsQ8 := []int{1536, -1536, 32767, -32768}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := projectionDecodeRef(t, tc.channels, tc.frameSize, frameCount, tc.bitrate)
			if got := allPerStreamCELT(ref.packets, ref.streams); got != tc.allCELT {
				t.Fatalf("all-CELT classification=%v, want %v (streams=%d coupled=%d)", got, tc.allCELT, ref.streams, ref.coupledStreams)
			}
			if got := projectionPacketsHaveMixedStreamModes(ref.packets, ref.streams); got != tc.mixedModes {
				t.Fatalf("mixed per-stream mode classification=%v, want %v", got, tc.mixedModes)
			}
			mapping := trivialMapping(tc.channels)
			for _, gainQ8 := range gainsQ8 {
				t.Run(fmt.Sprintf("gain_q8_%d", gainQ8), func(t *testing.T) {
					got, err := decodeProjectionFormatsWithGain(sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, ref.demixing, ref.packets)
					if err != nil {
						t.Fatalf("gopus projection decode: %v", err)
					}
					wantFloat, err := decodeWithLibopusReferencePacketsGain(3, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, mapping, ref.demixing, ref.packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "projection gain float32 reference decode", err)
					}
					assertProjectionFloatSampleExact(t, got.float32, wantFloat, "projection gain float32")

					wantInt16, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, mapping, ref.demixing, ref.packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "projection gain int16 reference decode", err)
					}
					assertProjectionInt16SampleExact(t, got.int16, wantInt16, "projection gain int16")

					wantInt24, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, mapping, ref.demixing, ref.packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "projection gain int24 reference decode", err)
					}
					if len(got.int24) != len(wantInt24) {
						t.Fatalf("int24 sample count Go=%d C=%d", len(got.int24), len(wantInt24))
					}
					if tc.allCELT {
						gotSource, err := decodeProjectionSourceInt24WithGain(sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, ref.packets)
						if err != nil {
							t.Fatalf("gopus source Int24 decode: %v", err)
						}
						wantSource, err := decodeWithLibopusReferencePacketsInt24Gain(1, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSize, gainQ8, mapping, nil, ref.packets)
						if err != nil {
							libopustest.HelperUnavailable(t, "source Int24 reference decode", err)
						}
						for i := range gotSource {
							if gotSource[i] != wantSource[i] {
								t.Fatalf("source Int24 sample %d Go/C=%d/%d", i, gotSource[i], wantSource[i])
							}
						}
					}
					for i := range got.int24 {
						if got.int24[i] != wantInt24[i] {
							t.Fatalf("int24 sample %d Go/C=%d/%d; float PCM Go/C=%08x/%08x (%g/%g)", i, got.int24[i], wantInt24[i], math.Float32bits(got.float32[i]), math.Float32bits(wantFloat[i]), got.float32[i], wantFloat[i])
						}
					}
				})
			}
		})
	}
}
