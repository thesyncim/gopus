package multistream

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	oggcontainer "github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"math"
	"math/rand"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestProjectionAnalysisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels       = 4
		frameCount     = 6
		bitrate        = 384000
		maxPacketBytes = 4000
	)
	cases := []struct {
		name       string
		frameSize  int
		vbr        bool
		constraint bool
	}{
		{name: "cvbr_20ms", frameSize: 960, vbr: true, constraint: true},
		{name: "cbr_40ms", frameSize: 1920},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := generateAmbisonicsSweep(channels, tc.frameSize, frameCount)
			ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, tc.vbr, tc.constraint,
				10, -1000, tc.frameSize, frameCount, maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection analysis reference", err)
				return
			}
			if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
				t.Fatalf("oracle records packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), frameCount)
			}
			enc, err := NewProjectionEncoder(48000, channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			enc.SetBitrate(bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(tc.constraint)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()
			for frame := range frameCount {
				start := frame * channels * tc.frameSize
				input := pcm[start : start+channels*tc.frameSize]
				got, err := encodePacketMax(enc, input, tc.frameSize, input, maxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
						firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
				}
			}
		})
	}
}

func TestNewProjectionDecoder_InvalidLayout(t *testing.T) {
	// coupledStreams (3) > streams (2) is rejected by NewDecoder.
	_, err := NewProjectionDecoder(48000, 4, 2, 3, nil)
	if !errors.Is(err, ErrInvalidCoupledStreams) {
		t.Fatalf("NewProjectionDecoder bad layout: got %v, want ErrInvalidCoupledStreams", err)
	}
}

// TestNewProjectionDecoder_BadMatrix verifies that NewProjectionDecoder returns
// ErrInvalidProjectionMatrix when the supplied demixing matrix does not match the
// expected 2*rows*cols byte size for the channel/stream layout.
func TestNewProjectionDecoder_BadMatrix(t *testing.T) {
	// Valid FOA family-3 layout: 4 channels, streams=2, coupled=2.
	// Expected matrix size = 2 * rows(4) * cols(streams+coupled=4) = 32 bytes.
	// Supply a too-short matrix to trip the size check.
	badMatrix := make([]byte, 8)
	_, err := NewProjectionDecoder(48000, 4, 2, 2, badMatrix)
	if !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("NewProjectionDecoder bad matrix: got %v, want ErrInvalidProjectionMatrix", err)
	}
}

// TestAmbisonicsMapping_InvalidChannels verifies that AmbisonicsMapping
// propagates the ValidateAmbisonics error for an invalid channel count instead of
// returning a mapping.
func TestAmbisonicsMapping_InvalidChannels(t *testing.T) {
	// 5 channels is not a valid ambisonics count ((order+1)^2 + {0,2}).
	mapping, err := AmbisonicsMapping(5)
	if err == nil {
		t.Fatalf("AmbisonicsMapping(5) returned mapping %v, want error", mapping)
	}
	if !errors.Is(err, ErrInvalidAmbisonicsChannels) {
		t.Fatalf("AmbisonicsMapping(5) error = %v, want ErrInvalidAmbisonicsChannels", err)
	}
}

func TestNewProjectionEncoderRoundtrip(t *testing.T) {
	cases := []struct {
		name     string
		channels int
	}{
		{name: "foa-4ch", channels: 4},
		{name: "foa-nd-6ch", channels: 6},
		{name: "soa-9ch", channels: 9},
		{name: "soa-nd-11ch", channels: 11},
		{name: "toa-16ch", channels: 16},
		{name: "toa-nd-18ch", channels: 18},
		{name: "fourthoa-25ch", channels: 25},
		{name: "fourthoa-nd-27ch", channels: 27},
		{name: "fifthoa-36ch", channels: 36},
		{name: "fifthoa-nd-38ch", channels: 38},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewProjectionEncoder(48000, tc.channels)
			if err != nil {
				t.Fatalf("NewProjectionEncoder(%d): %v", tc.channels, err)
			}
			if enc.MappingFamily() != 3 {
				t.Errorf("MappingFamily() = %d, want 3", enc.MappingFamily())
			}

			demix := enc.GetDemixingMatrix()
			if demix == nil {
				t.Fatal("GetDemixingMatrix() returned nil")
			}
			wantSize := enc.DemixingMatrixSize()
			if len(demix) != wantSize {
				t.Fatalf("GetDemixingMatrix() len = %d, want %d", len(demix), wantSize)
			}

			dec, err := NewProjectionDecoder(48000, tc.channels, enc.Streams(), enc.CoupledStreams(), demix)
			if err != nil {
				t.Fatalf("NewProjectionDecoder(%d): %v", tc.channels, err)
			}

			frameSize := 960
			pcm := generateMultichannelSine(tc.channels, frameSize)
			packet, err := encodePacket(enc, pcm, frameSize)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if packet == nil {
				t.Fatal("Encode returned nil (DTX)")
			}

			out, err := dec.DecodeToFloat32(packet, frameSize)
			if err != nil {
				t.Fatalf("DecodeToFloat32: %v", err)
			}
			if len(out) != frameSize*tc.channels {
				t.Fatalf("output len = %d, want %d", len(out), frameSize*tc.channels)
			}
			if energy := computeEnergyF32(out); energy < 1e-6 {
				t.Fatalf("output energy too low: %e", energy)
			}
		})
	}
}

// TestNewProjectionEncoderUnsupportedOrders verifies that projection encoding
// correctly rejects ambisonics orders outside the range supported by libopus 1.6.1.
//
// libopus only provides pre-computed matrices for orders 1-5; orders 0 and 6-14
// return OPUS_BAD_ARG from opus_projection_ambisonics_encoder_create.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_ambisonics_encoder_get_size,
// src/mapping_matrix.c (only foa..fifthoa tables exist)
func TestNewProjectionEncoderUnsupportedOrders(t *testing.T) {
	unsupported := []struct {
		name     string
		channels int
	}{
		// Order 0 (no mixing/demixing matrix in libopus)
		{name: "order0-1ch", channels: 1},
		{name: "order0-nd-3ch", channels: 3},
		// Orders 6-14 (no matrices in libopus)
		{name: "order6-49ch", channels: 49},
		{name: "order6-nd-51ch", channels: 51},
		{name: "order7-64ch", channels: 64},
		{name: "order7-nd-66ch", channels: 66},
		{name: "order8-81ch", channels: 81},
		{name: "order14-225ch", channels: 225},
		{name: "order14-nd-227ch", channels: 227},
	}

	for _, tc := range unsupported {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewProjectionEncoder(48000, tc.channels)
			if err == nil {
				t.Fatalf("NewProjectionEncoder(%d) expected error, got nil", tc.channels)
			}
			if !errors.Is(err, ErrProjectionOrderUnsupported) {
				t.Errorf("NewProjectionEncoder(%d) error = %v, want ErrProjectionOrderUnsupported", tc.channels, err)
			}
		})
	}
}

// TestProjectionDecoderNilDemixMatrix verifies that NewProjectionDecoder with a nil
// demixing matrix creates a plain multistream decoder (no projection applied).
func TestProjectionDecoderNilDemixMatrix(t *testing.T) {
	dec, err := NewProjectionDecoder(48000, 4, 2, 2, nil)
	if err != nil {
		t.Fatalf("NewProjectionDecoder with nil matrix: %v", err)
	}
	if dec.Channels() != 4 {
		t.Errorf("Channels() = %d, want 4", dec.Channels())
	}
}

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
			switch {
			case config < 12:
				seen |= 1 // SILK
			case config < 16:
				seen |= 2 // Hybrid
			default:
				seen |= 4 // CELT
			}
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

func TestProjectionDecodeInt24MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 4
		frameCount = 6
	)
	mapping := trivialMapping(channels)
	for _, frameSize := range []int{480, 960} {
		for _, bitrate := range []int{96000, 128000, 192000, 256000} {
			t.Run(fmt.Sprintf("frame_%d/bitrate_%d", frameSize, bitrate), func(t *testing.T) {
				ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
				if !allPerStreamCELT(ref.packets, ref.streams) {
					t.Fatalf("expected all-CELT stream at frame=%d bitrate=%d", frameSize, bitrate)
				}
				dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
				if err != nil {
					t.Fatalf("NewProjectionDecoder: %v", err)
				}
				var got []int32
				for i, packet := range ref.packets {
					frame, err := dec.DecodeToInt24(packet, frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt24: %v", i, err)
					}
					got = append(got, frame...)
				}
				want, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection int24 reference decode", err)
				}
				if len(got) != len(want) {
					t.Fatalf("sample count mismatch Go=%d C=%d", len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("sample %d int24 Go/C=%d/%d", i, got[i], want[i])
					}
				}
			})
		}
	}
}

