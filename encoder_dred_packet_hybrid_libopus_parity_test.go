//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"bytes"
	"testing"

	encpkg "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}
	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketExtensionFramingMatchesLibopus(t, gotPacket, packetInfo.packet)
}

func assertDREDPacketExtensionFramingMatchesLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}

func assertDREDPacketPrimaryFrameSizesMatchLibopus(t *testing.T, gotPacket, wantPacket []byte) {
	t.Helper()
	_, gotFrames, gotPadding, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parse got DRED packet frames: %v", err)
	}
	_, wantFrames, wantPadding, _, err := parsePacketFramesAndPadding(wantPacket)
	if err != nil {
		t.Fatalf("parse libopus DRED packet frames: %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("primary frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if len(gotFrames[i]) != len(wantFrames[i]) {
			t.Fatalf("primary frame %d length=%d want %d", i, len(gotFrames[i]), len(wantFrames[i]))
		}
	}
	if !bytes.Equal(gotPadding, wantPadding) {
		t.Fatalf("extension padding mismatch\n got=%x\nwant=%x", gotPadding, wantPadding)
	}
}
