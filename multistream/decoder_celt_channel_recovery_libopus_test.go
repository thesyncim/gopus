package multistream

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestMultistreamCELTChannelRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	mono, err := hex.DecodeString("f87de653c754da8fd5b84b5c152311f42e2736250011668e86ffe7531d2559002d1069c04ba89ce0191fa84b411482b75a8d02fdc36760a2d58341a47247422ebdaad83ec8e520711ffaccd38509bac00000000000000000000000000000000000056be55a555117d558557cf16e8b3101f0c24f72c33861766a8be92409a7147be6532762112640002dc15097392136b2b9382e1e0705696039660141a53754")
	if err != nil {
		t.Fatal(err)
	}
	stereo := encodeModeSwitchSingleStreamPackets(t, 2, 960, []encoder.Mode{encoder.ModeCELT})[0]
	if stereo[0]&4 == 0 {
		t.Fatal("recovery packet must be stereo")
	}
	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, losses := range []int{0, 1, 2, 6, 15} {
			t.Run(fmt.Sprintf("fs%d/losses%d", rate, losses), func(t *testing.T) {
				packets := make([][]byte, losses+3)
				packets[0] = mono
				packets[len(packets)-2] = stereo
				packets[len(packets)-1] = stereo
				assertMultistreamSequenceFormatsMatchSelectedLibopus(t, rate, 2, packets)
			})
		}
	}
}

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

