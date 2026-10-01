package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecoderPitchAfterSILKRateResetMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	prime, packet := decodeFECTransitionFixture(t)
	packet[0] = 0x01 // NB SILK; the fixture has no LBRR.
	if PacketHasLBRR(packet) {
		t.Fatal("rate-reset packet unexpectedly carries LBRR")
	}

	const sampleRate, channels, frameSize = 48000, 1, 5760
	cases := []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	}
	want, err := probeSelectedLibopusPitch(t, sampleRate, channels, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder pitch sequence probe", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("selected C returned %d pitch records, want %d", len(want), len(cases))
	}
	if want[1].decoded != frameSize || want[1].pitch != 0 {
		t.Fatalf("selected C rate-reset FEC=(samples=%d,pitch=%d), want (%d,0)", want[1].decoded, want[1].pitch, frameSize)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize)
	fecPCM := make([]float32, frameSize)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	if primeSamples != int(want[0].decoded) {
		t.Fatalf("prime samples=%d C=%d", primeSamples, want[0].decoded)
	}
	if got := dec.Pitch(); got != int(want[0].pitch) {
		t.Fatalf("prime pitch=%d C=%d", got, want[0].pitch)
	}

	fecSamples, err := dec.DecodeWithFEC(packet, fecPCM, true)
	if err != nil {
		t.Fatalf("rate-reset FEC decode: %v", err)
	}
	if fecSamples != int(want[1].decoded) {
		t.Fatalf("rate-reset FEC samples=%d C=%d", fecSamples, want[1].decoded)
	}
	if got := dec.Pitch(); got != int(want[1].pitch) {
		t.Fatalf("rate-reset FEC pitch=%d C=%d", got, want[1].pitch)
	}
	if got := dec.silkDecoder.GetLastSignalType(); got != 0 {
		t.Fatalf("SILK last signal type after rate-reset PLC=%d, want inactive", got)
	}
}

type selectedLibopusPitchStep struct {
	decoded int32
	pitch   int32
}

func probeSelectedLibopusPitch(t *testing.T, sampleRate, channels int, cases []libopustest.DecodeDiffCase) ([]selectedLibopusPitchStep, error) {
	t.Helper()
	bin, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:      "decoder pitch sequence probe",
		OutputBase: "gopus_decoder_pitch_sequence",
		SourceFile: "libopus_decoder_pitch_sequence.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
	})
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GPI1", 1,
		uint32(sampleRate), uint32(channels), uint32(len(cases)))
	for _, step := range cases {
		decodeFEC := uint32(0)
		if step.DecodeFEC {
			decodeFEC = 1
		}
		payload.U32(step.FrameSize)
		payload.U32(decodeFEC)
		payload.U32(uint32(len(step.Packet)))
		payload.Raw(step.Packet)
	}
	wire, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		return nil, err
	}
	r, err := libopustest.NewOracleReader("decoder pitch sequence probe", "GPO1", wire)
	if err != nil {
		return nil, err
	}
	count := r.Count(len(cases))
	out := make([]selectedLibopusPitchStep, count)
	for i := range out {
		out[i].decoded = r.I32()
		if ctl := r.I32(); ctl != 0 {
			return nil, fmt.Errorf("selected C OPUS_GET_PITCH step %d returned %d", i, ctl)
		}
		out[i].pitch = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
