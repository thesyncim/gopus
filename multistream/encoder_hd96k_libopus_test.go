//go:build gopus_qext

package multistream

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var multistreamHD96kEncodeHelper libopustest.HelperCache

type multistreamHD96kEncodedFrame struct {
	packet  []byte
	samples int
	range32 uint32
}

type multistreamHD96kLayout struct {
	name     string
	channels int
	streams  int
	coupled  int
	mapping  []byte
}

func buildMultistreamHD96kEncodeHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "96 kHz multistream encoder",
		OutputBase:  "gopus_libopus_multistream_hd96k_encode",
		SourceFile:  "libopus_multistream_hd96k_encode.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func TestMultistreamNativeHD96kEncodeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameCount = 5
		packetCap  = 3825
		bitrate    = 256000
		complexity = 10
		shortFrame = 1920
	)
	mono := multistreamHD96kLayout{name: "mono", channels: 1, streams: 1, mapping: []byte{0}}
	for _, frameSize := range []int{240, 480, 960, 1920} {
		for _, qext := range []bool{false, true} {
			t.Run(frameSizeName(frameSize)+"/mono_float/qext_"+boolName(qext), func(t *testing.T) {
				runMultistreamHD96kEncodeCase(t, mono, frameSize, qext, false, frameCount,
					packetCap, bitrate, complexity)
			})
		}
	}

	layouts := []multistreamHD96kLayout{
		{name: "stereo_coupled", channels: 2, streams: 1, coupled: 1, mapping: []byte{0, 1}},
		{name: "stereo_discrete", channels: 2, streams: 2, mapping: []byte{0, 1}},
	}
	for _, layout := range layouts {
		for _, int16Input := range []bool{false, true} {
			format := "float"
			if int16Input {
				format = "int16"
			}
			for _, qext := range []bool{false, true} {
				t.Run(layout.name+"/"+format+"/20ms/qext_"+boolName(qext), func(t *testing.T) {
					runMultistreamHD96kEncodeCase(t, layout, shortFrame, qext, int16Input,
						frameCount, packetCap, bitrate, complexity)
				})
			}
		}
	}
}

