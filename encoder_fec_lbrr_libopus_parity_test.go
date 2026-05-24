//go:build gopus_libopus_oracle

package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func reportPacketByteDiff(t *testing.T, packetIdx int, got, want []byte) {
	t.Helper()
	limit := len(got)
	if len(want) < limit {
		limit = len(want)
	}
	first := -1
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			first = i
			break
		}
	}
	if first < 0 && len(got) != len(want) {
		first = limit
	}
	t.Fatalf("packet %d DIVERGES (len got=%d want=%d first=%d)\n got=%x\nwant=%x",
		packetIdx, len(got), len(want), first, got, want)
}

func assertEncoderFECPacketSequenceByteExact(t *testing.T, cfg libopusFECPacketConfig, frames int) {
	t.Helper()
	pcm := fecParityPCMSequence(cfg.FrameSize, cfg.Channels, frames)
	wantPackets, err := emitLibopusFECPackets(cfg, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus FEC packets", err)
	}
	gotPackets, err := encodeGopusFECPackets(cfg, frames)
	if err != nil {
		t.Fatalf("encode gopus: %v", err)
	}
	if len(gotPackets) != len(wantPackets) {
		t.Fatalf("packet count=%d want %d", len(gotPackets), len(wantPackets))
	}
	const warmupPackets = 2 // LBRR payload in packet matches libopus cadence (first at index 2).
	lbrrSeen := false
	for i := range wantPackets {
		if packetHasInBandFEC(t, wantPackets[i]) {
			lbrrSeen = true
		}
		if i < warmupPackets {
			continue
		}
		if !bytes.Equal(gotPackets[i], wantPackets[i]) {
			reportPacketByteDiff(t, i, gotPackets[i], wantPackets[i])
		}
	}
	if !lbrrSeen {
		t.Fatal("libopus reference sequence produced no in-band LBRR packet")
	}
}

func TestEncoderSilkMonoFirstPacketByteExactWithFECMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := libopusFECPacketConfig{
		FrameSize: 960,
		Channels:  1,
		Bitrate:   fecParityBitrateForFrameSize(960),
		InBandFEC: true,
	}
	pcm := fecParityPCMSequence(cfg.FrameSize, cfg.Channels, 1)
	wantPackets, err := emitLibopusFECPackets(cfg, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus packet", err)
	}
	gotPackets, err := encodeGopusFECPackets(cfg, 1)
	if err != nil {
		t.Fatalf("encode gopus: %v", err)
	}
	if !bytes.Equal(gotPackets[0], wantPackets[0]) {
		reportPacketByteDiff(t, 0, gotPackets[0], wantPackets[0])
	}
}

func TestEncoderFECPacketSequenceByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name string
		cfg  libopusFECPacketConfig
	}{
		{
			name: "mono_20ms_dred_settings",
			cfg: libopusFECPacketConfig{
				FrameSize: 960, Channels: 1,
				Bitrate:   fecParityBitrateForFrameSize(960),
				InBandFEC: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertEncoderFECPacketSequenceByteExact(t, tc.cfg, 24)
		})
	}
}

func TestEncoderFECMono20msPacket1ByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := libopusFECPacketConfig{
		FrameSize: 960, Channels: 1, Bitrate: fecParityBitrateForFrameSize(960), InBandFEC: true,
	}
	pcm := fecParityPCMSequence(cfg.FrameSize, cfg.Channels, 2)
	want, _ := emitLibopusFECPackets(cfg, pcm)
	got, _ := encodeGopusFECPackets(cfg, 2)
	if !bytes.Equal(got[0], want[0]) {
		reportPacketByteDiff(t, 0, got[0], want[0])
	}
	if !bytes.Equal(got[1], want[1]) {
		reportPacketByteDiff(t, 1, got[1], want[1])
	}
}

func TestEncoderFECStereo20msPacket2ByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := libopusFECPacketConfig{
		FrameSize: 960, Channels: 2, Bitrate: fecParityBitrateForFrameSize(960), InBandFEC: true,
	}
	pcm := fecParityPCMSequence(cfg.FrameSize, cfg.Channels, 3)
	want, err := emitLibopusFECPackets(cfg, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "libopus FEC packets", err)
	}
	got, err := encodeGopusFECPackets(cfg, 3)
	if err != nil {
		t.Fatalf("encode gopus: %v", err)
	}
	if !packetHasInBandFEC(t, want[2]) {
		t.Fatal("libopus packet 2 missing LBRR")
	}
	if !bytes.Equal(got[2], want[2]) {
		reportPacketByteDiff(t, 2, got[2], want[2])
	}
}
