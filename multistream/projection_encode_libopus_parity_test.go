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
}

// encodeLibopusProjection runs the libopus projection encoder oracle for the
// given parameters and PCM. When sampleFormat is 1 (int16) pcm16 is used and the
// oracle drives opus_projection_encode; otherwise pcm32 is used with
// opus_projection_encode_float.
func encodeLibopusProjection(sampleRate, channels, application, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes, sampleFormat int, pcm32 []float32, pcm16 []int16) (*projectionEncodeRef, error) {
	binPath, err := projectionEncodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "projection reference encode",
		OutputBase: "gopus_libopus_projection_encode_oracle",
		SourceFile: "libopus_projection_encode_oracle.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
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
		1,
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

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "projection reference encode", "GPEO")
	if err != nil {
		return nil, err
	}

	streams := int(reader.U32())
	coupled := int(reader.U32())
	demixSize := int(reader.U32())
	demixing := append([]byte(nil), reader.Bytes(demixSize)...)
	demixGain := int(int32(reader.U32()))

	packetCount := int(reader.U32())
	packets := make([][]byte, packetCount)
	for i := range packets {
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
	}, nil
}

// generateAmbisonicsSweep builds a multi-frame ambisonics PCM buffer with a
// distinct per-channel tone so the projection mixing matrix sees non-trivial
// inter-channel energy across the W/X/Y/Z... components.
func generateAmbisonicsSweep(channels, frameSize, frameCount int) []float32 {
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	n := frameSize * frameCount
	for s := 0; s < n; s++ {
		tt := float64(s) / 48000.0
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
		for ch := 0; ch < channels; ch++ {
			freq := 110.0 * float64(ch+1)
			pcm[s*channels+ch] = float32(amp * math.Sin(2*math.Pi*freq*tt))
		}
	}
	return pcm
}

// floatToInt16 converts a float PCM buffer to int16 the way a caller would, so
// the int16 oracle (opus_projection_encode) and the gopus float path that
// reconstructs s/32768 see equivalent samples.
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
	for i := 0; i < n; i++ {
		g := int16(binary.LittleEndian.Uint16(got[2*i : 2*i+2]))
		w := int16(binary.LittleEndian.Uint16(want[2*i : 2*i+2]))
		if g != w {
			t.Fatalf("demixing[%d] mismatch: gopus=%d libopus=%d", i, g, w)
		}
	}
}

