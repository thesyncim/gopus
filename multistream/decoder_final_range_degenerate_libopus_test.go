package multistream

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamFinalRangeTracksLastCompletedFrame(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 5760
	)
	packet, err := hex.DecodeString("4b82015d6c4724ea0378fb4d130329a3bb118c3dcf94d3ab4fb99572856213c0715e2741d0afda05128fa3b48962aa234661fc0c0bfff29c89c20353c28a7d31bc415a9d01542ceca42a3c54da9332d8490e84c2876d3367d4b1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parse source packet: %v", err)
	}
	firstFrameBytes := 0
	if len(parsed.frames) > 0 {
		firstFrameBytes = len(parsed.frames[0])
	}
	if packet[0]&3 != 3 || packet[1]&0xC0 != 0x80 || len(parsed.frames) != 2 || firstFrameBytes != 1 {
		t.Fatalf("source packet framing code=%d first-frame-bytes=%d frames=%d, want code 3, one-byte first frame, two frames",
			packet[0]&3, firstFrameBytes, len(parsed.frames))
	}
	if len(parsed.frames[1]) > 251 {
		t.Fatalf("second frame has %d bytes, test packet builder expects a one-byte VBR size", len(parsed.frames[1]))
	}

	validThenEmpty := make([]byte, len(packet))
	validThenEmpty[0] = packet[0]
	validThenEmpty[1] = 0x82 // Code 3, VBR, two frames.
	validThenEmpty[2] = byte(len(parsed.frames[1]))
	copy(validThenEmpty[3:], parsed.frames[1])
	copy(validThenEmpty[3+len(parsed.frames[1]):], parsed.frames[0])
	reversed, err := parseOpusPacket(validThenEmpty, false)
	if err != nil || len(reversed.frames) != 2 || len(reversed.frames[0]) <= 1 || len(reversed.frames[1]) != 1 {
		t.Fatalf("reversed packet framing=%v frame lengths=%v, want coded frame then one-byte frame", err, frameLengths(reversed.frames))
	}

	variants := []struct {
		name      string
		packet    []byte
		wantRange uint32
	}{
		{name: "empty_then_coded", packet: packet, wantRange: 0x0a850556},
		{name: "coded_then_empty", packet: validThenEmpty, wantRange: 0},
	}
	formats := []struct {
		name              string
		multistreamFormat uint32
		sequenceFormat    uint32
	}{
		{name: "float32", multistreamFormat: libopustest.MultistreamDecodeFormatFloat32, sequenceFormat: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", multistreamFormat: libopustest.MultistreamDecodeFormatInt16, sequenceFormat: libopustest.DecodeDiffFormatInt16},
		{name: "int24", multistreamFormat: libopustest.MultistreamDecodeFormatInt24, sequenceFormat: libopustest.DecodeDiffFormatInt24},
	}

	oracleCases := make([]libopustest.MultistreamDecodeCase, 0, len(variants)*len(formats))
	for _, variant := range variants {
		for _, format := range formats {
			oracleCases = append(oracleCases, libopustest.MultistreamDecodeCase{
				SampleRate: sampleRate,
				Format:     format.multistreamFormat,
				Family:     0,
				Channels:   1,
				Streams:    1,
				FrameSize:  frameSize,
				Mapping:    []byte{0},
				Packet:     variant.packet,
			})
		}
	}
	freshWant, err := libopustest.ProbeMultistreamDecodeFresh(oracleCases)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream degenerate-frame final-range oracle", err)
	}
	if len(freshWant) != len(oracleCases) {
		t.Fatalf("fresh C results=%d want %d", len(freshWant), len(oracleCases))
	}

	for variantIndex, variant := range variants {
		for formatIndex, format := range formats {
			t.Run(variant.name+"/"+format.name, func(t *testing.T) {
				want := freshWant[variantIndex*len(formats)+formatIndex]
				if want.Code != 1920 || want.FinalRange != variant.wantRange {
					t.Fatalf("C packet result=(%d,%08x), want (1920,%08x)", want.Code, want.FinalRange, variant.wantRange)
				}

				sequenceWant, err := libopustest.ProbeDecodeSequence(sampleRate, 1, []libopustest.DecodeDiffCase{
					{Packet: variant.packet, Format: format.sequenceFormat, FrameSize: frameSize},
					{Format: format.sequenceFormat, FrameSize: frameSize},
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "multistream degenerate-frame PLC follow-up oracle", err)
				}
				if len(sequenceWant) != 2 || sequenceWant[0].Code != want.Code ||
					sequenceWant[0].FinalRange != want.FinalRange || !bytes.Equal(sequenceWant[0].PCM, want.PCM) {
					t.Fatalf("C sequence first result differs from fresh multistream oracle")
				}
				if sequenceWant[1].Code <= 0 || sequenceWant[1].FinalRange != 0 {
					t.Fatalf("C PLC follow-up result=(%d,%08x), want positive samples and zero range", sequenceWant[1].Code, sequenceWant[1].FinalRange)
				}

				decoder, err := NewDecoderDefault(sampleRate, 1)
				if err != nil {
					t.Fatalf("NewDecoderDefault: %v", err)
				}
				gotPCM, gotCount, err := decodeDegenerateRangePacket(decoder, format.multistreamFormat, variant.packet, frameSize)
				if err != nil {
					t.Fatalf("decode packet: %v", err)
				}
				if gotCount != int(want.Code) || !bytes.Equal(gotPCM, want.PCM) {
					t.Fatalf("packet PCM/count mismatch Go=%d/%d C=%d/%d", gotCount, len(gotPCM), want.Code, len(want.PCM))
				}
				if gotRange := decoder.FinalRange(); gotRange != want.FinalRange {
					t.Fatalf("packet final range Go=%08x C=%08x", gotRange, want.FinalRange)
				}
				if gotDuration := decoder.LastPacketDuration(); gotDuration != int(want.Code) {
					t.Fatalf("packet duration=%d, want decoded duration %d", gotDuration, want.Code)
				}
				if decoder.firstStreamState().InDTX() {
					t.Fatal("multi-frame packet was reported as DTX from its one-byte PLC frame")
				}

				if _, err := decoder.DecodeToFloat32([]byte{0xff}, frameSize); err == nil {
					t.Fatal("malformed packet decoded without an error")
				}
				if gotRange := decoder.FinalRange(); gotRange != want.FinalRange {
					t.Fatalf("malformed packet changed final range to %08x, want %08x", gotRange, want.FinalRange)
				}
				if _, err := decoder.DecodeToFloat32(variant.packet, int(want.Code)-1); !errors.Is(err, ErrBufferTooSmall) {
					t.Fatalf("short output capacity error=%v, want ErrBufferTooSmall", err)
				}
				if gotRange := decoder.FinalRange(); gotRange != want.FinalRange {
					t.Fatalf("short output capacity changed final range to %08x, want %08x", gotRange, want.FinalRange)
				}

				gotPCM, gotCount, err = decodeDegenerateRangePacket(decoder, format.multistreamFormat, nil, frameSize)
				if err != nil {
					t.Fatalf("decode nil PLC follow-up: %v", err)
				}
				if gotCount != int(sequenceWant[1].Code) || !bytes.Equal(gotPCM, sequenceWant[1].PCM) {
					t.Fatalf("PLC PCM/count mismatch Go=%d/%d C=%d/%d", gotCount, len(gotPCM), sequenceWant[1].Code, len(sequenceWant[1].PCM))
				}
				if gotRange := decoder.FinalRange(); gotRange != sequenceWant[1].FinalRange {
					t.Fatalf("PLC follow-up final range Go=%08x C=%08x", gotRange, sequenceWant[1].FinalRange)
				}
				if gotDuration := decoder.LastPacketDuration(); gotDuration != int(sequenceWant[1].Code) {
					t.Fatalf("PLC follow-up duration=%d, want %d", gotDuration, sequenceWant[1].Code)
				}
				decoder.Reset()
				if gotRange := decoder.FinalRange(); gotRange != 0 {
					t.Fatalf("reset final range=%08x, want 0", gotRange)
				}
			})
		}
	}
}