func TestProjectionDecodeIntoPrefilledBuffer(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 4
		frameSize = 960
		sentinel  = uint32(0x7fc12345)
	)
	ref := projectionDecodeRef(t, channels, frameSize, 2, 128000)
	if !allPerStreamCELT(ref.packets, ref.streams) {
		t.Fatal("projection packet did not exercise CELT decoding")
	}
	ownedDec, err := NewProjectionDecoder(48000, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	intoDec, err := NewProjectionDecoder(48000, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, 5760*channels+1)
	var firstOwned, firstSnapshot []float32
	for packetIndex, packet := range ref.packets {
		for i := range output {
			output[i] = math.Float32frombits(sentinel)
		}
		owned, err := ownedDec.DecodeToFloat32(packet, frameSize)
		if err != nil {
			t.Fatalf("packet %d owned decode: %v", packetIndex, err)
		}
		if packetIndex == 0 {
			firstOwned = owned
			firstSnapshot = append([]float32(nil), owned...)
		}
		n, err := intoDec.DecodeIntoFloat32(packet, output, 5760)
		if err != nil || n != frameSize {
			t.Fatalf("packet %d DecodeIntoFloat32=(%d,%v) want (%d,nil)", packetIndex, n, err, frameSize)
		}
		if len(owned) != n*channels {
			t.Fatalf("packet %d owned length=%d want %d", packetIndex, len(owned), n*channels)
		}
		for i, want := range owned {
			if math.Float32bits(output[i]) != math.Float32bits(want) {
				t.Fatalf("packet %d PCM[%d] bits=%08x want %08x", packetIndex, i, math.Float32bits(output[i]), math.Float32bits(want))
			}
		}
		for i := n * channels; i < len(output); i++ {
			if math.Float32bits(output[i]) != sentinel {
				t.Fatalf("packet %d output tail[%d] was overwritten", packetIndex, i)
			}
		}
	}
	if got := testing.AllocsPerRun(50, func() {
		if n, err := intoDec.DecodeIntoFloat32(ref.packets[0], output, frameSize); err != nil || n != frameSize {
			t.Fatalf("warm projection DecodeIntoFloat32=(%d,%v)", n, err)
		}
	}); got != 0 {
		t.Fatalf("warm projection DecodeIntoFloat32 allocations=%v want 0", got)
	}
	for i, want := range firstSnapshot {
		if math.Float32bits(firstOwned[i]) != math.Float32bits(want) {
			t.Fatalf("owned PCM[%d] changed after later decodes", i)
		}
	}
}

func projectionDecodeRef(t *testing.T, channels, frameSize, frameCount, bitrate int) *projectionEncodeRef {
	t.Helper()
	const (
		sampleRate     = 48000
		application    = 2049  // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
	)
	pcm := generateAmbisonicsSweep(channels, frameSize, frameCount)
	ref, err := encodeLibopusProjection(sampleRate, channels, application, bitrate, false, true,
		10, bandwidthAuto, frameSize, frameCount, maxPacketBytes, 0, pcm, nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection reference encode", err)
	}
	return ref
}

func trivialMapping(channels int) []byte {
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	return mapping
}

