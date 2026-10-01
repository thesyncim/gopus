package multistream

import (
	"errors"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestMultistreamPLCFrameSizeValidationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		badPLCSize = 121 // 2.5 ms is 120 samples at 48 kHz.
		shortCap   = 961 // Packet duration fits although this is not a PLC quantum.
	)
	pcm := make([]float32, frameSize*channels)
	for sample := range frameSize {
		phase := 2 * math.Pi * 440 * float64(sample) / sampleRate
		pcm[2*sample] = float32(0.35 * math.Sin(phase))
		pcm[2*sample+1] = float32(0.29 * math.Sin(phase+0.2))
	}
	encoded, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate: sampleRate, Channels: channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeCELTOnly,
		Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
		Bitrate:       64000,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalMusic,
		VBR:           true,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    1,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C packet", err)
	}
	if len(encoded) != 1 {
		t.Fatalf("selected C encoder returned %d records, want 1", len(encoded))
	}
	if encoded[0].Ret <= 0 {
		t.Fatalf("selected C encoder packet status=%d, want positive", encoded[0].Ret)
	}
	packet := encoded[0].Packet

	cases := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: badPLCSize},
		{Format: libopustest.DecodeDiffFormatInt16, FrameSize: badPLCSize},
		{Format: libopustest.DecodeDiffFormatInt24, FrameSize: badPLCSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: shortCap},
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C sequence", err)
	}
	control, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: shortCap},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "PLC frame-size C control", err)
	}
	if len(want) != len(cases) || len(control) != 2 {
		t.Fatalf("C sequence lengths got=%d control=%d", len(want), len(control))
	}
	if want[0].Code != frameSize || want[4].Code != frameSize || control[1].Code != frameSize {
		t.Fatalf("C valid decode statuses got=%d/%d control=%d", want[0].Code, want[4].Code, control[1].Code)
	}
	for i := 1; i <= 3; i++ {
		if want[i].Code != -1 {
			t.Fatalf("C invalid PLC format %d status=%d, want OPUS_BAD_ARG (-1)", cases[i].Format, want[i].Code)
		}
		if want[i].FinalRange != want[0].FinalRange {
			t.Fatalf("C invalid PLC call %d changed range %08x to %08x", i, want[0].FinalRange, want[i].FinalRange)
		}
	}
	if want[4].Code != control[1].Code || want[4].FinalRange != control[1].FinalRange {
		t.Fatal("C invalid PLC calls changed state before the recovery packet")
	}
	assertMSDecodeFloatFrameAware(t, want[4].Float32(), control[1].Float32(), channels, frameSize, nil, "C PLC recovery")

	dec, err := NewDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault: %v", err)
	}
	if _, err := dec.DecodeToFloat32(packet, frameSize); err != nil {
		t.Fatalf("prime DecodeToFloat32: %v", err)
	}
	if got := dec.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("prime range Go=%08x C=%08x", got, want[0].FinalRange)
	}

	output := make([]float32, badPLCSize*channels)
	for i := range output {
		output[i] = float32(i) + 0.25
	}
	sentinel := append([]float32(nil), output...)
	if n, err := dec.DecodeIntoFloat32(nil, output, badPLCSize); n != 0 || !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeIntoFloat32 invalid PLC=(%d,%v), want (0,ErrInvalidPacket)", n, err)
	}
	for i := range output {
		if output[i] != sentinel[i] {
			t.Fatalf("failed DecodeIntoFloat32 wrote output[%d]=%g, want %g", i, output[i], sentinel[i])
		}
	}
	if _, err := dec.DecodeToFloat32(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToFloat32 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if _, err := dec.DecodeToInt16(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToInt16 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if _, err := dec.DecodeToInt24(nil, badPLCSize); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("DecodeToInt24 invalid PLC error=%v, want ErrInvalidPacket", err)
	}
	if got := dec.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("invalid PLC calls changed range %08x to %08x", want[0].FinalRange, got)
	}

	partial := make([]float32, shortCap*channels)
	for i := range partial {
		partial[i] = float32(i) + 0.5
	}
	partialSentinel := append([]float32(nil), partial...)
	if n, err := dec.DecodeIntoFloat32(packet, partial, shortCap); err != nil || n != frameSize {
		t.Fatalf("DecodeIntoFloat32 packet fits short capacity=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	assertMSDecodeFloatFrameAware(t, partial[:frameSize*channels], want[4].Float32(), channels, frameSize, nil, "Go PLC recovery")
	for i := frameSize * channels; i < len(partial); i++ {
		if partial[i] != partialSentinel[i] {
			t.Fatalf("DecodeIntoFloat32 wrote beyond packet duration at output[%d]", i)
		}
	}
	if got := dec.FinalRange(); got != want[4].FinalRange {
		t.Fatalf("recovery range Go=%08x C=%08x", got, want[4].FinalRange)
	}
}