func TestMultistreamHybridRedundancyFinalRangeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	ref := projectionDecodeRef(t, 9, 480, 2, 256000)
	first, err := parseMultistreamPacket(ref.packets[0], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	second, err := parseMultistreamPacket(ref.packets[1], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	const stream = 4
	if len(first) <= stream || len(second) <= stream || parseStreamTOC(second[stream][0]).mode != streamModeHybrid {
		t.Fatal("selected projection packet does not contain the Hybrid stream")
	}
	channels := streamChannels(stream, ref.coupledStreams)
	if channels != 1 {
		t.Fatalf("Hybrid stream channels=%d, want 1", channels)
	}
	cases := []libopustest.DecodeDiffCase{
		{Packet: first[stream], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
		{Packet: second[stream], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 480},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, cases)
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	decoder, err := NewDecoder(48000, channels, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for i, decodeCase := range cases {
		got, err := decoder.DecodeToFloat32(decodeCase.Packet, 480)
		if err != nil {
			t.Fatalf("step %d decode: %v", i, err)
		}
		if want[i].Code != 480 || len(got) != 480 {
			t.Fatalf("step %d samples Go=%d C=%d", i, len(got), want[i].Code)
		}
		wantPCM := want[i].Float32()
		if len(wantPCM) != len(got) {
			t.Fatalf("step %d PCM length Go=%d C=%d", i, len(got), len(wantPCM))
		}
		for sample, value := range got {
			if math.Float32bits(value) != math.Float32bits(wantPCM[sample]) {
				t.Fatalf("step %d PCM sample %d Go=%08x C=%08x", i, sample, math.Float32bits(value), math.Float32bits(wantPCM[sample]))
			}
		}
		if i == 1 && decoder.decoders[0].(*streamState).hybridDec.FinalRange() == want[i].FinalRange {
			t.Fatal("Hybrid packet did not exercise a redundant range contribution")
		}
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
	decoder.Reset()
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("reset decoder final range=%08x, want 0", got)
	}
}

func TestMultistreamLongPLCBurstMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, mode := range []encoder.Mode{encoder.ModeSILK, encoder.ModeHybrid, encoder.ModeCELT} {
		for _, channels := range []int{1, 2} {
			good := encodeModeSwitchSingleStreamPackets(t, channels, 960, []encoder.Mode{mode})[0]
			packets := make([][]byte, 18)
			packets[0] = good
			packets[16] = good
			packets[17] = good
			for _, rate := range rates {
				t.Run(fmt.Sprintf("mode%d/fs%d/ch%d", mode, rate, channels), func(t *testing.T) {
					assertMultistreamSequenceFormatsMatchSelectedLibopus(t, rate, channels, packets)
				})
			}
		}
	}
}

type previousRedundancyPLCMatrixCase struct {
	name       string
	sampleRate int
	frameSize  int
	channels   int
	gainQ8     int
	format     uint32
}

const (
	previousRedundancyPCMFloat32 uint32 = iota
	previousRedundancyPCMInt16
	previousRedundancyPCMInt24
)

func TestPLCModeAfterCELTRedundancyAcrossFormatsMatchesLibopus(t *testing.T) {
	cases := []previousRedundancyPLCMatrixCase{
		{name: "48k_mono_10ms_float_zero_gain", sampleRate: 48000, frameSize: 480, channels: 1, format: previousRedundancyPCMFloat32},
		{name: "48k_mono_20ms_int24_plus8db", sampleRate: 48000, frameSize: 960, channels: 1, gainQ8: 2048, format: previousRedundancyPCMInt24},
		{name: "48k_stereo_10ms_int16_minus8db", sampleRate: 48000, frameSize: 480, channels: 2, gainQ8: -2048, format: previousRedundancyPCMInt16},
		{name: "48k_stereo_20ms_int24_saturated_gain", sampleRate: 48000, frameSize: 960, channels: 2, gainQ8: 8192, format: previousRedundancyPCMInt24},
		{name: "24k_mono_10ms_float_minus8db", sampleRate: 24000, frameSize: 240, channels: 1, gainQ8: -2048, format: previousRedundancyPCMFloat32},
		{name: "24k_stereo_20ms_int16_zero_gain", sampleRate: 24000, frameSize: 480, channels: 2, format: previousRedundancyPCMInt16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodePreviousRedundancyPLCMatrixPackets(t, tc)
			sequence := [][]byte{packets[0], packets[1], packets[2], nil, nil, packets[3]}
			mapping := []byte{0}
			coupled := 0
			if tc.channels == 2 {
				mapping = []byte{0, 1}
				coupled = 1
			}
			const streams = 1
			wantFloat, wantInt16, wantInt24 := referencePLCMatrixOutput(t, tc, streams, coupled, mapping, sequence)

			dec, err := NewDecoder(tc.sampleRate, tc.channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(tc.gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", tc.gainQ8, err)
			}
			gotFloat := make([]float32, 0, len(wantFloat))
			gotInt16 := make([]int16, 0, len(wantInt16))
			gotInt24 := make([]int32, 0, len(wantInt24))
			for i, packet := range sequence {
				switch tc.format {
				case previousRedundancyPCMFloat32:
					pcm, err := dec.DecodeToFloat32(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToFloat32: %v", i, err)
					}
					gotFloat = append(gotFloat, pcm...)
				case previousRedundancyPCMInt16:
					pcm, err := dec.DecodeToInt16(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt16: %v", i, err)
					}
					gotInt16 = append(gotInt16, pcm...)
				case previousRedundancyPCMInt24:
					pcm, err := dec.DecodeToInt24(packet, tc.frameSize)
					if err != nil {
						t.Fatalf("frame %d DecodeToInt24: %v", i, err)
					}
					gotInt24 = append(gotInt24, pcm...)
				}
				if i == 2 {
					assertPreviousRedundancySelectsCELT(t, dec)
				}
				if i == 3 {
					assertPLCConsumedPreviousRedundancy(t, dec)
				}
			}
			switch tc.format {
			case previousRedundancyPCMFloat32:
				assertPLCMatrixFloat32(t, gotFloat, wantFloat)
			case previousRedundancyPCMInt16:
				assertPLCMatrixInt16(t, gotInt16, wantInt16)
			case previousRedundancyPCMInt24:
				assertPLCMatrixInt24(t, gotInt24, wantInt24)
			}
		})
	}
}

func encodePreviousRedundancyPLCMatrixPackets(t *testing.T, tc previousRedundancyPLCMatrixCase) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)
	const frameCount = 4
	perFrame := tc.frameSize * tc.channels
	pcm := make([]int16, perFrame*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < tc.frameSize; i++ {
			for ch := 0; ch < tc.channels; ch++ {
				time := float64(frame*tc.frameSize+i) / float64(tc.sampleRate)
				frequency := 220 + ch*90
				value := 0.23*math.Sin(2*math.Pi*float64(frequency)*time) + 0.07*math.Sin(2*math.Pi*1700*time)
				pcm[frame*perFrame+i*tc.channels+ch] = int16(value * 32767)
			}
		}
	}
	maxBandwidth := libopustest.OpusBandwidthFullband
	if tc.sampleRate == 24000 {
		maxBandwidth = libopustest.OpusBandwidthSuperwideband
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{ShortPCM: pcm[:perFrame], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[perFrame : 2*perFrame], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[2*perFrame : 3*perFrame], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: maxBandwidth},
		{ShortPCM: pcm[3*perFrame:], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: maxBandwidth},
	}
	params := libopustest.OpusEncodeFixedParams{
		SampleRate:    tc.sampleRate,
		Channels:      tc.channels,
		Application:   libopustest.OpusApplicationAudio,
		Bitrate:       128000,
		Complexity:    10,
		ForceChannels: tc.channels,
		LSBDepth:      16,
		FrameSize:     tc.frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected-C CELT redundancy matrix sequence", err)
	}
	if len(records) != frameCount {
		t.Fatalf("selected-C encoder returned %d frames, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("selected-C frame %d status=%d packetBytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	if parseStreamTOC(packets[0][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[1][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[2][0]).mode != streamModeHybrid ||
		parseStreamTOC(packets[3][0]).mode != streamModeCELT {
		t.Fatalf("selected-C mode sequence sampleRate=%d frameSize=%d channels=%d is %v, want SILK/SILK/Hybrid/CELT",
			tc.sampleRate, tc.frameSize, tc.channels,
			[]int{parseStreamTOC(packets[0][0]).mode, parseStreamTOC(packets[1][0]).mode, parseStreamTOC(packets[2][0]).mode, parseStreamTOC(packets[3][0]).mode})
	}
	return packets
}

func referencePLCMatrixOutput(t *testing.T, tc previousRedundancyPLCMatrixCase, streams, coupled int, mapping []byte, packets [][]byte) ([]float32, []int16, []int32) {
	t.Helper()
	switch tc.format {
	case previousRedundancyPCMFloat32:
		want, err := decodeWithLibopusReferencePacketsGain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C float PLC matrix decode", err)
		}
		return want, nil, nil
	case previousRedundancyPCMInt16:
		want, err := decodeWithLibopusReferencePacketsInt16Gain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C int16 PLC matrix decode", err)
		}
		return nil, want, nil
	case previousRedundancyPCMInt24:
		want, err := decodeWithLibopusReferencePacketsInt24Gain(1, tc.sampleRate, tc.channels, streams, coupled, tc.frameSize, tc.gainQ8, mapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "selected-C int24 PLC matrix decode", err)
		}
		return nil, nil, want
	default:
		t.Fatalf("unsupported output format %d", tc.format)
		return nil, nil, nil
	}
}

func assertPLCMatrixFloat32(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("float32 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("float32 sample %d Go=%08x C=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func assertPLCMatrixInt16(t *testing.T, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("int16 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("int16 sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}
}

func assertPLCMatrixInt24(t *testing.T, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("int24 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("int24 sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}
}

const (
	prevRedundancySampleRate = 48000
	prevRedundancyFrameSize  = 960
)

func encodePrevRedundancyPLCSequence(t *testing.T) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)

	const frameCount = 4
	pcm := make([]int16, prevRedundancyFrameSize*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < prevRedundancyFrameSize; i++ {
			time := float64(frame*prevRedundancyFrameSize+i) / prevRedundancySampleRate
			value := 0.25*math.Sin(2*math.Pi*220*time) + 0.08*math.Sin(2*math.Pi*1700*time)
			pcm[frame*prevRedundancyFrameSize+i] = int16(value * 32767)
		}
	}

	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{
			ShortPCM:  pcm[:prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeSILKOnly,
			Bandwidth: libopustest.OpusBandwidthWideband,
		},
		{
			ShortPCM:  pcm[prevRedundancyFrameSize : 2*prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeSILKOnly,
			Bandwidth: libopustest.OpusBandwidthWideband,
		},
		{
			ShortPCM:  pcm[2*prevRedundancyFrameSize : 3*prevRedundancyFrameSize],
			ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband,
		},
		{
			ShortPCM:  pcm[3*prevRedundancyFrameSize:],
			ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband,
		},
	}
	params := libopustest.OpusEncodeFixedParams{
		SampleRate:    prevRedundancySampleRate,
		Channels:      1,
		Application:   libopustest.OpusApplicationAudio,
		Bitrate:       128000,
		Complexity:    10,
		ForceChannels: 1,
		LSBDepth:      16,
		FrameSize:     prevRedundancyFrameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected-C CELT redundancy sequence", err)
	}
	if len(records) != frameCount {
		t.Fatalf("encoder returned %d records, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("frame %d encoded status=%d packet-bytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	if parseStreamTOC(packets[0][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[1][0]).mode != streamModeSILK ||
		parseStreamTOC(packets[2][0]).mode != streamModeHybrid ||
		parseStreamTOC(packets[3][0]).mode != streamModeCELT {
		t.Fatalf("selected-C mode sequence is %v, want SILK/SILK/Hybrid/CELT",
			[]int{parseStreamTOC(packets[0][0]).mode, parseStreamTOC(packets[1][0]).mode, parseStreamTOC(packets[2][0]).mode, parseStreamTOC(packets[3][0]).mode})
	}
	return packets
}

func assertPreviousRedundancySelectsCELT(t *testing.T, dec *Decoder) {
	t.Helper()
	st, ok := dec.decoders[0].(*streamState)
	if !ok || st.lastMode != streamModeHybrid || !st.prevRedundancy {
		if ok {
			t.Fatalf("redundancy seed state lastMode=%d prevRedundancy=%v, want Hybrid with CELT redundancy", st.lastMode, st.prevRedundancy)
		}
		t.Fatal("single stream decoder state has an unexpected type")
	}
}

func assertPLCConsumedPreviousRedundancy(t *testing.T, dec *Decoder) {
	t.Helper()
	st, ok := dec.decoders[0].(*streamState)
	if !ok || st.lastMode != streamModeCELT || st.prevRedundancy {
		if ok {
			t.Fatalf("post-PLC state lastMode=%d prevRedundancy=%v, want CELT with redundancy consumed", st.lastMode, st.prevRedundancy)
		}
		t.Fatal("single stream decoder state has an unexpected type")
	}
}

func TestMultistreamDTXSingleGain(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}

	// Encode a low-level stereo tone so PLC/DTX produces non-zero audio while
	// staying below the 16-bit saturation ceiling in libopus's FIXED_POINT +
	// ENABLE_RES24 decode_gain path (SATURATE(x, 32767) on 24-bit opus_res).
	in := make([]float32, 960*2)
	for i := range 960 {
		v := 0.001 * float32(math.Sin(2*math.Pi*440*float64(i)/48000))
		in[2*i] = v
		in[2*i+1] = v
	}
	pktBuf := make([]byte, 4000)
	var pktLen int
	for range 3 {
		pktLen, err = enc.Encode(in, 960, pktBuf)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	pkt := pktBuf[:pktLen]

	dec0, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault(0): %v", err)
	}
	decGain, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault(gain): %v", err)
	}
	// +6.02 dB (2.0x linear gain).
	decGain.SetGain(1536)

	buf0 := make([]float32, 960*2)
	bufGain := make([]float32, 960*2)
	if _, err := dec0.DecodeIntoFloat32(pkt, buf0, 960); err != nil {
		t.Fatalf("dec0 prime: %v", err)
	}
	if _, err := decGain.DecodeIntoFloat32(pkt, bufGain, 960); err != nil {
		t.Fatalf("decGain prime: %v", err)
	}

	// Decode a 1-byte DTX frame (TOC byte only). Gain must be applied once (~2x), not twice (~4x).
	dtx := []byte{pkt[0]}
	if _, err := dec0.DecodeIntoFloat32(dtx, buf0, 960); err != nil {
		t.Fatalf("dec0 DTX: %v", err)
	}
	if _, err := decGain.DecodeIntoFloat32(dtx, bufGain, 960); err != nil {
		t.Fatalf("decGain DTX: %v", err)
	}

	var e0, eGain float64
	for i := range buf0 {
		e0 += float64(buf0[i]) * float64(buf0[i])
		eGain += float64(bufGain[i]) * float64(bufGain[i])
	}
	if e0 == 0 {
		t.Fatal("expected non-zero DTX concealment energy")
	}
	ratio := math.Sqrt(eGain / e0)
	want := math.Pow(10.0, 1536.0/(20.0*256.0))
	if math.Abs(ratio-want) > 0.05 {
		t.Fatalf("DTX gain ratio = %.4f, want %.4f (double gain would be %.4f)", ratio, want, want*want)
	}
}

func TestMultistreamMalformedOversizedPacketDoesNotGrowReframeArena(t *testing.T) {
	dec, err := NewDecoder(48000, 4, 2, 2, []byte{0, 1, 2, 3})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	// 4 MB malformed packet (code 0 frame exceeding maxOpusFrameBytes).
	bad := make([]byte, 4*1024*1024)
	bad[0] = 0xfc
	out := make([]float32, 960*4)
	if _, err := dec.DecodeIntoFloat32(bad, out, 960); err == nil {
		t.Fatal("DecodeIntoFloat32(4MB malformed) succeeded; want error")
	}
	if got := dec.reframeArena.Cap(); got != 0 {
		t.Fatalf("reframeArena cap = %d after malformed packet, want 0", got)
	}
}

func TestMultistreamInvalidPacketDoesNotCorruptLastPacketDuration(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	dec, err := NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	pktBuf := make([]byte, 4000)
	pktLen, err := enc.Encode(make([]float32, 960*2), 960, pktBuf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	pkt := pktBuf[:pktLen]
	out := make([]float32, 960*2)
	if _, err := dec.DecodeIntoFloat32(pkt, out, 960); err != nil {
		t.Fatalf("DecodeIntoFloat32 valid: %v", err)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration() = %d, want 960", got)
	}

	// Malformed packet must not overwrite lastPacketDuration.
	if _, err := dec.DecodeIntoFloat32([]byte{0x03, 0x00}, out, 960); err == nil {
		t.Fatal("DecodeIntoFloat32(malformed) succeeded; want error")
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration() after malformed packet = %d, want 960", got)
	}
}

func testSILKRedundancyTwoFramePacket(t *testing.T) []byte {
	t.Helper()
	frame, err := hex.DecodeString("e1456147e59e3d5223c014e14994368219fc6faa569563dfa98b7b2419fc5daa569563dfa98b7b247d2d5cf1629eaaf9e297034c0c8e")
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 1+2*len(frame))
	packet[0] = 0x05 // coupled SILK, code 1: two equal-size frames
	copy(packet[1:], frame)
	copy(packet[1+len(frame):], frame)
	return packet
}

func TestMultistreamSILKRedundancyFinalRangeMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := testSILKRedundancyTwoFramePacket(t)
	cases := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 2, cases)
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	decoder, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("new decoder final range=%08x, want 0", got)
	}
	for i, decodeCase := range cases {
		got, err := decoder.DecodeToFloat32(decodeCase.Packet, 960)
		if err != nil {
			t.Fatalf("step %d decode: %v", i, err)
		}
		if want[i].Code != 960 || len(got) != 1920 {
			t.Fatalf("step %d samples Go=%d C=%d", i, len(got)/2, want[i].Code)
		}
		wantPCM := want[i].Float32()
		if len(wantPCM) != len(got) {
			t.Fatalf("step %d PCM length Go=%d C=%d", i, len(got), len(wantPCM))
		}
		for sample, value := range got {
			if math.Float32bits(value) != math.Float32bits(wantPCM[sample]) {
				t.Fatalf("step %d PCM sample %d Go=%08x C=%08x", i, sample, math.Float32bits(value), math.Float32bits(wantPCM[sample]))
			}
		}
		if gotRange := decoder.FinalRange(); gotRange != want[i].FinalRange {
			t.Fatalf("step %d final range Go=%08x C=%08x", i, gotRange, want[i].FinalRange)
		}
	}
	decoder.Reset()
	if got := decoder.FinalRange(); got != 0 {
		t.Fatalf("reset decoder final range=%08x, want 0", got)
	}
	if _, err := decoder.DecodeToFloat32(packet, 960); err != nil {
		t.Fatalf("post-reset decode: %v", err)
	}
	if got := decoder.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("post-reset final range Go=%08x C=%08x", got, want[0].FinalRange)
	}
}