// decodeProjectionGopusFloat32 decodes every packet through a fresh gopus
// projection decoder and concatenates the interleaved float32 output.
func decodeProjectionGopusFloat32(sampleRate, channels, streams, coupled, frameSize int, demixing []byte, packets [][]byte) ([]float32, error) {
	dec, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return nil, fmt.Errorf("NewProjectionDecoder: %w", err)
	}
	var out []float32
	for i, pkt := range packets {
		frame, err := dec.DecodeToFloat32(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// decodeProjectionGopusInt16 decodes every packet through a fresh gopus
// projection decoder and concatenates the interleaved int16 output.
func decodeProjectionGopusInt16(sampleRate, channels, streams, coupled, frameSize int, demixing []byte, packets [][]byte) ([]int16, error) {
	dec, err := NewProjectionDecoder(sampleRate, channels, streams, coupled, demixing)
	if err != nil {
		return nil, fmt.Errorf("NewProjectionDecoder: %w", err)
	}
	var out []int16
	for i, pkt := range packets {
		frame, err := dec.DecodeToInt16(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// allPerStreamCELT reports whether every per-stream frame in every packet uses
// pure-CELT coding (TOC config >= 16). Hybrid configs are 12..15, SILK 0..11.
func allPerStreamCELT(packets [][]byte, streams int) bool {
	for _, pkt := range packets {
		sp, err := parseMultistreamPacket(pkt, streams)
		if err != nil {
			return false
		}
		for _, p := range sp {
			if len(p) == 0 {
				continue
			}
			if int(p[0]>>3) < 16 {
				return false
			}
		}
	}
	return true
}

// assertProjectionFloatSampleExact requires bit-exact float32 PCM against the
// same-feature, same-instruction libopus reference.
func assertProjectionFloatSampleExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	mismatches := 0
	maxAbs := 0.0
	firstIdx := -1
	for i := range got {
		if math.Float32bits(got[i]) == math.Float32bits(want[i]) {
			continue
		}
		d := math.Abs(float64(got[i] - want[i]))
		if d > maxAbs {
			maxAbs = d
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		mismatches++
	}
	if mismatches == 0 {
		return
	}
	if projectionUsesIntegerDecoderReference() {
		t.Fatalf("fixed projection float decode not bit-exact: %d/%d samples differ, maxAbs=%g (firstIdx=%d got=%g want=%g)",
			mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
	}
	t.Fatalf("%s decode not sample-exact: %d/%d samples differ, maxAbs=%g (firstIdx=%d got=%g want=%g)",
		label, mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

// assertProjectionInt16SampleExact requires bit-exact int16 PCM against the
// same-feature, same-instruction libopus reference.
func assertProjectionInt16SampleExact(t *testing.T, got, want []int16, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	mismatches := 0
	maxAbs := 0
	firstIdx := -1
	for i := range got {
		if got[i] == want[i] {
			continue
		}
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d > maxAbs {
			maxAbs = d
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		mismatches++
	}
	if mismatches == 0 {
		return
	}
	if projectionUsesIntegerDecoderReference() {
		t.Fatalf("fixed projection int16 decode not exact: %d/%d samples differ, maxAbs=%d (firstIdx=%d got=%d want=%d)",
			mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
	}
	t.Fatalf("%s decode not sample-exact: %d/%d samples differ, maxAbs=%d (firstIdx=%d got=%d want=%d)",
		label, mismatches, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

// TestProjectionDecodeMatchesLibopus locks gopus projection (ambisonics mapping
// family 3) DECODE to sample-exact parity against the libopus projection decode
// oracle for first-order ambisonics (FOA, 4 channels) at bitrates that select
// pure-CELT per-stream coding for every frame.
//
// Float32 (opus_projection_decode_float) and int16
// (opus_projection_decode) are compared exactly in each same-feature,
// same-instruction build.
func TestProjectionDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 4 // FOA
		frameCount = 6
	)
	// >=96 kbit/s keeps every FOA per-stream frame in pure-CELT mode.
	frameSizes := []int{480, 960} // 10 ms, 20 ms at 48 kHz
	bitrates := []int{96000, 128000, 192000, 256000}

	for _, frameSize := range frameSizes {
		for _, bitrate := range bitrates {
			name := fmt.Sprintf("foa-4ch/fs%d/br%d", frameSize, bitrate)
			t.Run(name, func(t *testing.T) {
				ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
				if !allPerStreamCELT(ref.packets, ref.streams) {
					t.Fatalf("expected all-CELT per-stream coding at br=%d fs=%d; got mixed modes", bitrate, frameSize)
				}
				mapping := trivialMapping(channels)

				gotFloat, err := decodeProjectionGopusFloat32(sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection float decode: %v", err)
				}
				wantFloat, err := decodeWithLibopusReferencePackets(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
				}
				assertProjectionFloatSampleExact(t, gotFloat, wantFloat, "float32")

				gotInt16, err := decodeProjectionGopusInt16(sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection int16 decode: %v", err)
				}
				wantInt16, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
				}
				assertProjectionInt16SampleExact(t, gotInt16, wantInt16, "int16")
			})
		}
	}
}

// TestProjectionDecodePerStreamModeClassification checks exact output for
// mixed-mode family-3 streams and confirms the fixtures include Hybrid/SILK
// per-stream frames. A failure reports whether an all-CELT or mixed-mode stream
// diverged.
//
// For each configuration it decodes the libopus-encoded family-3 stream through
// both gopus and the libopus projection decode oracle and asserts:
//
//   - every configuration is sample-exact against the selected C archive, and
//   - the matrix includes non-CELT per-stream frames as well as the all-CELT
//     cases covered by TestProjectionDecodeMatchesLibopus.
//
// This includes second-order ambisonics (SOA, 9 channels), whose per-stream
// frames are predominantly Hybrid/SILK at all tested bitrates.
func TestProjectionDecodePerStreamModeClassification(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameCount = 6
	)
	cases := []struct {
		name     string
		channels int
		frameSz  int
		bitrate  int
	}{
		{"foa-4ch/fs960/br64000", 4, 960, 64000},
		{"soa-9ch/fs480/br256000", 9, 480, 256000},
		{"soa-9ch/fs960/br128000", 9, 960, 128000},
		{"soa-9ch/fs960/br64000", 9, 960, 64000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := projectionDecodeRef(t, tc.channels, tc.frameSz, frameCount, tc.bitrate)
			mapping := trivialMapping(tc.channels)

			got, err := decodeProjectionGopusFloat32(sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSz, ref.demixing, ref.packets)
			if err != nil {
				t.Fatalf("gopus projection float decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(3, sampleRate, tc.channels, ref.streams, ref.coupledStreams, tc.frameSz, mapping, ref.demixing, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference decode", err)
			}
			if len(got) != len(want) {
				t.Fatalf("sample count mismatch gopus=%d libopus=%d", len(got), len(want))
			}

			maxAbs := 0.0
			firstIdx := -1
			for i := range got {
				if firstIdx < 0 && math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					firstIdx = i
				}
				d := math.Abs(float64(got[i] - want[i]))
				if d > maxAbs {
					maxAbs = d
				}
			}

			allCELT := allPerStreamCELT(ref.packets, ref.streams)
			if allCELT {
				t.Fatal("expected this mode-classification fixture to include a non-CELT per-stream frame")
			}
			if firstIdx >= 0 {
				t.Fatalf("mixed-mode projection stream is not sample-exact: maxAbs=%g firstIdx=%d got=%g want=%g", maxAbs, firstIdx, got[firstIdx], want[firstIdx])
			}
			t.Logf("sample-exact mixed-mode stream maxAbs=%g", maxAbs)
		})
	}
}

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

type projRobustSeed struct {
	channels  int
	streams   int
	coupled   int
	demixing  []byte
	frameSize int
	packet    []byte
}

// projRobustSeedPackets encodes valid family-3 projection packets across the
// supported ambisonics orders (FOA/SOA/TOA) and a couple of frame sizes via the
// libopus projection encoder, returning one packet + its layout per spec. These
// are the bases the mutator perturbs.
func projRobustSeedPackets(t *testing.T) []projRobustSeed {
	t.Helper()
	const (
		application    = 2049 // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000
		maxPacketBytes = 4000
		sampleRate     = 48000
	)
	var seeds []projRobustSeed
	for _, channels := range []int{4, 9, 16} {
		for _, fs := range []int{480, 960} {
			pcm := generateAmbisonicsSweep(channels, fs, 2)
			ref, err := encodeLibopusProjection(sampleRate, channels, application,
				128000, false, false, 10, bandwidthAuto, fs, 2, maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference encode", err)
				return seeds
			}
			if len(ref.packets) != 2 || len(ref.packets[0]) == 0 {
				t.Fatalf("projection seed ch%d frame%d: missing C packets", channels, fs)
			}
			seeds = append(seeds, projRobustSeed{
				channels:  channels,
				streams:   ref.streams,
				coupled:   ref.coupledStreams,
				demixing:  append([]byte(nil), ref.demixing...),
				frameSize: fs,
				packet:    append([]byte(nil), ref.packets[0]...),
			})
		}
	}
	return seeds
}

// projRobustMutate applies one seeded structured mutation to a copy of a valid
// projection packet.
func projRobustMutate(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(8) {
	case 0: // truncate (incl. 0 → empty/PLC)
		return p[:rng.Intn(len(p)+1)]
	case 1: // single byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite first stream TOC config
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // flip first stream TOC code bits
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // corrupt a self-delimited sub-packet length byte
		if len(p) >= 2 {
			i := 1 + rng.Intn(min(8, len(p)-1))
			p[i] = byte(rng.Intn(256))
		}
		return p
	case 6: // append junk
		n := 1 + rng.Intn(8)
		for range n {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	default: // scribble a short run
		n := 1 + rng.Intn(4)
		for range n {
			p[rng.Intn(len(p))] = byte(rng.Intn(256))
		}
		return p
	}
}

// projRobustFormat selects the matching Go and C public sample format.
type projRobustFormat int

const (
	projRobustFloat32 projRobustFormat = iota
	projRobustInt16
	projRobustInt24
)

// projRobustGopusDecode decodes one packet through a fresh gopus projection
// decoder, recovering a panic into an error so a crash minimises to one packet.
func projRobustGopusDecode(seed projRobustSeed, format projRobustFormat, packet []byte, capturePCM bool) (pcm []uint32, samples int, finalRange uint32, err error) {
	defer func() {
		if r := recover(); r != nil {
			pcm = nil
			samples = 0
			finalRange = 0
			err = fmt.Errorf("PANIC in gopus projection decode: %v", r)
		}
	}()
	dec, err := NewProjectionDecoder(48000, seed.channels, seed.streams, seed.coupled, seed.demixing)
	if err != nil {
		return nil, 0, 0, err
	}
	switch format {
	case projRobustInt16:
		out, e := dec.DecodeToInt16(packet, seed.frameSize)
		samples = len(out) / seed.channels
		finalRange = dec.FinalRange()
		if capturePCM {
			pcm = make([]uint32, len(out))
			for i, v := range out {
				pcm[i] = uint32(int32(v))
			}
		}
		err = e
	case projRobustInt24:
		out, e := dec.DecodeToInt24(packet, seed.frameSize)
		samples = len(out) / seed.channels
		finalRange = dec.FinalRange()
		if capturePCM {
			pcm = make([]uint32, len(out))
			for i, v := range out {
				pcm[i] = uint32(v)
			}
		}
		err = e
	default:
		out, e := dec.DecodeToFloat32(packet, seed.frameSize)
		samples = len(out) / seed.channels
		finalRange = dec.FinalRange()
		if capturePCM {
			pcm = make([]uint32, len(out))
			for i, v := range out {
				pcm[i] = math.Float32bits(v)
			}
		}
		err = e
	}
	return pcm, samples, finalRange, err
}

// projRobustOracleDecode preserves every output bit. Infrastructure failures
// remain errors; only the C helper's negative decode status means rejection.
func projRobustOracleDecode(seed projRobustSeed, format projRobustFormat, packet []byte) (pcm []uint32, rejected bool, err error) {
	mapping := trivialMapping(seed.channels)
	switch format {
	case projRobustInt16:
		out, e := decodeWithLibopusReferencePacketsInt16Gain(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, 0, mapping, seed.demixing, [][]byte{packet})
		pcm = make([]uint32, len(out))
		for i, v := range out {
			pcm[i] = uint32(int32(v))
		}
		err = e
	case projRobustInt24:
		out, e := decodeWithLibopusReferencePacketsInt24Gain(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, 0, mapping, seed.demixing, [][]byte{packet})
		pcm = make([]uint32, len(out))
		for i, v := range out {
			pcm[i] = uint32(v)
		}
		err = e
	default:
		out, e := decodeWithLibopusReferencePackets(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, mapping, seed.demixing, [][]byte{packet})
		pcm = make([]uint32, len(out))
		for i, v := range out {
			pcm[i] = math.Float32bits(v)
		}
		err = e
	}
	if projRobustOracleRejected(err) {
		return nil, true, nil
	}
	return pcm, false, err
}

func projRobustOracleRejected(err error) bool {
	var child *exec.ExitError
	if !errors.As(err, &child) || child.ExitCode() != 1 {
		return false
	}
	const marker = "(opus_projection_decode_float failed: "
	message := err.Error()
	start := strings.LastIndex(message, marker)
	if start < 0 || !strings.HasSuffix(message, ")") {
		return false
	}
	code, e := strconv.ParseInt(strings.TrimSuffix(message[start+len(marker):], ")"), 10, 32)
	return e == nil && code < 0
}

func TestProjectionRobustOracleErrorClassification(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) != 6 {
		t.Fatalf("seed count=%d, want 6", len(seeds))
	}
	for _, format := range []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24} {
		_, rejected, err := projRobustOracleDecode(seeds[0], format, []byte{0xff})
		if err != nil || !rejected {
			t.Fatalf("format %d: rejected=%v error=%v, want C packet rejection", format, rejected, err)
		}
	}
	for _, message := range []string{
		"compile projection reference: compiler failed",
		"run helper: exit status 1 (failed to read packet payload)",
		"projection helper: truncated output header",
		"run helper: exit status 1 (opus_projection_decode_float failed: -4",
		"run helper: exit status 1 (opus_projection_decode_float failed: -4)",
	} {
		if projRobustOracleRejected(errors.New(message)) {
			t.Fatalf("infrastructure error classified as packet rejection: %s", message)
		}
	}
}

func projIsRobustPanic(err error) bool {
	return err != nil && len(err.Error()) >= 5 && err.Error()[:5] == "PANIC"
}

// TestProjectionDecodeRobustnessRandom feeds random byte buffers to the
// projection decoder across the supported orders and asserts NO panic and a
// sample count within the requested frame size.
func TestProjectionDecodeRobustnessRandom(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Fatal("no projection seed packets")
	}

	const maxLen = 160
	rng := rand.New(rand.NewSource(0x9203A))
	iters := fuzzBudget(4000)
	cases := 0
	buf := make([]byte, maxLen)
	for range iters {
		seed := seeds[rng.Intn(len(seeds))]
		n := rng.Intn(maxLen + 1)
		buf = buf[:n]
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		for _, format := range []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24} {
			_, samples, _, err := projRobustGopusDecode(seed, format, buf, false)
			cases++
			if projIsRobustPanic(err) {
				t.Fatalf("ch%d/fmt%d: %v — packet=% x", seed.channels, format, err, buf)
			}
			if err == nil && (samples < 0 || samples > seed.frameSize) {
				t.Fatalf("ch%d/fmt%d: samples=%d outside [0,%d] — packet=% x", seed.channels, format, samples, seed.frameSize, buf)
			}
		}
	}
	t.Logf("projection random no-panic sweep: %d cases over %d seeds", cases, len(seeds))
}

// TestProjectionDecodeRobustnessMalformed mutates valid family-3 packets and
// asserts gopus and the libopus projection oracle agree on accept-vs-reject and
// exact PCM bits in the selected format, with NO panic.
func TestProjectionDecodeRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Fatal("no projection seed packets")
	}

	rng := rand.New(rand.NewSource(0x9203AB0F))
	iters := fuzzBudget(4000)
	formats := []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24}
	total := 0
	var accepted, rejectedCounts [3]int
	const batchSize = 128
	for base := 0; base < iters; base += batchSize {
		count := min(batchSize, iters-base)
		type malformedCase struct {
			seed   projRobustSeed
			format projRobustFormat
			packet []byte
			label  string
			got    []uint32
			gn     int
			grange uint32
			gerr   error
		}
		cases := make([]malformedCase, count)
		oracleCases := make([]libopustest.MultistreamDecodeCase, count)
		for i := range cases {
			k := base + i
			seed := seeds[rng.Intn(len(seeds))]
			m := projRobustMutate(rng, seed.packet)
			format := formats[rng.Intn(len(formats))]
			label := fmt.Sprintf("ch%d/fmt%d/mut%d", seed.channels, format, k)

			got, gn, grange, gerr := projRobustGopusDecode(seed, format, m, true)
			if projIsRobustPanic(gerr) {
				t.Fatalf("%s: %v — packet=% x", label, gerr, m)
			}
			cases[i] = malformedCase{seed, format, m, label, got, gn, grange, gerr}
			oracleCases[i] = libopustest.MultistreamDecodeCase{
				SampleRate: 48000,
				Format:     projRobustOracleFormat(format),
				Family:     3,
				Channels:   uint32(seed.channels),
				Streams:    uint32(seed.streams),
				Coupled:    uint32(seed.coupled),
				FrameSize:  uint32(seed.frameSize),
				Mapping:    trivialMapping(seed.channels),
				Demixing:   seed.demixing,
				Packet:     m,
			}
		}

		oracleResults, oerr := libopustest.ProbeMultistreamDecodeFresh(oracleCases)
		if oerr != nil {
			libopustest.HelperUnavailable(t, "projection reference decode", oerr)
			return
		}
		if len(oracleResults) != len(cases) {
			t.Fatalf("projection reference decode returned %d results for %d cases", len(oracleResults), len(cases))
		}
		for i, tc := range cases {
			oracle := oracleResults[i]
			want, pcmErr := projRobustPCMFromOracle(tc.format, oracle.PCM)
			if pcmErr != nil {
				libopustest.HelperUnavailable(t, "projection reference decode", pcmErr)
				return
			}
			on := int(oracle.Code)
			rejected := oracle.Code < 0
			total++

			if rejected {
				rejectedCounts[tc.format]++
				if tc.gerr == nil {
					t.Errorf("%s: libopus REJECTED but gopus ACCEPTED (n=%d) — packet=% x", tc.label, tc.gn, tc.packet)
				}
				continue
			}
			if tc.gerr != nil {
				t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x", tc.label, on, tc.gerr, tc.packet)
				continue
			}
			accepted[tc.format]++
			if tc.grange != oracle.FinalRange {
				t.Errorf("%s: final range gopus=%08x libopus=%08x — packet=% x", tc.label, tc.grange, oracle.FinalRange, tc.packet)
			}
			if !slices.Equal(tc.got, want) {
				first := 0
				for first < min(len(tc.got), len(want)) && tc.got[first] == want[first] {
					first++
				}
				t.Errorf("%s: PCM differs at sample %d; lengths Go=%d C=%d — packet=% x", tc.label, first, len(tc.got), len(want), tc.packet)
			}
		}
	}
	for _, format := range formats {
		if accepted[format] == 0 || rejectedCounts[format] == 0 {
			t.Fatalf("format %d lacks accepted/rejected coverage: %d/%d", format, accepted[format], rejectedCounts[format])
		}
	}
	t.Logf("projection malformed sweep: %d cases; accepted float/int16/int24=%v, rejected=%v", total, accepted, rejectedCounts)
}

