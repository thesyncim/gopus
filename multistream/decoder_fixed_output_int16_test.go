//go:build gopus_fixed_point

package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestFixedPublicMultistreamHybridDecodeToInt16MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		frameCount = 6
	)
	pcm := make([]float32, frameSize*frameCount)
	for i := range pcm {
		time := float64(i) / sampleRate
		pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*time) + 0.1*math.Sin(2*math.Pi*1900*time))
	}
	records, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate: sampleRate, Channels: channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeHybrid,
		Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
		Bitrate:       24000,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalMusic,
		VBR:           true,
		VBRConstraint: false,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		t.Fatalf("selected libopus encode: %v", err)
	}
	var cases []libopustest.DecodeDiffCase
	for _, record := range records {
		if record.Ret <= 1 || len(record.Packet) == 0 {
			continue
		}
		cfg := record.Packet[0] >> 3
		if cfg < 12 || cfg > 15 {
			continue
		}
		cases = append(cases, libopustest.DecodeDiffCase{
			Packet: record.Packet, Format: libopustest.DecodeDiffFormatInt16, FrameSize: frameSize,
		})
	}
	if len(cases) == 0 {
		t.Fatal("selected C encoder produced no Hybrid packets")
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		t.Fatalf("selected libopus decode sequence: %v", err)
	}
	dec, err := NewDecoder(sampleRate, channels, 1, 0, []byte{0})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	for frame, decodeCase := range cases {
		got, err := dec.DecodeToInt16(decodeCase.Packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt16: %v", frame, err)
		}
		if want[frame].Code != frameSize {
			t.Fatalf("frame %d selected C returned %d samples, want %d", frame, want[frame].Code, frameSize)
		}
		wantPCM := want[frame].Int16()
		if len(got) != len(wantPCM) {
			t.Fatalf("frame %d sample count Go=%d C=%d", frame, len(got), len(wantPCM))
		}
		for i := range got {
			if got[i] != wantPCM[i] {
				t.Fatalf("frame %d sample %d: Go=%d selected C=%d", frame, i, got[i], wantPCM[i])
			}
		}
	}
}
