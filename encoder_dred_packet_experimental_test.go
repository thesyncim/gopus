//go:build gopus_unsupported_controls
// +build gopus_unsupported_controls

package gopus

import (
	"bytes"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
)

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		t.Skipf("libopus hybrid DRED packet helper unavailable: %v", err)
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
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40ms(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		t.Skipf("libopus 40 ms hybrid DRED packet helper unavailable: %v", err)
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
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msStereo(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		t.Skipf("libopus stereo hybrid DRED packet helper unavailable: %v", err)
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
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}