func projRobustOracleFormat(format projRobustFormat) uint32 {
	switch format {
	case projRobustInt16:
		return libopustest.MultistreamDecodeFormatInt16
	case projRobustInt24:
		return libopustest.MultistreamDecodeFormatInt24
	default:
		return libopustest.MultistreamDecodeFormatFloat32
	}
}

func projRobustPCMFromOracle(format projRobustFormat, raw []byte) ([]uint32, error) {
	sampleBytes := 4
	if format == projRobustInt16 {
		sampleBytes = 2
	}
	if len(raw)%sampleBytes != 0 {
		return nil, fmt.Errorf("PCM bytes=%d is not a multiple of sample width %d", len(raw), sampleBytes)
	}
	pcm := make([]uint32, len(raw)/sampleBytes)
	for i := range pcm {
		switch format {
		case projRobustInt16:
			pcm[i] = uint32(int32(int16(binary.LittleEndian.Uint16(raw[2*i:]))))
		default:
			pcm[i] = binary.LittleEndian.Uint32(raw[4*i:])
		}
	}
	return pcm, nil
}

func matrixBytes(vals ...int16) []byte {
	out := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(out[2*i:2*i+2], uint16(v))
	}
	return out
}

func TestSetProjectionDemixingMatrixInvalidSize(t *testing.T) {
	dec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	err = dec.SetProjectionDemixingMatrix([]byte{1, 2, 3})
	if !errors.Is(err, ErrInvalidProjectionMatrix) {
		t.Fatalf("SetProjectionDemixingMatrix error = %v, want %v", err, ErrInvalidProjectionMatrix)
	}
}

func TestApplyProjectionDemixingSwapsChannels(t *testing.T) {
	dec, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	// Column-major 2x2 swap matrix:
	// [0 1]
	// [1 0]
	if err := dec.SetProjectionDemixingMatrix(matrixBytes(0, 32767, 32767, 0)); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix failed: %v", err)
	}

	pcm := []float32{1, 2, 3, 4}
	dec.applyProjectionDemixing32(pcm, 2)

	scale := float32(32767.0 / 32768.0)
	want := []float32{2 * scale, 1 * scale, 4 * scale, 3 * scale}
	for i := range want {
		if math.Abs(float64(pcm[i]-want[i])) > 1e-3 {
			t.Fatalf("sample[%d] = %f, want %f", i, pcm[i], want[i])
		}
	}
}

func TestSetProjectionDemixingMatrixFromFamily3Header(t *testing.T) {
	head := oggcontainer.DefaultOpusHeadMultistreamWithFamily(48000, 4, oggcontainer.MappingFamilyProjection, 2, 2, nil)
	dec, err := NewDecoder(48000, 4, 2, 2, []byte{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("NewDecoder failed: %v", err)
	}

	if err := dec.SetProjectionDemixingMatrix(head.DemixingMatrix); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix failed: %v", err)
	}
	if got, want := len(dec.projectionDemixing), 16; got != want {
		t.Fatalf("projectionDemixing len = %d, want %d", got, want)
	}
}

var (
	projDemixingCTLOnce sync.Once
	projDemixingCTLPath string
	projDemixingCTLErr  error
)

func getProjectionDemixingCTLPath() (string, error) {
	projDemixingCTLOnce.Do(func() {
		if _, err := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); err != nil {
			projDemixingCTLErr = err
			return
		}
		projDemixingCTLPath, projDemixingCTLErr = buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			Label:      "projection demixing CTL",
			OutputBase: "gopus_libopus_projection_demixing_ctl",
			SourceFile: "libopus_projection_demixing_ctl.c",
			CFlags:     []string{"-O2", "-DNDEBUG"},
			Libs:       []string{"-lm"},
		})
	})
	return projDemixingCTLPath, projDemixingCTLErr
}

type projDemixingCTLResult struct {
	streams  int
	coupled  int
	demixing []byte
	gain     int
}

