//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package gopus

import (
	"bytes"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
)

// TestProbeEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo is a
// focused parity probe for single-stream stereo CELT FB DRED packets. The
// existing TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms only
// checks DRED payload offset/bytes (mono); this probe explicitly exercises
// channels=2 + ModeCELT + BandwidthFullband at 20ms and additionally surfaces
// any full-packet-length divergence between gopus and libopus.
//
// The probe compares (in order):
//   - full packet length (the headline check)
//   - DRED payload offset
//   - DRED payload bytes
//   - primary CELT frame byte counts
//
// History:
//   - Pre-fix (encoder/encoder.go reserving DRED bytes from SILK/Hybrid only):
//     gopus packet=187 vs libopus packet=107, 80-byte excess from CELT VBR
//     compute_vbr running at the full 40 kbps (libopus runs CELT at 40 kbps
//     minus dred_bitrate_bps per opus_encoder.c line 1338).
//   - Post-fix (reserving DRED bytes from CELT too): gopus undershoots libopus
//     by ~15 bytes for stereo and ~23 bytes for mono. The residual is from
//     gopus's CELT compute_vbr being slightly more conservative than libopus
//     at low (post-DRED) bitrates; this is independent of stereo coupling and
//     is left as a separate alignment task.
//   - CVBR alignment fix: CELT now shrinks VBR packets after current-frame
//     dynalloc/trim and mirrors libopus float compute_vbr tonality/transient
//     math, so this probe is a strict packet-length parity gate.
func TestProbeEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20msStereo(t *testing.T) {
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		t.Skipf("libopus stereo CELT DRED packet helper unavailable: %v", err)
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
