//go:build gopus_fixed_point

package gopus_test

import (
	"fmt"
	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"testing"
)

func celtFixedTestSignal(n int, seed int64, transient bool) []float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, n)
	for i := range out {
		t := float64(i)
		v := 0.30*math.Sin(2*math.Pi*440.0*t/48000.0) +
			0.15*math.Sin(2*math.Pi*1200.0*t/48000.0) +
			0.05*(rng.Float64()*2-1)
		if transient && i >= n/2 && i < n/2+80 {
			v += 0.6
		}
		out[i] = float32(v)
	}
	return out
}

// celtFixedFullbandTOC builds the CELT-only fullband TOC byte for a given 48 kHz
// per-channel frame size. Configs 28..31 are CELT fullband at 2.5/5/10/20 ms.
func celtFixedFullbandTOC(frameSize48 int, stereo bool) (byte, bool) {
	var config int
	switch frameSize48 {
	case 120:
		config = 28
	case 240:
		config = 29
	case 480:
		config = 30
	case 960:
		config = 31
	default:
		return 0, false
	}
	toc := byte(config << 3) // code 0 (single frame): low framing bits stay 0
	if stereo {
		toc |= 0x04
	}
	return toc, true
}

// TestDecoderFixedPointCELTParity validates that, under -tags gopus_fixed_point,
// the public Decoder.DecodeInt16 and Decoder.DecodeInt24 of CELT-only packets
// are bit-exact with the matching public libopus decode sequence, including
// the active ENABLE_QEXT feature when that tag is enabled. Both decoders retain
// state across all packets. The public int16/int24 paths route CELT-only frames
// through the integer internal/fixedpoint.CELTDecoder output, so they reproduce
// libopus' RES2INT16 / RES2INT24 conversions exactly:
//
//	int16 sample = SAT16(PSHR32(opus_res, 8))   (RES2INT16)
//	int24 sample = opus_res                       (RES2INT24, ENABLE_RES24)
//
// The libopus reference is the int16 sequence; the int24 output is checked for
// the same RES2INT16 relationship (SAT16(PSHR32(int24, 8)) == int16 reference),
// which pins the int24 path to libopus' int24 semantics.
func TestDecoderFixedPointCELTParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name        string
		channels    int
		frameSize48 int
		bitrate     int
		transient   bool
	}
	cases := []tc{
		{"mono_960_64k", 1, 960, 64000, false},
		{"mono_960_96k", 1, 960, 96000, false},
		{"mono_960_32k", 1, 960, 32000, false},
		{"mono_transient_960_96k", 1, 960, 96000, true},
		{"mono_480_64k", 1, 480, 64000, false},
		{"mono_240_48k", 1, 240, 48000, false},
		{"stereo_960_128k", 2, 960, 128000, false},
		{"stereo_transient_960_128k", 2, 960, 128000, true},
		{"stereo_480_96k", 2, 480, 96000, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			channels := c.channels
			enc := celt.NewEncoder(channels)
			enc.SetBitrate(c.bitrate)

			toc, ok := celtFixedFullbandTOC(c.frameSize48, channels == 2)
			if !ok {
				t.Fatalf("no TOC for frame size %d", c.frameSize48)
			}

			const frames = 6
			var opusPackets [][]byte
			for frame := 0; frame < frames; frame++ {
				transient := c.transient && frame%2 == 1
				mono := celtFixedTestSignal(c.frameSize48, int64(0x5e1d+frame*7)+int64(c.frameSize48), transient)
				pcm := make([]float32, c.frameSize48*channels)
				for i := 0; i < c.frameSize48; i++ {
					pcm[i*channels] = mono[i]
					if channels == 2 {
						pcm[i*channels+1] = mono[i] * 0.8
					}
				}
				payload, err := enc.EncodeFrame(pcm, c.frameSize48)
				if err != nil {
					t.Fatalf("frame %d: encode: %v", frame, err)
				}
				if len(payload) <= 1 {
					continue // degenerate/silence packet uses the PLC path (out of scope)
				}
				// EncodeFrame returns a slice aliasing the encoder's reused range
				// coder buffer, so copy it before the next frame overwrites it.
				payloadCopy := append([]byte(nil), payload...)
				pkt := make([]byte, len(payloadCopy)+1)
				pkt[0] = toc
				copy(pkt[1:], payloadCopy)
				opusPackets = append(opusPackets, pkt)
			}
			if len(opusPackets) == 0 {
				t.Fatalf("no packets produced")
			}

			// Decode the complete packets through the public libopus decoder selected
			// for this build. In particular, fixed+QEXT must use the same QEXT archive
			// as the public Go decoder rather than the fixed-only inner CELT helper.
			refInt16, err := probeFixedPublicDecodeSequence(48000, channels, c.frameSize48, opusPackets, libopustest.DecodeDiffFormatInt16)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed public decode int16", err)
				return
			}
			refInt24, err := probeFixedPublicDecodeSequence(48000, channels, c.frameSize48, opusPackets, libopustest.DecodeDiffFormatInt24)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed public decode int24", err)
				return
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}

			for p, pkt := range opusPackets {
				out := make([]int16, c.frameSize48*channels)
				n, err := dec.DecodeInt16(pkt, out)
				if err != nil {
					t.Fatalf("packet %d: DecodeInt16: %v", p, err)
				}
				if n != c.frameSize48 {
					t.Fatalf("packet %d: decoded %d samples, want %d", p, n, c.frameSize48)
				}
				want := refInt16[p].Int16()
				if got, wantRange := dec.FinalRange(), refInt16[p].FinalRange; got != wantRange {
					t.Fatalf("packet %d final range: gopus=0x%08x libopus=0x%08x", p, got, wantRange)
				}
				for i := 0; i < c.frameSize48*channels; i++ {
					if out[i] != want[i] {
						t.Fatalf("packet %d sample %d: gopus=%d libopus=%d", p, i, out[i], want[i])
					}
				}
			}

			// int24: a fresh decoder (independent cross-frame state) decodes the
			// same packets. Each int24 sample is the opus_res value, so applying
			// RES2INT16 (SAT16(PSHR32(a, 8))) must reproduce the libopus int16
			// reference exactly.
			dec24, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("new int24 decoder: %v", err)
			}
			for p, pkt := range opusPackets {
				out := make([]int32, c.frameSize48*channels)
				n, err := dec24.DecodeInt24(pkt, out)
				if err != nil {
					t.Fatalf("packet %d: DecodeInt24: %v", p, err)
				}
				if n != c.frameSize48 {
					t.Fatalf("packet %d: DecodeInt24 decoded %d samples, want %d", p, n, c.frameSize48)
				}
				want := refInt16[p].Int16()
				want24 := refInt24[p].Int24()
				if got, wantRange := dec24.FinalRange(), refInt24[p].FinalRange; got != wantRange {
					t.Fatalf("packet %d final range: gopus=0x%08x libopus=0x%08x", p, got, wantRange)
				}
				for i := 0; i < c.frameSize48*channels; i++ {
					if out[i] != want24[i] {
						t.Fatalf("packet %d sample %d: int24=%d libopus int24=%d", p, i, out[i], want24[i])
					}
					got16 := res2int16FromInt24(out[i])
					if got16 != want[i] {
						t.Fatalf("packet %d sample %d: RES2INT16(int24)=%d (int24=%d) libopus int16=%d",
							p, i, got16, out[i], want[i])
					}
				}
			}
		})
	}
}