// probeProjectionDemixingCTL calls the libopus oracle to get the demixing matrix and gain
// for a family-3 projection encoder created with opus_projection_ambisonics_encoder_create.
//
// This exercises:
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_GAIN_REQUEST
//   - OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
func probeProjectionDemixingCTL(t *testing.T, sampleRate, channels, application int) projDemixingCTLResult {
	t.Helper()
	binPath, err := getProjectionDemixingCTLPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "projection demixing CTL", err)
	}

	// NewOraclePayload writes magic + version(1) + header fields.
	// C oracle expects: GPDI version(1) sample_rate channels application
	payload := libopustest.NewOraclePayload(
		"GPDI",
		uint32(sampleRate),
		uint32(channels),
		uint32(application),
	)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "projection demixing CTL", "GPDO")
	if err != nil {
		libopustest.HelperUnavailable(t, "projection demixing CTL", err)
	}

	// RunOracle already consumed the magic+version header; next fields are streams, coupled.
	streams := int(reader.U32())
	coupled := int(reader.U32())
	demixSize := int(reader.U32())
	reader.ExpectRemaining(demixSize + 4)
	demixBytes := reader.Bytes(demixSize)
	gain := int(int32(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	return projDemixingCTLResult{
		streams:  streams,
		coupled:  coupled,
		demixing: demixBytes,
		gain:     gain,
	}
}

// TestProjectionDemixingMatrixCTLParityOrders validates GetDemixingMatrix() against
// opus_projection_ambisonics_encoder_create + OPUS_PROJECTION_GET_DEMIXING_MATRIX
// for all supported ambisonics orders (1-5) with and without non-diegetic channels.
//
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_REQUEST
func TestProjectionDemixingMatrixCTLParityOrders(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := []struct {
		name     string
		channels int
	}{
		// Order 1 (FOA)
		{name: "foa-4ch", channels: 4},
		{name: "foa-nd-6ch", channels: 6},
		// Order 2 (SOA)
		{name: "soa-9ch", channels: 9},
		{name: "soa-nd-11ch", channels: 11},
		// Order 3 (TOA)
		{name: "toa-16ch", channels: 16},
		{name: "toa-nd-18ch", channels: 18},
		// Order 4 (4thOA)
		{name: "fourthoa-25ch", channels: 25},
		{name: "fourthoa-nd-27ch", channels: 27},
		// Order 5 (5thOA)
		{name: "fifthoa-36ch", channels: 36},
		{name: "fifthoa-nd-38ch", channels: 38},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// OPUS_APPLICATION_AUDIO = 2049
			ref := probeProjectionDemixingCTL(t, 48000, tc.channels, 2049)

			enc, err := NewEncoderAmbisonics(48000, tc.channels, 3)
			if err != nil {
				t.Fatalf("NewEncoderAmbisonics(%d, 3): %v", tc.channels, err)
			}

			if enc.Streams() != ref.streams {
				t.Errorf("Streams() = %d, want %d", enc.Streams(), ref.streams)
			}
			if enc.CoupledStreams() != ref.coupled {
				t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), ref.coupled)
			}

			gotSize := enc.DemixingMatrixSize()
			if gotSize != len(ref.demixing) {
				t.Errorf("DemixingMatrixSize() = %d, want %d", gotSize, len(ref.demixing))
			}

			gotGain := enc.DemixingMatrixGain()
			if gotGain != ref.gain {
				t.Errorf("DemixingMatrixGain() = %d, want %d", gotGain, ref.gain)
			}

			gotMatrix := enc.GetDemixingMatrix()
			if len(gotMatrix) != len(ref.demixing) {
				t.Fatalf("GetDemixingMatrix() len = %d, want %d", len(gotMatrix), len(ref.demixing))
			}

			n := len(ref.demixing) / 2
			for i := range n {
				got := int16(binary.LittleEndian.Uint16(gotMatrix[2*i : 2*i+2]))
				want := int16(binary.LittleEndian.Uint16(ref.demixing[2*i : 2*i+2]))
				if got != want {
					t.Errorf("demixing[%d] = %d, want %d", i, got, want)
				}
			}
		})
	}
}

// TestProjectionDemixingMatrixSize validates DemixingMatrixSize formula matches libopus.
//
// libopus formula: channels * (streams + coupled_streams) * sizeof(opus_int16)
// Reference: libopus src/opus_projection_encoder.c:opus_projection_encoder_ctl
// OPUS_PROJECTION_GET_DEMIXING_MATRIX_SIZE_REQUEST
func TestProjectionDemixingMatrixSize(t *testing.T) {
	cases := []struct {
		channels int
		streams  int
		coupled  int
		want     int
	}{
		{4, 2, 2, 4 * 4 * 2},    // 4ch FOA: 4 inputs × 4 outputs × 2 bytes
		{6, 3, 3, 6 * 6 * 2},    // 6ch FOA+nd: 6 inputs × 6 outputs × 2 bytes
		{9, 5, 4, 9 * 9 * 2},    // 9ch SOA
		{16, 8, 8, 16 * 16 * 2}, // 16ch TOA
	}
	for _, tc := range cases {
		got := ProjectionDemixingMatrixSize(tc.channels, tc.streams, tc.coupled)
		if got != tc.want {
			t.Errorf("ProjectionDemixingMatrixSize(%d,%d,%d) = %d, want %d",
				tc.channels, tc.streams, tc.coupled, got, tc.want)
		}
	}
}

// TestGetDemixingMatrixNonProjection validates that GetDemixingMatrix returns nil
// for non-projection encoders (family != 3).
func TestGetDemixingMatrixNonProjection(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6) // family 1
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	if got := enc.GetDemixingMatrix(); got != nil {
		t.Errorf("GetDemixingMatrix() on non-projection encoder = %v, want nil", got)
	}
	if got := enc.DemixingMatrixGain(); got != 0 {
		t.Errorf("DemixingMatrixGain() on non-projection encoder = %d, want 0", got)
	}
	if got := enc.DemixingMatrixSize(); got != 0 {
		t.Errorf("DemixingMatrixSize() on non-projection encoder = %d, want 0", got)
	}
}

// TestGetDemixingMatrixMatchesDecoder validates that GetDemixingMatrix from the encoder
// can be used to initialize a projection decoder that produces correct output.
func TestGetDemixingMatrixMatchesDecoder(t *testing.T) {
	channels := 4
	enc, err := NewEncoderAmbisonics(48000, channels, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics: %v", err)
	}

	demix := enc.GetDemixingMatrix()
	if demix == nil {
		t.Fatal("GetDemixingMatrix() returned nil")
	}

	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	dec, err := NewDecoder(48000, channels, enc.Streams(), enc.CoupledStreams(), mapping)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	if err := dec.SetProjectionDemixingMatrix(demix); err != nil {
		t.Fatalf("SetProjectionDemixingMatrix: %v", err)
	}

	// Encode and decode a frame to verify the pipeline works end-to-end.
	frameSize := 960
	pcm := generateMultichannelSine(channels, frameSize)
	packet, err := encodePacket(enc, pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if packet == nil {
		t.Fatal("Encode returned nil (DTX)")
	}

	out, err := dec.DecodeToFloat32(packet, frameSize)
	if err != nil {
		t.Fatalf("DecodeToFloat32: %v", err)
	}
	if len(out) != frameSize*channels {
		t.Fatalf("output len = %d, want %d", len(out), frameSize*channels)
	}

	energy := computeEnergyF32(out)
	if energy < 1e-6 {
		t.Fatalf("output energy too low: %e (silent output)", energy)
	}
}

var projectionEncodeHelper libopustest.HelperCache

// projectionEncodeRef holds the result of driving libopus
// opus_projection_ambisonics_encoder_create + opus_projection_encode_float /
// opus_projection_encode through the projection_encode_oracle C helper.
type projectionEncodeRef struct {
	streams        int
	coupledStreams int
	demixing       []byte
	demixingGain   int
	packets        [][]byte
	ranges         []uint32
}

// encodeLibopusProjection runs the libopus projection encoder oracle for the
// given parameters and PCM. When sampleFormat is 1 (int16) pcm16 is used and the
// oracle drives opus_projection_encode; otherwise pcm32 is used with
// opus_projection_encode_float.
func encodeLibopusProjection(sampleRate, channels, application, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes, sampleFormat int, pcm32 []float32, pcm16 []int16) (*projectionEncodeRef, error) {
	binPath, err := projectionEncodeHelper.Path(func() (string, error) {
		return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			Label:      "projection reference encode",
			OutputBase: "gopus_libopus_projection_encode_oracle",
			SourceFile: "libopus_projection_encode_oracle.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			Libs:       []string{"-lm"},
		})
	})
	if err != nil {
		return nil, err
	}

	boolU32 := func(b bool) uint32 {
		if b {
			return 1
		}
		return 0
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GPEI",
		2,
		uint32(sampleRate),
		uint32(channels),
		uint32(application),
		uint32(int32(bitrate)),
		boolU32(vbr),
		boolU32(vbrConstraint),
		uint32(complexity),
		uint32(int32(bandwidth)),
		uint32(frameSize),
		uint32(frameCount),
		uint32(maxPacketBytes),
		uint32(sampleFormat),
	)
	if sampleFormat == 1 {
		for _, s := range pcm16 {
			payload.I16(s)
		}
	} else {
		payload.Float32s(pcm32...)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "projection reference encode", "GPEO", 2)
	if err != nil {
		return nil, err
	}

	streams := int(reader.U32())
	coupled := int(reader.U32())
	demixSize := int(reader.U32())
	demixing := append([]byte(nil), reader.Bytes(demixSize)...)
	demixGain := int(int32(reader.U32()))

	packetCount := reader.Count(frameCount)
	packets := make([][]byte, packetCount)
	ranges := make([]uint32, packetCount)
	for i := range packets {
		ranges[i] = reader.U32()
		n := int(reader.U32())
		packets[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return &projectionEncodeRef{
		streams:        streams,
		coupledStreams: coupled,
		demixing:       demixing,
		demixingGain:   demixGain,
		packets:        packets,
		ranges:         ranges,
	}, nil
}

// generateAmbisonicsSweep builds a multi-frame ambisonics PCM buffer with a
// distinct per-channel tone so the projection mixing matrix sees non-trivial
// inter-channel energy across the W/X/Y/Z... components.
func generateAmbisonicsSweep(channels, frameSize, frameCount int) []float32 {
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	n := frameSize * frameCount
	for s := range n {
		tt := float64(s) / 48000.0
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
		for ch := range channels {
			freq := 110.0 * float64(ch+1)
			pcm[s*channels+ch] = float32(amp * math.Sin(2*math.Pi*freq*tt))
		}
	}
	return pcm
}

// floatToInt16 constructs exact short PCM for both projection encoders.
func floatToInt16(pcm []float32) []int16 {
	out := make([]int16, len(pcm))
	for i, v := range pcm {
		scaled := math.Round(float64(v) * 32768.0)
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		out[i] = int16(scaled)
	}
	return out
}

func compareProjectionDemixing(t *testing.T, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("demixing matrix size mismatch: gopus=%d libopus=%d", len(got), len(want))
	}
	n := len(want) / 2
	for i := range n {
		g := int16(binary.LittleEndian.Uint16(got[2*i : 2*i+2]))
		w := int16(binary.LittleEndian.Uint16(want[2*i : 2*i+2]))
		if g != w {
			t.Fatalf("demixing[%d] mismatch: gopus=%d libopus=%d", i, g, w)
		}
	}
}

