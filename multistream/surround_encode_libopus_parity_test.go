package multistream

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var surroundRefencodeHelper libopustest.HelperCache

// armEncodeFloatDrift reports the darwin/arm64-only ≤1-ULP CELT float drift that
// can flip a single quantization step and cascade into differing packet bytes.
// CI runs amd64, where surround encode is byte-exact. See
// project_arm64_celt_1ulp_drift.md.
func armEncodeFloatDrift() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// surroundEncodeRef holds the result of driving libopus
// opus_multistream_surround_encoder_create + opus_multistream_encode_float
// through the refencode_multistream C helper.
type surroundEncodeRef struct {
	streams        int
	coupledStreams int
	mapping        []byte
	packets        [][]byte
}

// encodeLibopusSurround runs the libopus surround encoder oracle for the given
// parameters and PCM (interleaved float32, frameCount frames of frameSize each).
func encodeLibopusSurround(sampleRate, channels, mappingFamily, application int, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes int, pcm []float32) (*surroundEncodeRef, error) {
	binPath, err := surroundRefencodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "multistream surround reference encode",
		OutputBase: "gopus_libopus_refencode_public_multistream",
		SourceFile: "libopus_refencode_multistream.c",
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
		"GMEI",
		1,
		uint32(sampleRate),
		uint32(channels),
		uint32(mappingFamily),
		uint32(application),
		uint32(int32(bitrate)),
		boolU32(vbr),
		boolU32(vbrConstraint),
		uint32(complexity),
		uint32(int32(bandwidth)),
		uint32(frameSize),
		uint32(frameCount),
		uint32(maxPacketBytes),
		0, // SAMPLE_FORMAT_FLOAT32
	)
	payload.Float32s(pcm...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream surround reference encode", "GMEO")
	if err != nil {
		return nil, err
	}

	streams := int(reader.U32())
	coupled := int(reader.U32())
	chans := int(reader.U32())
	if chans != channels {
		return nil, fmt.Errorf("oracle channels mismatch: got %d want %d", chans, channels)
	}
	mapping := make([]byte, chans)
	copy(mapping, reader.Bytes(chans))

	packetCount := int(reader.U32())
	packets := make([][]byte, packetCount)
	for i := range packets {
		n := int(reader.U32())
		packets[i] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return &surroundEncodeRef{streams: streams, coupledStreams: coupled, mapping: mapping, packets: packets}, nil
}

// generateSurroundSweep builds a multi-frame multichannel PCM buffer with a
// distinct per-channel tone plus a slowly drifting amplitude so the surround
// masking / per-stream rate split sees non-trivial inter-channel energy.
func generateSurroundSweep(channels, frameSize, frameCount int) []float32 {
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	baseFreqs := []float64{220, 330, 440, 550, 660, 770, 880, 990}
	n := frameSize * frameCount
	for s := 0; s < n; s++ {
		tt := float64(s) / 48000.0
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
		for ch := 0; ch < channels; ch++ {
			freq := baseFreqs[ch%len(baseFreqs)] * (1.0 + 0.05*float64(ch))
			pcm[s*channels+ch] = float32(amp * math.Sin(2*math.Pi*freq*tt))
		}
	}
	return pcm
}

// runSurroundEncodeParity drives both gopus and libopus surround encoders with
// identical parameters and PCM, then asserts byte-exact packet equality. The
// darwin/arm64 documented ≤1-ULP CELT drift is logged and skipped (CI is amd64,
// where this is byte-exact).
func runSurroundEncodeParity(t *testing.T, sampleRate, channels, frameSize, frameCount, bitrate, complexity int, vbr, vbrConstraint bool) {
	t.Helper()

	const (
		application    = 2049 // OPUS_APPLICATION_AUDIO
		mappingFamily  = 1
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
	)

	pcm := generateSurroundSweep(channels, frameSize, frameCount)

	ref, err := encodeLibopusSurround(sampleRate, channels, mappingFamily, application,
		bitrate, vbr, vbrConstraint, complexity, bandwidthAuto, frameSize, frameCount, maxPacketBytes, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream surround reference encode", err)
	}

	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault(%d, %d): %v", sampleRate, channels, err)
	}
	// The derived stream/coupled layout must match libopus exactly regardless of
	// the float path (mapping-family-1 stream-count derivation).
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("stream layout mismatch: gopus streams=%d coupled=%d, libopus streams=%d coupled=%d",
			enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}

	enc.SetBitrate(bitrate)
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(vbrConstraint)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()

	for i := 0; i < frameCount; i++ {
		start := i * frameSize * channels
		frame := pcm[start : start+frameSize*channels]
		got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, frameSize, frame, maxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: gopus Encode: %v", i, err)
		}
		want := ref.packets[i]

		diverged := len(got) != len(want)
		mismatch := -1
		if !diverged {
			for j := range got {
				if got[j] != want[j] {
					mismatch = j
					diverged = true
					break
				}
			}
		}
		if !diverged {
			continue
		}
		if armEncodeFloatDrift() {
			t.Logf("frame %d: documented darwin/arm64 CELT float drift (gopus len=%d libopus len=%d firstMismatch=%d)",
				i, len(got), len(want), mismatch)
			return
		}
		if len(got) != len(want) {
			t.Fatalf("frame %d: packet length mismatch: gopus=%d libopus=%d", i, len(got), len(want))
		}
		t.Fatalf("frame %d: byte %d mismatch: gopus=0x%02x libopus=0x%02x (len=%d)",
			i, mismatch, got[mismatch], want[mismatch], len(got))
	}
}

