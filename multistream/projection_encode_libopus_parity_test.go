package multistream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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

func sameInts(a, b []int) bool {
	if len(a) != len(b) || a == nil || b == nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