func firstByteMismatch(a, b []byte) int {
	n := min(len(b), len(a))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// perStreamConfigs returns the Opus TOC config (TOC>>3) of every stream in a
// multistream packet, or nil if the packet cannot be split. Equal config lists
// mean every per-stream encoder selected the same mode/bandwidth/frame duration,
// which isolates per-stream mode-decision divergence from CELT bit drift.
func perStreamConfigs(packet []byte, streams int) []int {
	sp, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		return nil
	}
	cfgs := make([]int, 0, len(sp))
	for _, p := range sp {
		if len(p) == 0 {
			cfgs = append(cfgs, -1)
			continue
		}
		cfgs = append(cfgs, int(p[0]>>3))
	}
	return cfgs
}

// runProjectionEncodeParity calls the matching float or short public entry in
// both implementations and checks every packet byte and entropy-coder range.
func runProjectionEncodeParity(t *testing.T, channels, frameSize, frameCount, bitrate, complexity, sampleFormat int, vbr, vbrConstraint bool) {
	t.Helper()
	const (
		sampleRate     = 48000
		application    = 2049
		bandwidthAuto  = -1000
		maxPacketBytes = 4000
	)
	pcm := generateAmbisonicsSweep(channels, frameSize, frameCount)
	var pcm16 []int16
	if sampleFormat == 1 {
		pcm16 = floatToInt16(pcm)
	}
	ref, err := encodeLibopusProjection(sampleRate, channels, application, bitrate, vbr, vbrConstraint,
		complexity, bandwidthAuto, frameSize, frameCount, maxPacketBytes, sampleFormat, pcm, pcm16)
	if err != nil {
		t.Fatalf("live C projection encode: %v", err)
	}
	enc, err := NewProjectionEncoder(sampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}
	compareProjectionDemixing(t, enc.GetDemixingMatrix(), ref.demixing)
	if enc.DemixingMatrixGain() != ref.demixingGain {
		t.Fatalf("demixing gain Go=%d C=%d", enc.DemixingMatrixGain(), ref.demixingGain)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(vbrConstraint)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()
	out := make([]byte, maxPacketBytes)
	for frame := range frameCount {
		start := frame * frameSize * channels
		var got []byte
		if sampleFormat == 1 {
			input := pcm16[start : start+frameSize*channels]
			n, err := enc.EncodeInt16WithAnalysis(input, frameSize, input, out)
			if err != nil {
				t.Fatalf("frame %d Go short encode: %v", frame, err)
			}
			got = out[:n]
		} else {
			input := pcm[start : start+frameSize*channels]
			var err error
			got, err = encodePacketMax(enc, input, frameSize, input, maxPacketBytes)
			if err != nil {
				t.Fatalf("frame %d Go float encode: %v", frame, err)
			}
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x configs Go/C=%v/%v", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame], perStreamConfigs(got, enc.Streams()), perStreamConfigs(ref.packets[frame], ref.streams))
		}
	}
}

// TestProjectionEncodeMatchesLibopus checks float projection packets and final
// ranges for first- and second-order ambisonics across CBR and constrained VBR.
func TestProjectionEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	orders := []struct {
		name     string
		channels int
	}{
		{"foa-4ch", 4},
		{"soa-9ch", 9},
	}
	frameSizes := []int{480, 960} // 10 ms, 20 ms at 48 kHz
	bitrates := []int{64000, 256000}

	for _, order := range orders {
		for _, frameSize := range frameSizes {
			for _, bitrate := range bitrates {
				for _, vbr := range []bool{false, true} {
					name := fmt.Sprintf("%s/fs%d/br%d/vbr%t", order.name, frameSize, bitrate, vbr)
					t.Run(name, func(t *testing.T) {
						runProjectionEncodeParity(t, order.channels, frameSize, 6, bitrate, 10, 0, vbr, true)
					})
				}
			}
		}
	}
}

// TestProjectionEncodeInt16MatchesLibopus checks the actual short projection
// callback, packets, and final ranges for first- and second-order ambisonics.
func TestProjectionEncodeInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, channels := range []int{4, 9} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			runProjectionEncodeParity(t, channels, 960, 6, 256000, 10, 1, false, true)
		})
	}
}

func TestProjectionInitialMonoDecisionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 9
		frameSize  = 480
		frameCount = 5
		bitrate    = 64000
		maxPacket  = 4000
		seed       = 0x90c2
	)
	pcm := seededMultichannelPCM(seed, channels, frameSize, frameCount)
	ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, false, false,
		10, -1000, frameSize, frameCount, maxPacket, 0, pcm, nil)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection first-frame reference", err)
		return
	}
	if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
		t.Fatalf("C records packets=%d ranges=%d, want %d", len(ref.packets), len(ref.ranges), frameCount)
	}
	enc, err := NewProjectionEncoder(48000, channels)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	enc.SetComplexity(10)
	enc.SetBandwidthAuto()

	for pass := range 2 {
		if pass != 0 {
			enc.Reset()
		}
		for frame := range frameCount {
			start := frame * frameSize * channels
			input := pcm[start : start+frameSize*channels]
			got, encodeErr := encodePacketMax(enc, input, frameSize, input, maxPacket)
			if encodeErr != nil {
				t.Fatalf("pass %d frame %d: Go encode: %v", pass, frame, encodeErr)
			}
			want := ref.packets[frame]
			if !bytes.Equal(got, want) {
				t.Errorf("pass %d frame %d: packet len Go=%d C=%d firstByte=%d configs Go=%v C=%v",
					pass, frame, len(got), len(want), firstByteMismatch(got, want),
					perStreamConfigs(got, enc.Streams()), perStreamConfigs(want, ref.streams))
			}
			if gotRange := enc.GetFinalRange(); gotRange != ref.ranges[frame] {
				t.Errorf("pass %d frame %d: final range Go=%08x C=%08x", pass, frame, gotRange, ref.ranges[frame])
			}
			goStreams, goErr := parseMultistreamPacket(got, enc.Streams())
			cStreams, cErr := parseMultistreamPacket(want, ref.streams)
			if goErr != nil || cErr != nil {
				t.Fatalf("pass %d frame %d: parse Go=%v C=%v", pass, frame, goErr, cErr)
			}
			if len(goStreams) != ref.streams || len(cStreams) != ref.streams {
				t.Fatalf("pass %d frame %d: child count Go=%d C=%d want=%d",
					pass, frame, len(goStreams), len(cStreams), ref.streams)
			}
			for stream := range goStreams {
				if !bytes.Equal(goStreams[stream], cStreams[stream]) {
					t.Errorf("pass %d frame %d stream %d: child len Go=%d C=%d firstByte=%d",
						pass, frame, stream, len(goStreams[stream]), len(cStreams[stream]),
						firstByteMismatch(goStreams[stream], cStreams[stream]))
				}
			}
		}
	}
}