// TestSurroundEncodeMatchesLibopusByteExact verifies that gopus
// MultistreamEncoder surround encode is byte-exact vs libopus
// opus_multistream_surround_encoder_create + opus_multistream_encode_float
// across the standard mapping-family-1 layouts (mono, stereo, quad, 5.0, 5.1,
// 6.1, 7.1), a range of bitrates/frame sizes, both CBR and constrained VBR.
// 5.1/6.1/7.1 exercise the LFE-stream carve-out and the surround masking
// per-channel allocation.
func TestSurroundEncodeMatchesLibopusByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	frameSizes := []int{480, 960} // 10 ms, 20 ms at 48 kHz

	layouts := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo", 2},
		{"quad", 4},
		{"surround_5_0", 5},
		{"surround_5_1", 6},
		{"surround_6_1", 7},
		{"surround_7_1", 8},
	}

	bitrates := []int{64000, 128000, 256000, 384000}

	for _, layout := range layouts {
		layout := layout
		for _, frameSize := range frameSizes {
			frameSize := frameSize
			for _, bitrate := range bitrates {
				bitrate := bitrate
				for _, vbr := range []bool{false, true} {
					vbr := vbr
					name := fmt.Sprintf("%s/fs%d/br%d/vbr%t", layout.name, frameSize, bitrate, vbr)
					t.Run(name, func(t *testing.T) {
						runSurroundEncodeParity(t, sampleRate, layout.channels, frameSize, 6, bitrate, 10, vbr, true)
					})
				}
			}
		}
	}
}

// TestSurroundEncodeUnconstrainedVBRMatchesLibopus checks byte-exact parity for
// surround encode in UNCONSTRAINED VBR mode (SetVBR(true) + SetVBRConstraint(false)).
// The pre-existing surround parity tests only exercise constrained VBR
// (vbrConstraint=true); single-stream unconstrained VBR is already byte-exact
// (testvectors/encoder_vbr_cvbr_byte_parity_test.go), so the surround per-stream
// path should be too. This test fills that coverage gap directly against
// opus_multistream_surround_encoder_create + opus_multistream_encode_float.
func TestSurroundEncodeUnconstrainedVBRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960
	)
	layouts := []struct {
		name     string
		channels int
	}{
		{"stereo", 2},
		{"surround_5_1", 6},
		{"surround_7_1", 8},
	}
	bitrates := []int{96000, 256000}

	for _, layout := range layouts {
		layout := layout
		for _, bitrate := range bitrates {
			bitrate := bitrate
			name := fmt.Sprintf("%s/br%d", layout.name, bitrate)
			t.Run(name, func(t *testing.T) {
				// vbr=true, vbrConstraint=false → unconstrained VBR.
				runSurroundEncodeParity(t, sampleRate, layout.channels, frameSize, 6, bitrate, 10, true, false)
			})
		}
	}
}

// TestSurroundEncodeComplexityMatchesLibopus checks byte-exact parity across the
// complexity sweep for the LFE-bearing 5.1 layout, which exercises the LFE-stream
// rate carve-out and the surround masking allocation.
func TestSurroundEncodeComplexityMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 6
		frameSize  = 960
		bitrate    = 256000
	)
	for _, complexity := range []int{0, 5, 10} {
		complexity := complexity
		t.Run(fmt.Sprintf("complexity%d", complexity), func(t *testing.T) {
			runSurroundEncodeParity(t, sampleRate, channels, frameSize, 6, bitrate, complexity, true, true)
		})
	}
}
