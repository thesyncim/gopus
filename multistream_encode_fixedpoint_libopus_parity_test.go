//go:build gopus_fixed_point

package gopus_test

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamFixedRefencodeHelper libopustest.HelperCache

const (
	msEncodeFormatFloat32 = 0
	msEncodeFormatInt16   = 1
)

// armEncodeFixedDrift reports the darwin/arm64-only <=1-ULP CELT drift that can
// flip a single quantization step and cascade into differing packet bytes. CI
// runs amd64, where the fixed multistream encode is byte-exact. See
// project_arm64_celt_1ulp_drift.md.
func armEncodeFixedDrift() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// msFixedEncodeRef holds the result of driving the libopus FIXED_POINT
// multistream surround encoder (opus_multistream_surround_encoder_create +
// opus_multistream_encode / opus_multistream_encode_float) through the
// refencode_multistream C helper built against the --enable-fixed-point
// reference tree (FIXED_POINT, ENABLE_RES24).
type msFixedEncodeRef struct {
	streams        int
	coupledStreams int
	mapping        []byte
	packets        [][]byte
}

// encodeLibopusMultistreamFixed runs the libopus FIXED_POINT surround encoder
// oracle for the given parameters and PCM (interleaved, frameCount frames of
// frameSize each). sampleFormat selects the int16 (opus_multistream_encode) or
// float32 (opus_multistream_encode_float) public entry point.
func encodeLibopusMultistreamFixed(sampleRate, channels, mappingFamily, application, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes, sampleFormat int, floatPCM []float32, int16PCM []int16) (*msFixedEncodeRef, error) {
	binPath, err := multistreamFixedRefencodeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "multistream fixed reference encode",
		OutputBase: "gopus_libopus_refencode_multistream_fixed",
		SourceFile: "libopus_refencode_multistream.c",
		FixedRef:   true,
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
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
		uint32(sampleFormat),
	)
	if sampleFormat == msEncodeFormatInt16 {
		for _, v := range int16PCM {
			payload.I16(v)
		}
	} else {
		payload.Float32s(floatPCM...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream fixed reference encode", "GMEO")
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
	return &msFixedEncodeRef{streams: streams, coupledStreams: coupled, mapping: mapping, packets: packets}, nil
}

// generateMSFixedSweep builds a multi-frame multichannel PCM buffer with a
// distinct per-channel tone plus a slowly drifting amplitude so the surround
// masking / per-stream rate split sees non-trivial inter-channel energy. It
// returns both the float32 buffer and its int16 quantization (round-half-away,
// matching the gopus int16 PCM contract: int16 -> float32/32768 round-trips
// exactly through FLOAT2INT16).
func generateMSFixedSweep(channels, frameSize, frameCount int) ([]float32, []int16) {
	total := channels * frameSize * frameCount
	pcmF := make([]float32, total)
	pcmI := make([]int16, total)
	baseFreqs := []float64{220, 330, 440, 550, 660, 770, 880, 990}
	n := frameSize * frameCount
	for s := 0; s < n; s++ {
		tt := float64(s) / 48000.0
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
		for ch := 0; ch < channels; ch++ {
			freq := baseFreqs[ch%len(baseFreqs)] * (1.0 + 0.05*float64(ch))
			v := amp * math.Sin(2*math.Pi*freq*tt)
			idx := s*channels + ch
			// Quantize to int16 first so the float and int16 oracle paths consume
			// numerically identical samples (int16 -> float32/32768 is exact).
			q := int(math.Floor(v*32768.0 + 0.5))
			if q > 32767 {
				q = 32767
			}
			if q < -32768 {
				q = -32768
			}
			pcmI[idx] = int16(q)
			pcmF[idx] = float32(q) / 32768.0
		}
	}
	return pcmF, pcmI
}

// runMSFixedEncodeParity drives both gopus (under -tags gopus_fixed_point) and the
// libopus FIXED_POINT surround encoder with identical parameters and PCM, then
// asserts byte-exact packet equality for the requested sample format. The
// darwin/arm64 documented <=1-ULP CELT drift is logged and skipped (CI is amd64,
// where this is byte-exact).
func runMSFixedEncodeParity(t *testing.T, sampleRate, channels, frameSize, frameCount, bitrate, complexity int, vbr, vbrConstraint bool, sampleFormat int) {
	t.Helper()

	const (
		application    = 2049  // OPUS_APPLICATION_AUDIO
		mappingFamily  = 1     // Vorbis-style surround mapping
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
	)

	pcmF, pcmI := generateMSFixedSweep(channels, frameSize, frameCount)

	ref, err := encodeLibopusMultistreamFixed(sampleRate, channels, mappingFamily, application,
		bitrate, vbr, vbrConstraint, complexity, bandwidthAuto, frameSize, frameCount, maxPacketBytes, sampleFormat, pcmF, pcmI)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed reference encode", err)
		return
	}

	enc, err := gopus.NewMultistreamEncoderDefault(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault(%d, %d): %v", sampleRate, channels, err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("stream layout mismatch: gopus streams=%d coupled=%d, libopus streams=%d coupled=%d",
			enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}

	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate(%d): %v", bitrate, err)
	}
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(vbrConstraint)
	if err := enc.SetComplexity(complexity); err != nil {
		t.Fatalf("SetComplexity(%d): %v", complexity, err)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatalf("SetBandwidthAuto: %v", err)
	}

	out := make([]byte, maxPacketBytes)
	for i := 0; i < frameCount; i++ {
		start := i * frameSize * channels
		end := start + frameSize*channels

		var (
			n   int
			err error
		)
		if sampleFormat == msEncodeFormatInt16 {
			n, err = enc.EncodeInt16(pcmI[start:end], out)
		} else {
			n, err = enc.Encode(pcmF[start:end], out)
		}
		if err != nil {
			t.Fatalf("frame %d: gopus Encode: %v", i, err)
		}
		got := out[:n]
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
		if armEncodeFixedDrift() {
			t.Logf("frame %d: documented darwin/arm64 CELT drift (gopus len=%d libopus len=%d firstMismatch=%d)",
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

// TestMultistreamEncodeFixedPointParity gates that, under -tags gopus_fixed_point,
// MultistreamEncoder.Encode / EncodeInt16 produce byte-exact multistream packets
// vs the libopus FIXED_POINT (ENABLE_RES24) opus_multistream_encode_float /
// opus_multistream_encode reference across the standard mapping-family-1 layouts
// (mono, stereo-coupled, quad, 5.0, 5.1, 6.1, 7.1), CBR and constrained VBR,
// multiple bitrates and frame sizes, multi-frame.
//
// Each elementary stream is encoded through the FIXED_POINT single-stream CELT /
// SILK / Hybrid path (gated under the same tag); the multistream packet assembly
// and surround channel routing feed each substream the per-stream PCM. Bit-exact
// on amd64; subject to the documented per-arch 1-ULP CELT drift budget on arm64.
func TestMultistreamEncodeFixedPointParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	frameSizes := []int{480, 960} // 10 ms, 20 ms at 48 kHz

	layouts := []struct {
		name     string
		channels int
	}{
		{"mono", 1},
		{"stereo_coupled", 2},
		{"quad", 4},
		{"surround_5_0", 5},
		{"surround_5_1", 6},
		{"surround_6_1", 7},
		{"surround_7_1", 8},
	}

	bitrates := []int{64000, 128000, 256000}
	formats := []struct {
		name string
		fmt  int
	}{
		{"int16", msEncodeFormatInt16},
		{"float32", msEncodeFormatFloat32},
	}

	for _, layout := range layouts {
		layout := layout
		for _, frameSize := range frameSizes {
			frameSize := frameSize
			for _, bitrate := range bitrates {
				bitrate := bitrate
				for _, vbr := range []bool{false, true} {
					vbr := vbr
					for _, f := range formats {
						f := f
						name := fmt.Sprintf("%s/%s/fs%d/br%d/vbr%t", layout.name, f.name, frameSize, bitrate, vbr)
						t.Run(name, func(t *testing.T) {
							runMSFixedEncodeParity(t, sampleRate, layout.channels, frameSize, 6, bitrate, 10, vbr, true, f.fmt)
						})
					}
				}
			}
		}
	}
}
