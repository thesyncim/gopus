//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"bytes"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeCELT || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want celt stereo", toc)
	}

	// DRED payload offset and bytes are independent of the CELT primary-frame
	// length, so we still gate on them strictly.
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}

	delta := len(gotPacket) - len(packetInfo.packet)
	if delta != 0 {
		t.Fatalf("CELT FB stereo packet length=%d want %d (delta=%d bytes)",
			len(gotPacket), len(packetInfo.packet), delta)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}