func runMultistreamHD96kEncodeCase(t *testing.T, layout multistreamHD96kLayout,
	frameSize int, qext, int16Input bool, frameCount, packetCap, bitrate, complexity int,
) {
	t.Helper()
	f32Frames := makeMultistreamHD96kPCM(frameSize, frameCount, layout.channels)
	var i16Frames [][]int16
	if int16Input {
		i16Frames = multistreamHD96kInt16Frames(f32Frames)
	}
	want, cSampleRate, cChannels, cStreams, cCoupled, cQEXT := encodeMultistreamHD96kWithLibopus(
		t, layout, qext, int16Input, frameSize, packetCap, bitrate, complexity, f32Frames, i16Frames)
	if cSampleRate != 96000 || cChannels != layout.channels || cStreams != layout.streams ||
		cCoupled != layout.coupled || cQEXT != qext {
		t.Fatalf("selected C encoder identity rate/channels/streams/coupled/QEXT=%d/%d/%d/%d/%t want 96000/%d/%d/%d/%t",
			cSampleRate, cChannels, cStreams, cCoupled, cQEXT,
			layout.channels, layout.streams, layout.coupled, qext)
	}

	enc, err := NewEncoder(96000, layout.channels, layout.streams, layout.coupled, layout.mapping)
	if err != nil {
		t.Fatalf("NewEncoder(96000, %d, %d, %d): %v", layout.channels, layout.streams, layout.coupled, err)
	}
	enc.SetBitrate(bitrate)
	enc.SetComplexity(complexity)
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetQEXT(qext)
	if enc.SampleRate() != cSampleRate || enc.Streams() != cStreams ||
		enc.CoupledStreams() != cCoupled || enc.QEXT() != cQEXT {
		t.Fatalf("Go encoder identity rate/streams/coupled/QEXT=%d/%d/%d/%t want selected C %d/%d/%d/%t",
			enc.SampleRate(), enc.Streams(), enc.CoupledStreams(), enc.QEXT(),
			cSampleRate, cStreams, cCoupled, cQEXT)
	}
	out := make([]byte, packetCap)
	for i := range frameCount {
		var n int
		if int16Input {
			n, err = enc.EncodeInt16(i16Frames[i], frameSize, out)
		} else {
			n, err = enc.Encode(f32Frames[i], frameSize, out)
		}
		if err != nil {
			t.Fatalf("frame %d Encode: %v", i, err)
		}
		gotPacket := out[:n]
		if !bytes.Equal(gotPacket, want[i].packet) {
			common := min(len(gotPacket), len(want[i].packet))
			firstDiff := 0
			for firstDiff < common && gotPacket[firstDiff] == want[i].packet[firstDiff] {
				firstDiff++
			}
			t.Fatalf("frame %d packet differs: Go len=%d C len=%d first diff=%d Go=%x C=%x",
				i, len(gotPacket), len(want[i].packet), firstDiff, gotPacket, want[i].packet)
		}
		if samples := getFrameDurationAtRateScratch(&enc.packetParser, gotPacket, 96000); samples != frameSize || samples != want[i].samples {
			t.Fatalf("frame %d samples=%d C=%d want=%d", i, samples, want[i].samples, frameSize)
		}
		if gotRange := enc.GetFinalRange(); gotRange != want[i].range32 {
			t.Fatalf("frame %d final range=%08x C=%08x", i, gotRange, want[i].range32)
		}
	}

	if qext && layout.streams == 1 && frameSize == 1920 &&
		!multistreamPacketHasQEXT(t, out[:len(want[frameCount-1].packet)]) {
		t.Fatal("selected 96 kHz QEXT sequence has no extension on the final 20 ms frame")
	}

	// The preceding C differential sequence warms every child encoder and its
	// native 96 kHz scratch. Repeated Encode calls then exercise the public
	// steady-state route with the same input format and caller-owned output.
	allocs := testing.AllocsPerRun(20, func() {
		var err error
		if int16Input {
			_, err = enc.EncodeInt16(i16Frames[frameCount-1], frameSize, out)
		} else {
			_, err = enc.Encode(f32Frames[frameCount-1], frameSize, out)
		}
		if err != nil {
			t.Fatalf("warmed Encode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warmed native 96 kHz multistream encode allocations=%g, want 0", allocs)
	}
}

func encodeMultistreamHD96kWithLibopus(t *testing.T, layout multistreamHD96kLayout,
	qext, int16Input bool, frameSize, packetCap, bitrate, complexity int,
	f32Frames [][]float32, i16Frames [][]int16,
) ([]multistreamHD96kEncodedFrame, int, int, int, int, bool) {
	t.Helper()
	bin, err := multistreamHD96kEncodeHelper.Path(buildMultistreamHD96kEncodeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "96 kHz multistream encoder", err)
		return nil, 0, 0, 0, 0, false
	}
	format := uint32(0)
	if int16Input {
		format = 1
	}
	payload := libopustest.NewOraclePayloadVersion("GM96", 2,
		boolU32(qext), uint32(frameSize), uint32(len(f32Frames)), uint32(packetCap),
		uint32(bitrate), uint32(complexity), uint32(layout.channels),
		uint32(layout.streams), uint32(layout.coupled), format)
	for i := range f32Frames {
		if int16Input {
			for _, sample := range i16Frames[i] {
				payload.I16(sample)
			}
		} else {
			payload.Float32s(f32Frames[i]...)
		}
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "96 kHz multistream encoder", "GMSO", 1)
	if err != nil {
		libopustest.HelperUnavailable(t, "96 kHz multistream encoder", err)
		return nil, 0, 0, 0, 0, false
	}
	rate, channels, streams, coupled, gotQEXT := int(reader.U32()), int(reader.U32()),
		int(reader.U32()), int(reader.U32()), reader.U32() != 0
	count := int(reader.U32())
	if count != len(f32Frames) {
		t.Fatalf("selected C frame count=%d want %d", count, len(f32Frames))
	}
	want := make([]multistreamHD96kEncodedFrame, count)
	for i := range want {
		n := int(reader.U32())
		want[i].samples = int(reader.U32())
		want[i].range32 = reader.U32()
		packetLen := int(reader.U32())
		if n <= 0 || packetLen != n || n > packetCap {
			t.Fatalf("selected C frame %d packet lengths=%d/%d cap=%d", i, n, packetLen, packetCap)
		}
		want[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
		if want[i].samples != frameSize {
			t.Fatalf("selected C frame %d sample count=%d want %d", i, want[i].samples, frameSize)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return want, rate, channels, streams, coupled, gotQEXT
}

func makeMultistreamHD96kPCM(frameSize, frameCount, channels int) [][]float32 {
	frames := make([][]float32, frameCount)
	for frame := range frameCount {
		pcm := make([]float32, frameSize*channels)
		for sample := range frameSize {
			i := frame*frameSize + sample
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/96000)
			high := 0.21 * math.Sin(2*math.Pi*30000*float64(i)/96000)
			for channel := range channels {
				lowScale := float64(channel+1) / float64(channels)
				highScale := float64(channels-channel) / float64(channels)
				value := low*lowScale + high*highScale
				s16 := int16(math.Round(value * 32767))
				pcm[sample*channels+channel] = float32(s16) / 32768
			}
		}
		frames[frame] = pcm
	}
	return frames
}

func multistreamHD96kInt16Frames(f32Frames [][]float32) [][]int16 {
	frames := make([][]int16, len(f32Frames))
	for frame := range f32Frames {
		frames[frame] = make([]int16, len(f32Frames[frame]))
		for i, sample := range f32Frames[frame] {
			frames[frame][i] = int16(math.Round(float64(sample) * 32768))
		}
	}
	return frames
}

func multistreamPacketHasQEXT(t *testing.T, packet []byte) bool {
	t.Helper()
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parse 96 kHz QEXT packet: %v", err)
	}
	extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
	if err != nil {
		t.Fatalf("parse 96 kHz QEXT extensions: %v", err)
	}
	for _, ext := range extensions {
		if ext.ID == qextPacketExtensionID && ext.Frame == 0 && len(ext.Data) > 0 {
			return true
		}
	}
	return false
}

func frameSizeName(frameSize int) string {
	switch frameSize {
	case 240:
		return "2p5ms"
	case 480:
		return "5ms"
	case 960:
		return "10ms"
	case 1920:
		return "20ms"
	default:
		return "invalid"
	}
}

func boolName(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func boolU32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}
