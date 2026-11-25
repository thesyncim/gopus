package multistream

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

var surroundRefencodeHelper libopustest.HelperCache

// surroundEncodeRef holds the result of driving libopus
// opus_multistream_surround_encoder_create and the matching float or short
// encode entry point through the refencode_multistream C helper.
type surroundEncodeRef struct {
	streams        int
	coupledStreams int
	mapping        []byte
	packets        [][]byte
	ranges         []uint32
}

// encodeLibopusSurround runs the libopus surround encoder oracle for the given
// parameters and PCM (interleaved float32, frameCount frames of frameSize each).
func encodeLibopusSurround(sampleRate, channels, mappingFamily, application int, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes int, pcm []float32, dtx bool) (*surroundEncodeRef, error) {
	return encodeLibopusSurroundFormat(sampleRate, channels, mappingFamily, application,
		bitrate, vbr, vbrConstraint, complexity, bandwidth, frameSize, frameCount,
		maxPacketBytes, 0, pcm, nil, dtx)
}

// encodeLibopusSurroundInt16 calls opus_multistream_encode with the exact short
// input passed to the Go public EncodeInt16 entry point.
func encodeLibopusSurroundInt16(sampleRate, channels, mappingFamily, application int, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes int, pcm []int16, dtx bool) (*surroundEncodeRef, error) {
	return encodeLibopusSurroundFormat(sampleRate, channels, mappingFamily, application,
		bitrate, vbr, vbrConstraint, complexity, bandwidth, frameSize, frameCount,
		maxPacketBytes, 1, nil, pcm, dtx)
}