func TestProjectionInt16PacketRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameCount = 24
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, tc := range []struct {
			channels  int
			frameSize int
			bitrate   int
			vbr       bool
		}{
			{4, 960, 256000, true},
			{9, 960, 256000, true},
			{16, 960, 384000, true},
			{4, 240, 128000, false},
		} {
			t.Run(fmt.Sprintf("rate%d/ch%d_fs%d_br%d_vbr%t", rate, tc.channels, tc.frameSize, tc.bitrate, tc.vbr), func(t *testing.T) {
				tc.frameSize = tc.frameSize * rate / 48000
				pcm := floatToInt16(generateAmbisonicsSweep(tc.channels, tc.frameSize, frameCount))
				ref, err := encodeLibopusProjection(rate, tc.channels, 2049, tc.bitrate, tc.vbr, true,
					10, -1000, tc.frameSize, frameCount, 4000, 1, nil, pcm)
				if err != nil {
					t.Fatalf("live C projection encode: %v", err)
				}
				enc, err := NewProjectionEncoder(rate, tc.channels)
				if err != nil {
					t.Fatal(err)
				}
				if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
					t.Fatalf("layout Go=(%d,%d) C=(%d,%d)", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
				}
				enc.SetBitrate(tc.bitrate)
				enc.SetVBR(tc.vbr)
				enc.SetVBRConstraint(true)
				enc.SetComplexity(10)
				enc.SetBandwidthAuto()
				out := make([]byte, 4000)
				for frame := range frameCount {
					start := frame * tc.frameSize * tc.channels
					input := pcm[start : start+tc.frameSize*tc.channels]
					n, err := enc.EncodeInt16WithAnalysis(input, tc.frameSize, input, out)
					if err != nil {
						t.Fatalf("frame %d: %v", frame, err)
					}
					if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
						t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x GoCfg=%v CCfg=%v", frame,
							firstByteMismatch(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame],
							perStreamConfigs(out[:n], enc.Streams()), perStreamConfigs(ref.packets[frame], ref.streams))
					}
				}
				last := pcm[(frameCount-1)*tc.frameSize*tc.channels:]
				if _, err := enc.EncodeInt16WithAnalysis(last, tc.frameSize, last, out); err != nil {
					t.Fatalf("warm short encode: %v", err)
				}
				if allocs := testing.AllocsPerRun(20, func() {
					if _, err := enc.EncodeInt16WithAnalysis(last, tc.frameSize, last, out); err != nil {
						t.Fatalf("short encode: %v", err)
					}
				}); allocs != 0 {
					t.Fatalf("warm short caller-buffer allocations=%g want 0", allocs)
				}
			})
		}
	}
}

func TestMultistreamShortCodingAndAnalysisInputsRemainSeparate(t *testing.T) {
	enc, err := NewEncoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	const frameSize = 960
	coding := make([]int16, 2*frameSize)
	analysis := make([]int16, 2*frameSize)
	coding[0], coding[1] = 12345, -23456
	analysis[0], analysis[1] = -11111, 22222
	if _, err := enc.EncodeInt16WithAnalysis(coding, frameSize, analysis, make([]byte, 4000)); err != nil {
		t.Fatal(err)
	}
	const scale = float32(1.0 / 32768.0)
	for ch := range 2 {
		if got, want := enc.streamInputScratch[0][ch], float32(coding[ch])*scale; got != want {
			t.Errorf("coding channel %d=%g want %g", ch, got, want)
		}
		if got, want := enc.analysisInputScratch[0][ch], float32(analysis[ch])*scale; got != want {
			t.Errorf("analysis channel %d=%g want %g", ch, got, want)
		}
	}
}

func TestProjectionLongFloatMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frameCount = 960, 24
	for _, channels := range []int{9, 16} {
		pcm16 := floatToInt16(generateAmbisonicsSweep(channels, frameSize, frameCount))
		pcm := make([]float32, len(pcm16))
		for i, s := range pcm16 {
			pcm[i] = float32(s) / 32768
		}
		bitrate := 256000
		if channels == 16 {
			bitrate = 384000
		}
		ref, err := encodeLibopusProjection(48000, channels, 2049, bitrate, true, true, 10, -1000, frameSize, frameCount, 4000, 0, pcm, nil)
		if err != nil {
			t.Fatal(err)
		}
		enc, err := NewProjectionEncoder(48000, channels)
		if err != nil {
			t.Fatal(err)
		}
		enc.SetBitrate(bitrate)
		enc.SetVBR(true)
		enc.SetVBRConstraint(true)
		enc.SetComplexity(10)
		enc.SetBandwidthAuto()
		for f := range frameCount {
			p := pcm[f*frameSize*channels : (f+1)*frameSize*channels]
			got, err := encodePacketMax(enc, p, frameSize, p, 4000)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, ref.packets[f]) || enc.GetFinalRange() != ref.ranges[f] {
				t.Fatalf("channels=%d frame=%d byte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", channels, f, firstByteMismatch(got, ref.packets[f]), len(got), len(ref.packets[f]), enc.GetFinalRange(), ref.ranges[f])
			}
		}
	}
}

const (
	projectionMatrixModeOutFloat = uint32(0)
	projectionMatrixModeOutShort = uint32(1)
)

var projectionMatrixHelper libopustest.HelperCache

func probeLibopusProjectionMatrix(t *testing.T, mode uint32, rows, cols, frameSize int, matrix []int16, input []float32) (*libopustest.OracleReader, []float32) {
	t.Helper()
	binPath, err := projectionMatrixHelper.Path(func() (string, error) {
		return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			Label:        "projection matrix",
			OutputBase:   "gopus_libopus_projection_matrix",
			SourceFile:   "libopus_projection_matrix_info.c",
			ProbeRelPath: "src/mapping_matrix.h",
			CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes:  []string{"celt", "src"},
			Libs:         []string{"-lm"},
			DeadStrip:    true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "projection matrix", err)
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GPMI", 2,
		mode,
		uint32(rows),
		uint32(cols),
		uint32(frameSize),
	)
	for _, v := range matrix {
		payload.I16(v)
	}
	for _, v := range input {
		payload.Float32(v)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "projection matrix", "GPMO", 2)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection matrix", err)
	}
	// Fixed C converts the protocol's float PCM to opus_res before matrix
	// multiplication. Compare the matrix on those same normalized samples.
	count := reader.Count(cols * frameSize)
	if count != len(input) {
		t.Fatalf("matrix input count=%d want %d", count, len(input))
	}
	matrixInput := make([]float32, count)
	for i := range matrixInput {
		matrixInput[i] = reader.Float32()
	}
	return reader, matrixInput
}

func TestProjectionDemixingFloatMatchesLibopusMatrixOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		rows      = 4
		cols      = 4
		frameSize = 3
	)
	matrix := []int16{
		16384, -8192, 32767, -16384,
		23170, 0, -23170, 8192,
		-10923, 10923, 5461, -5461,
		32767, -32768, 12345, -12345,
	}
	input := []float32{
		0, 0.25, -0.5, 0.9999695,
		-1, 1.25, -1.75, 0.0625,
		float32(math.Nextafter32(0.5, 1)), float32(math.Nextafter32(-0.5, -1)), 0.03125, -0.03125,
	}
	reader, matrixInput := probeLibopusProjectionMatrix(t, projectionMatrixModeOutFloat, rows, cols, frameSize, matrix, input)
	count := reader.Count(rows * frameSize)
	reader.ExpectRemaining(count * 4)
	want := make([]float32, count)
	for i := range want {
		want[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	got := make([]float32, len(input))
	applyProjectionDemixingMatrix32(got, matrixInput, matrix, make([]float32, cols), frameSize, rows, cols)
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("projection float[%d]=%08x want %08x (%0.10g vs %0.10g)",
				i, math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
		}
	}
}