func probeFixedPublicDecodeSequence(sampleRate, channels, frameSize int, packets [][]byte, format uint32) ([]libopustest.DecodeDiffResult, error) {
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{
			Packet: packet, Format: format, FrameSize: uint32(frameSize),
		}
	}
	results, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		return nil, err
	}
	for i, result := range results {
		if result.Code != int32(frameSize) {
			return nil, fmt.Errorf("packet %d: libopus decoded %d samples, want %d", i, result.Code, frameSize)
		}
	}
	return results, nil
}

// res2int16FromInt24 mirrors libopus RES2INT16(a) = SAT16(PSHR32(a, 8)) for the
// ENABLE_RES24 build, applied to an int24 (opus_res) sample. PSHR32 rounds:
// (a + (1<<7)) >> 8.
func res2int16FromInt24(a int32) int16 {
	v := (a + (1 << 7)) >> 8
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

var multistreamFixedRefencodeHelper libopustest.HelperCache

const (
	msEncodeFormatFloat32 = 0
	msEncodeFormatInt16   = 1
)

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
	binPath, err := multistreamFixedRefencodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "multistream fixed reference encode",
			OutputBase:  "gopus_libopus_refencode_multistream_fixed",
			SourceFile:  "libopus_refencode_multistream.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk", "src"},
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
// asserts byte-exact packet equality for the requested sample format.
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
// on the selected scalar or SIMD libopus instruction lane.
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