func encodeLibopusSurroundFormat(sampleRate, channels, mappingFamily, application int, bitrate int, vbr, vbrConstraint bool, complexity, bandwidth, frameSize, frameCount, maxPacketBytes, sampleFormat int, pcm32 []float32, pcm16 []int16, dtx bool) (*surroundEncodeRef, error) {
	wantSamples := frameCount * frameSize * channels
	if frameCount <= 0 || frameSize <= 0 || channels <= 0 ||
		(sampleFormat == 0 && (len(pcm32) != wantSamples || len(pcm16) != 0)) ||
		(sampleFormat == 1 && (len(pcm16) != wantSamples || len(pcm32) != 0)) ||
		(sampleFormat != 0 && sampleFormat != 1) {
		return nil, fmt.Errorf("surround oracle input format=%d float samples=%d short samples=%d want %d",
			sampleFormat, len(pcm32), len(pcm16), wantSamples)
	}
	binPath, err := surroundRefencodeHelper.Path(func() (string, error) {
		return buildMultistreamReferenceHelper(libopustest.CHelperConfig{
			Label:       "multistream surround reference encode",
			OutputBase:  "gopus_libopus_refencode_public_multistream",
			SourceFile:  "libopus_refencode_multistream.c",
			CFlags:      []string{"-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "src"},
			Libs:        []string{"-lm"},
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
		3,
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
		boolU32(dtx),
	)
	if sampleFormat == 1 {
		for _, sample := range pcm16 {
			payload.I16(sample)
		}
	} else {
		payload.Float32s(pcm32...)
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "multistream surround reference encode", "GMEO", 3)
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
	return &surroundEncodeRef{streams: streams, coupledStreams: coupled, mapping: mapping, packets: packets, ranges: ranges}, nil
}

// generateSurroundSweep builds a multi-frame multichannel PCM buffer with a
// distinct per-channel tone plus a slowly drifting amplitude so the surround
// masking / per-stream rate split sees non-trivial inter-channel energy.
func generateSurroundSweep(channels, frameSize, frameCount int) []float32 {
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	baseFreqs := []float64{220, 330, 440, 550, 660, 770, 880, 990}
	n := frameSize * frameCount
	for s := range n {
		tt := float64(s) / 48000.0
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tt)
		for ch := range channels {
			freq := baseFreqs[ch%len(baseFreqs)] * (1.0 + 0.05*float64(ch))
			pcm[s*channels+ch] = float32(amp * math.Sin(2*math.Pi*freq*tt))
		}
	}
	return pcm
}

// runSurroundEncodeParity drives both encoders with identical PCM and controls,
// then checks every packet byte and final range.
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
		bitrate, vbr, vbrConstraint, complexity, bandwidthAuto, frameSize, frameCount, maxPacketBytes, pcm, false)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
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

	for i := range frameCount {
		start := i * frameSize * channels
		frame := pcm[start : start+frameSize*channels]
		got, err := encodePacketMax(enc, frame, frameSize, frame, maxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: gopus Encode: %v", i, err)
		}
		if !bytes.Equal(got, ref.packets[i]) || enc.GetFinalRange() != ref.ranges[i] {
			mismatch := firstByteMismatch(got, ref.packets[i])
			var byteValues string
			if mismatch >= 0 && mismatch < len(got) && mismatch < len(ref.packets[i]) {
				byteValues = fmt.Sprintf(" byte Go/C=%02x/%02x", got[mismatch], ref.packets[i][mismatch])
			}
			t.Errorf("frame %d: firstByte=%d%s len Go/C=%d/%d range Go/C=%08x/%08x", i,
				mismatch, byteValues, len(got), len(ref.packets[i]),
				enc.GetFinalRange(), ref.ranges[i])
		}
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
		for _, frameSize := range frameSizes {
			for _, bitrate := range bitrates {
				for _, vbr := range []bool{false, true} {
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
		for _, bitrate := range bitrates {
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
		t.Run(fmt.Sprintf("complexity%d", complexity), func(t *testing.T) {
			runSurroundEncodeParity(t, sampleRate, channels, frameSize, 6, bitrate, complexity, true, true)
		})
	}
}

func TestSurroundInt16PacketRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameCount = 6
	for _, tc := range []struct {
		name          string
		channels      int
		mappingFamily int
		frameSize     int
		bitrate       int
		vbr           bool
	}{
		{name: "stereo_coupled_20ms_cvbr", channels: 2, mappingFamily: 1, frameSize: 960, bitrate: 64000, vbr: true},
		{name: "stereo_discrete_20ms_cbr", channels: 2, mappingFamily: 255, frameSize: 960, bitrate: 64000},
		{name: "quad_20ms_cvbr", channels: 4, mappingFamily: 1, frameSize: 960, bitrate: 128000, vbr: true},
		{name: "surround_5_1_10ms_cbr", channels: 6, mappingFamily: 1, frameSize: 480, bitrate: 256000},
		{name: "surround_7_1_20ms_cbr", channels: 8, mappingFamily: 1, frameSize: 960, bitrate: 384000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm := floatToInt16(generateSurroundSweep(tc.channels, tc.frameSize, frameCount))
			ref, err := encodeLibopusSurroundInt16(48000, tc.channels, tc.mappingFamily, 2049,
				tc.bitrate, tc.vbr, true, 10, -1000, tc.frameSize, frameCount,
				4000, pcm, false)
			if err != nil {
				t.Fatalf("live C short surround encode: %v", err)
			}
			if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
				t.Fatalf("oracle record count packets/ranges=%d/%d want %d", len(ref.packets), len(ref.ranges), frameCount)
			}
			var enc *Encoder
			if tc.mappingFamily == 255 {
				enc, err = NewEncoder(48000, tc.channels, 2, 0, []byte{0, 1})
			} else {
				enc, err = NewEncoderDefault(48000, tc.channels)
			}
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			if enc.MappingFamily() != tc.mappingFamily {
				t.Fatalf("mapping family Go=%d want %d", enc.MappingFamily(), tc.mappingFamily)
			}
			if !bytes.Equal(enc.mapping, ref.mapping) {
				t.Fatalf("channel mapping Go=%v C=%v", enc.mapping, ref.mapping)
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
				n, err := enc.EncodeInt16(input, tc.frameSize, out)
				if err != nil {
					t.Fatalf("frame %d Go short encode: %v", frame, err)
				}
				if n <= 0 || !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Fatalf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x",
						frame, firstByteMismatch(out[:n], ref.packets[frame]), n, len(ref.packets[frame]),
						enc.GetFinalRange(), ref.ranges[frame])
				}
			}
		})
	}
}

func TestSurroundLowSpaceFinalRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	var spec surroundFuzzSpec
	for _, candidate := range buildSurroundFuzzSweep() {
		if candidate.name == "surround_7_1/fs480/br64000/vbrfalse/cfalse/cx0" {
			spec = candidate
			break
		}
	}
	if spec.frameCount != 5 {
		t.Fatalf("missing 7.1 low-space witness: %+v", spec)
	}
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(compositeSampleRate, spec.channels, 1, compositeApplication,
		spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
		spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm, false)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
	}
	enc, err := NewEncoderDefault(compositeSampleRate, spec.channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(spec.bitrate)
	enc.SetVBR(spec.vbr)
	enc.SetVBRConstraint(spec.vbrConstraint)
	enc.SetComplexity(spec.complexity)
	enc.SetBandwidthAuto()
	var sawBare, sawPadded bool
	for frame := range spec.frameCount {
		start := frame * spec.frameSize * spec.channels
		input := pcm[start : start+spec.frameSize*spec.channels]
		got, err := encodePacketMax(enc, input, spec.frameSize, input, compositeMaxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame])
		}
		streams, err := parseMultistreamPacket(ref.packets[frame], ref.streams)
		if err != nil {
			t.Fatalf("frame %d: parse reference: %v", frame, err)
		}
		child := streams[4]
		parsed, err := parseOpusPacket(child, false)
		if err != nil {
			t.Fatalf("frame %d: parse trailing reference: %v", frame, err)
		}
		empty := true
		for _, payload := range parsed.frames {
			if len(payload) != 0 {
				empty = false
			}
		}
		if empty {
			if enc.encoders[4].FinalRange() != 0 {
				t.Fatalf("frame %d empty trailing child range=%08x want 0", frame, enc.encoders[4].FinalRange())
			}
			if len(child) == 1 {
				sawBare = true
			} else if len(child) == 2 {
				sawPadded = true
			}
		} else if enc.encoders[4].FinalRange() == 0 {
			t.Fatalf("frame %d coded trailing child has zero final range", frame)
		}
	}
	if !sawBare || !sawPadded {
		t.Fatalf("selected C trailing stream lacks bare/padded empty packet pair (bare=%t padded=%t)", sawBare, sawPadded)
	}
}

func TestSurroundLowSpaceThenRealFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels   = 8
		frameSize  = 480
		frameCount = 6
		bitrate    = 64000
		complexity = 0
	)
	pcm := generateSurroundSweep(channels, frameSize, frameCount)
	ref, err := encodeLibopusSurround(compositeSampleRate, channels, 1, compositeApplication,
		bitrate, false, false, complexity, compositeBandwidthAuto,
		frameSize, frameCount, compositeMaxPacketBytes, pcm, false)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
	}
	if ref.streams != 5 {
		t.Fatalf("reference streams=%d want 5", ref.streams)
	}
	firstStreams, err := parseMultistreamPacket(ref.packets[0], ref.streams)
	if err != nil {
		t.Fatalf("parse first reference packet: %v", err)
	}
	firstLFE := firstStreams[len(firstStreams)-1]
	parsed, err := parseOpusPacket(firstLFE, false)
	if err != nil {
		t.Fatalf("parse first LFE packet: %v", err)
	}
	if len(parsed.frames) != 1 || len(parsed.frames[0]) != 0 {
		t.Fatalf("first LFE packet=%x want one empty frame", firstLFE)
	}
	secondStreams, err := parseMultistreamPacket(ref.packets[1], ref.streams)
	if err != nil {
		t.Fatalf("parse second reference packet: %v", err)
	}
	secondLFE, err := parseOpusPacket(secondStreams[len(secondStreams)-1], false)
	if err != nil {
		t.Fatalf("parse second LFE packet: %v", err)
	}
	if len(secondLFE.frames) == 0 || len(secondLFE.frames[0]) == 0 {
		t.Fatalf("second LFE packet=%x want coded recovery frame", secondStreams[len(secondStreams)-1])
	}
	enc, err := NewEncoderDefault(compositeSampleRate, channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)
	enc.SetVBRConstraint(false)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()
	out := make([]byte, compositeMaxPacketBytes)
	for frame := range frameCount {
		start := frame * frameSize * channels
		input := pcm[start : start+frameSize*channels]
		n, err := enc.Encode(input, frameSize, out)
		if err != nil {
			t.Fatalf("frame %d Go encode: %v", frame, err)
		}
		got := out[:n]
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Fatalf("frame %d firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x",
				frame, firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame])
		}
	}
}