func TestProjectionDemixingInt16MatchesLibopusMatrixOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		rows      = 4
		cols      = 4
		frameSize = 3
	)
	matrix := []int16{
		16384, -8192, 32767, -16384,
		23170, 0, -23170, 8192,
		-10923, 10923, 5461, -5461,
		32767, -32768, 12345, -12345,
	}
	input := []float32{
		0, 0.25, -0.5, 0.9999695,
		-1, 1.25, -1.75, 0.0625,
		float32(math.Nextafter32(0.5, 1)), float32(math.Nextafter32(-0.5, -1)), 0.03125, -0.03125,
	}
	reader, matrixInput := probeLibopusProjectionMatrix(t, projectionMatrixModeOutShort, rows, cols, frameSize, matrix, input)
	count := reader.Count(rows * frameSize)
	reader.ExpectRemaining(count * 2)
	want := make([]int16, count)
	for i := range want {
		want[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	got := make([]int16, len(input))
	applyProjectionDemixingMatrixInt16(got, matrixInput, matrix, make([]float32, cols), frameSize, rows, cols)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("projection int16[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

func TestProjectionShortMixingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := buildMultistreamReferenceHelper(libopustest.CHelperConfig{
		Label:       "projection short mixing",
		OutputBase:  "gopus_libopus_projection_mix_short",
		SourceFile:  "libopus_projection_mix_short_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "src"},
		Libs:        []string{"-lm"},
	})
	if err != nil {
		t.Fatalf("build live C short matrix helper: %v", err)
	}
	for _, tc := range []struct {
		name                string
		channels, frameSize int
		coherent            bool
	}{
		{"foa_5ms", 4, 240, false},
		{"foa_60ms", 4, 2880, false},
		{"soa_20ms", 9, 960, false},
		{"soa_60ms", 9, 2880, false},
		{"soa_coherent_20ms", 9, 960, true},
		{"third_order_coherent_20ms", 16, 960, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewProjectionEncoder(48000, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.projectionRows != tc.channels || enc.projectionCols != tc.channels {
				t.Fatalf("projection dimensions=%d/%d want=%d", enc.projectionRows, enc.projectionCols, tc.channels)
			}
			input := make([]int16, tc.frameSize*tc.channels)
			for i := range input {
				input[i] = int16((i*7919+tc.channels*3137)%65536 - 32768)
			}
			if tc.coherent {
				for sample := range tc.frameSize {
					for col := range tc.channels {
						input[sample*tc.channels+col] = int16(32767 - (sample+col)%31)
						if enc.projectionMixing[col*tc.channels] < 0 {
							input[sample*tc.channels+col] = int16(-32768 + (sample+col)%31)
						}
					}
				}
			}
			payload := libopustest.NewOraclePayloadVersion("GMSI", 1,
				uint32(tc.channels), uint32(tc.channels), uint32(tc.frameSize))
			for _, coeff := range enc.projectionMixing {
				payload.I16(coeff)
			}
			for _, sample := range input {
				payload.I16(sample)
			}
			reader, err := libopustest.RunOracle(bin, payload.Bytes(), "projection short mixing", "GMSO")
			if err != nil {
				t.Fatalf("run live C short matrix helper: %v", err)
			}
			if got := reader.Count(len(input)); got != len(input) {
				t.Fatalf("C output count=%d want=%d", got, len(input))
			}
			buffers := enc.routeProjectionMixingShortToStreams(nil, input, tc.frameSize)
			for sample := range tc.frameSize {
				for row := range tc.channels {
					mappingIdx := enc.mapping[row]
					streamIdx, chanInStream := resolveMapping(mappingIdx, enc.coupledStreams)
					channels := streamChannels(streamIdx, enc.coupledStreams)
					idx := sample*channels + chanInStream
					got := projectionShortOracleBits(enc, streamIdx, idx, buffers[streamIdx][idx])
					want := reader.U32()
					if got != want {
						t.Fatalf("sample=%d outputRow=%d stream=%d/%d bits Go/C=%08x/%08x", sample, row, streamIdx, chanInStream, got, want)
					}
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
			enc.streamInputScratch = buffers
			enc.routeProjectionMixingShortToStreams(enc.streamInputScratch, input, tc.frameSize)
			if allocs := testing.AllocsPerRun(50, func() {
				enc.streamInputScratch = enc.routeProjectionMixingShortToStreams(enc.streamInputScratch, input, tc.frameSize)
			}); allocs != 0 {
				t.Fatalf("warm short matrix allocations=%g want 0", allocs)
			}
		})
	}
}

func hashInt16LE(vals []int16) string {
	buf := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(buf[2*i:2*i+2], uint16(v))
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func TestProjectionMixingDefaultsLibopusParity(t *testing.T) {
	tests := []struct {
		channels int
		streams  int
		coupled  int
		hash     string
	}{
		{channels: 4, streams: 2, coupled: 2, hash: "1143a56642be1320085afae0940651974008a4fa8a69856d0f2ed1dc7b386610"},
		{channels: 6, streams: 3, coupled: 3, hash: "cfa5a4b0fc3229589e8dd9b0b1538ba1e0b2e0ee3376036c2d08ff5be1158840"},
		{channels: 9, streams: 5, coupled: 4, hash: "bddd96d5de3e2da7097f23bfa6938a268aec9142a9961a3086ed3c652d1f6e7a"},
		{channels: 11, streams: 6, coupled: 5, hash: "5a6d53e62b7e04eaf52854688df25ce5d8c7df6933c5af04f174cd576804be30"},
		{channels: 16, streams: 8, coupled: 8, hash: "42b3b8555ebb469b536f0543c63356ef162d6f6c74bf70a8279550618f6529bf"},
		{channels: 18, streams: 9, coupled: 9, hash: "b5d46a15688b23ace9d42238aa5da3fee36d76a4d0b6321c6e26eaf8247dd501"},
		{channels: 25, streams: 13, coupled: 12, hash: "76695ab08fd5c2610670806b76e1c340f6865e3a65a82c2ec4da4a0ba3523c61"},
		{channels: 27, streams: 14, coupled: 13, hash: "ce65b5051c615ed0c7b9906a598de8fa6d0abeb77d5b5c1edbf8cd98add1d7ab"},
		{channels: 36, streams: 18, coupled: 18, hash: "7bf0876f8d7763bf8e45080f150b692dafc40e4a6e41433f2286b0c84287e26d"},
		{channels: 38, streams: 19, coupled: 19, hash: "f5b446b8490ac448097149d42421f46035740f0168bd21fa294c5a2d11f0aa37"},
	}

	for _, tc := range tests {
		matrix, ok := defaultProjectionMixingMatrix(tc.channels, tc.streams, tc.coupled)
		if !ok {
			t.Fatalf("defaultProjectionMixingMatrix(%d,%d,%d) not found", tc.channels, tc.streams, tc.coupled)
		}
		if got, want := len(matrix), tc.channels*tc.channels; got != want {
			t.Fatalf("matrix len = %d, want %d", got, want)
		}
		if got := hashInt16LE(matrix); got != tc.hash {
			t.Fatalf("matrix hash = %s, want %s", got, tc.hash)
		}
	}
}

func TestNewEncoderAmbisonicsFamily3InitializesProjectionMixing(t *testing.T) {
	enc, err := NewEncoderAmbisonics(48000, 9, 3)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics failed: %v", err)
	}

	if got, want := len(enc.projectionMixing), 81; got != want {
		t.Fatalf("projectionMixing len = %d, want %d", got, want)
	}
	if enc.projectionRows != 9 || enc.projectionCols != 9 {
		t.Fatalf("projection dims = %dx%d, want 9x9", enc.projectionRows, enc.projectionCols)
	}
}

func TestApplyProjectionMixingSwapsChannels(t *testing.T) {
	enc := &Encoder{
		projectionMixing: []int16{0, 32767, 32767, 0},
		projectionRows:   2,
		projectionCols:   2,
		inputChannels:    2,
		streams:          1,
		coupledStreams:   1,
		mapping:          []byte{0, 1},
	}

	in := []float32{1, 2, 3, 4}
	streams := enc.routeProjectionMixingToStreams(nil, in, 2)
	out := streams[0]
	scale := float32(32767.0 / 32768.0)
	want := []float32{2 * scale, 1 * scale, 4 * scale, 3 * scale}
	for i := range want {
		if math.Abs(float64(out[i]-want[i])) > 1e-6 {
			t.Fatalf("out[%d] = %f, want %f", i, out[i], want[i])
		}
	}
}

func TestProjectionRectangularDecodeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const rate, frameSize = 48000, 960
	for _, channels := range []int{2, 3} {
		t.Run(fmt.Sprintf("rows%d_cols%d", channels-1, channels), func(t *testing.T) {
			input := generateAmbisonicsSweep(channels, frameSize, 3)
			ref, err := encodeLibopusSurround(rate, channels, 1, 2049, 128000, false, true, 10, -1000, frameSize, 3, 4000, input, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "rectangular projection packets", err)
			}
			rows, cols := channels-1, ref.streams+ref.coupledStreams
			if cols != channels || len(ref.packets) != 3 {
				t.Fatal("unexpected C layout or packet count")
			}
			matrix := make([]byte, rows*cols*2)
			for col := range cols {
				for row := range rows {
					// Include nonzero coefficients in the unmapped last column.
					v := int16(16384/(row+1) - col*4096)
					binary.LittleEndian.PutUint16(matrix[(col*rows+row)*2:], uint16(v))
				}
			}
			packets := [][]byte{ref.packets[0], ref.packets[1], nil, ref.packets[2]}
			got, err := decodeProjectionFormatsWithGain(rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			wantFloat, err := decodeWithLibopusReferencePacketsGain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			assertProjectionFloatSampleExact(t, got.float32, wantFloat, "rectangular projection")
			want16, err := decodeWithLibopusReferencePacketsInt16Gain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			want24, err := decodeWithLibopusReferencePacketsInt24Gain(3, rate, rows, ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(rows), matrix, packets)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.int16) != len(want16) || len(got.int24) != len(want24) {
				t.Fatal("integer output lengths differ")
			}
			for i, want := range want16 {
				if got.int16[i] != want {
					t.Fatalf("int16[%d]=%d C=%d", i, got.int16[i], want)
				}
			}
			for i, want := range want24 {
				if got.int24[i] != want {
					t.Fatalf("int24[%d]=%d C=%d", i, got.int24[i], want)
				}
			}
			dec, err := NewProjectionDecoder(rate, rows, ref.streams, ref.coupledStreams, matrix)
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*rows)
			for range 3 {
				if _, err := dec.DecodeIntoFloat32(ref.packets[0], pcm, frameSize); err != nil {
					t.Fatal(err)
				}
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := dec.DecodeIntoFloat32(ref.packets[0], pcm, frameSize); err != nil {
					panic(err)
				}
			}); allocs != 0 {
				t.Fatalf("warmed decode allocations=%g, want 0", allocs)
			}
		})
	}
}