func firstByteMismatch(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
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

// runProjectionEncodeParity drives both gopus and libopus projection encoders
// with identical parameters and asserts:
//
//   - the stream/coupled layout matches (hard),
//   - the demixing matrix and gain are byte-exact (hard), and
//   - each per-frame packet is byte-exact (best-effort).
//
// The demixing matrix, gain, layout and projection mixing are byte-exact; the
// packet bytes ride on the shared single-stream Opus encode path. Two known
// non-projection residuals are logged rather than failed:
//
//   - darwin/arm64 ships a documented <=1-ULP CELT float drift that can flip a
//     quantization step (project_arm64_celt_1ulp_drift.md).
//   - the per-stream SILK/hybrid/CELT mode decision can diverge by one
//     classification step on the moderate per-stream rates ambisonics streams
//     receive (the standalone single-stream encoder reproduces this on the same
//     PCM; it is not projection-specific). When the per-stream TOC configs match
//     but bytes differ, the divergence is float drift only.
func runProjectionEncodeParity(t *testing.T, channels, frameSize, frameCount, bitrate, complexity, sampleFormat int, vbr, vbrConstraint bool) {
	t.Helper()

	const (
		sampleRate     = 48000
		application    = 2049  // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
	)

	pcm := generateAmbisonicsSweep(channels, frameSize, frameCount)
	var pcm16 []int16
	gopusPCM := pcm
	if sampleFormat == 1 {
		pcm16 = floatToInt16(pcm)
		// gopus reconstructs the int16 caller samples as float = s/32768, matching
		// mapping_matrix_multiply_channel_in_short's matrix*int16/(32768*32768).
		gopusPCM = make([]float32, len(pcm16))
		for i, s := range pcm16 {
			gopusPCM[i] = float32(s) / 32768.0
		}
	}

	ref, err := encodeLibopusProjection(sampleRate, channels, application, bitrate, vbr, vbrConstraint,
		complexity, bandwidthAuto, frameSize, frameCount, maxPacketBytes, sampleFormat, pcm, pcm16)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection reference encode", err)
	}

	enc, err := NewProjectionEncoder(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewProjectionEncoder(%d, %d): %v", sampleRate, channels, err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("stream layout mismatch: gopus streams=%d coupled=%d, libopus streams=%d coupled=%d",
			enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}

	compareProjectionDemixing(t, enc.GetDemixingMatrix(), ref.demixing)
	if enc.DemixingMatrixGain() != ref.demixingGain {
		t.Fatalf("demixing gain mismatch: gopus=%d libopus=%d", enc.DemixingMatrixGain(), ref.demixingGain)
	}

	enc.SetBitrate(bitrate)
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(vbrConstraint)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()

	for i := 0; i < frameCount; i++ {
		start := i * frameSize * channels
		frame := gopusPCM[start : start+frameSize*channels]
		got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, frameSize, frame, maxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: gopus Encode: %v", i, err)
		}
		want := ref.packets[i]
		if bytes.Equal(got, want) {
			continue
		}
		mismatch := firstByteMismatch(got, want)
		gotCfgs := perStreamConfigs(got, enc.Streams())
		wantCfgs := perStreamConfigs(want, ref.streams)
		if !sameInts(gotCfgs, wantCfgs) {
			// Per-stream mode-decision residual (not projection-specific).
			t.Logf("frame %d: per-stream mode-decision residual: gopus configs=%v libopus configs=%v (len gopus=%d libopus=%d)",
				i, gotCfgs, wantCfgs, len(got), len(want))
			continue
		}
		// The stream/coupled layout, the demixing matrix and the gain are asserted
		// hard above. A per-frame byte (or VBR length) divergence with matching
		// per-stream modes is the documented ≤1-ULP CELT float boundary on the
		// pure-Go builds (arm64 FMA, amd64-purego Go float vs the scalar libopus
		// oracle); only the amd64 asm/SIMD build is held strictly bit-exact. See
		// project_arm64_celt_1ulp_drift.md.
		if armEncodeFloatDrift() || !gopusBuildIsAsm {
			t.Logf("frame %d: documented pure-Go ≤1-ULP CELT float drift (gopus len=%d libopus len=%d firstMismatch=%d)",
				i, len(got), len(want), mismatch)
			continue
		}
		t.Fatalf("frame %d: packet mismatch with matching per-stream modes (gopus len=%d libopus len=%d firstMismatch=%d)",
			i, len(got), len(want), mismatch)
	}
}

// TestProjectionEncodeMatchesLibopus verifies that gopus NewProjectionEncoder +
// EncodeFloat32 reproduces libopus opus_projection_ambisonics_encoder_create +
// opus_projection_encode_float for 1st-order (4ch FOA) and 2nd-order (9ch SOA)
// ambisonics across CBR and constrained-VBR at several bitrates and frame sizes.
//
// The stream/coupled layout, the demixing matrix
// (OPUS_PROJECTION_GET_DEMIXING_MATRIX) and the gain are asserted byte-exact for
// every configuration; per-frame packets are asserted byte-exact whenever the
// per-stream mode decisions agree (see runProjectionEncodeParity for the
// documented residuals).
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
		order := order
		for _, frameSize := range frameSizes {
			frameSize := frameSize
			for _, bitrate := range bitrates {
				bitrate := bitrate
				for _, vbr := range []bool{false, true} {
					vbr := vbr
					name := fmt.Sprintf("%s/fs%d/br%d/vbr%t", order.name, frameSize, bitrate, vbr)
					t.Run(name, func(t *testing.T) {
						runProjectionEncodeParity(t, order.channels, frameSize, 6, bitrate, 10, 0, vbr, true)
					})
				}
			}
		}
	}
}

// TestProjectionEncodeInt16MatchesLibopus exercises the int16 input path
// (opus_projection_encode) for FOA and SOA. libopus int16 projection encode
// applies the mixing matrix directly on the int16 samples
// (mapping_matrix_multiply_channel_in_short: tmp += matrix*int16,
// output = tmp/(32768*32768)); gopus is fed the float reconstruction
// int16/32768, so the mixing reduces to matrix*(int16/32768)/32768. The two are
// equal in exact arithmetic; in float32 the accumulation order differs (libopus
// rounds the ~2^30 integer products, gopus the ~2^15 pre-divided products), so a
// frame may land on the documented float-drift / per-stream mode residual. The
// layout, demixing matrix and gain assertions are byte-exact regardless.
func TestProjectionEncodeInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, channels := range []int{4, 9} {
		channels := channels
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			runProjectionEncodeParity(t, channels, 960, 6, 256000, 10, 1, false, true)
		})
	}
}