func decodeDegenerateRangePacket(decoder *Decoder, format uint32, packet []byte, frameSize int) ([]byte, int, error) {
	switch format {
	case libopustest.MultistreamDecodeFormatFloat32:
		pcm, err := decoder.DecodeToFloat32(packet, frameSize)
		if err != nil {
			return nil, 0, err
		}
		out := make([]byte, 4*len(pcm))
		for i, sample := range pcm {
			binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(sample))
		}
		return out, len(pcm), nil
	case libopustest.MultistreamDecodeFormatInt16:
		pcm, err := decoder.DecodeToInt16(packet, frameSize)
		if err != nil {
			return nil, 0, err
		}
		out := make([]byte, 2*len(pcm))
		for i, sample := range pcm {
			binary.LittleEndian.PutUint16(out[2*i:], uint16(sample))
		}
		return out, len(pcm), nil
	case libopustest.MultistreamDecodeFormatInt24:
		pcm, err := decoder.DecodeToInt24(packet, frameSize)
		if err != nil {
			return nil, 0, err
		}
		out := make([]byte, 4*len(pcm))
		for i, sample := range pcm {
			binary.LittleEndian.PutUint32(out[4*i:], uint32(sample))
		}
		return out, len(pcm), nil
	default:
		return nil, 0, ErrInvalidPacket
	}
}

func frameLengths(frames [][]byte) []int {
	lengths := make([]int, len(frames))
	for i := range frames {
		lengths[i] = len(frames[i])
	}
	return lengths
}
