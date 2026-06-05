package main

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestMergeAndSplit checks the repacketizer round trip: four 1-frame packets
// merge into one 4-frame packet, which splits back into two 2-frame packets.
func TestMergeAndSplit(t *testing.T) {
	packets, err := encodeFrames(4)
	if err != nil {
		t.Fatalf("encodeFrames: %v", err)
	}

	merged, err := merge(packets)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	info, err := gopus.ParsePacket(merged)
	if err != nil {
		t.Fatalf("ParsePacket(merged): %v", err)
	}
	if info.FrameCount != 4 {
		t.Fatalf("merged frame count = %d, want 4", info.FrameCount)
	}

	first, second, err := splitInHalf(merged)
	if err != nil {
		t.Fatalf("splitInHalf: %v", err)
	}
	if frameCount(first) != 2 || frameCount(second) != 2 {
		t.Fatalf("split frame counts = %d, %d, want 2, 2", frameCount(first), frameCount(second))
	}
}

// TestPadUnpadRoundTrips verifies PacketPad grows the packet and PacketUnpad
// restores the original length.
func TestPadUnpadRoundTrips(t *testing.T) {
	packets, err := encodeFrames(1)
	if err != nil {
		t.Fatalf("encodeFrames: %v", err)
	}
	orig := packets[0]

	padded, err := padTo(orig, len(orig)+16)
	if err != nil {
		t.Fatalf("padTo: %v", err)
	}
	if len(padded) != len(orig)+16 {
		t.Fatalf("padded length = %d, want %d", len(padded), len(orig)+16)
	}

	unpaddedLen, err := gopus.PacketUnpad(padded, len(padded))
	if err != nil {
		t.Fatalf("PacketUnpad: %v", err)
	}
	if unpaddedLen != len(orig) {
		t.Fatalf("unpadded length = %d, want %d", unpaddedLen, len(orig))
	}
}

// TestMergedDecodes confirms the merged multi-frame packet decodes to the full
// concatenated duration.
func TestMergedDecodes(t *testing.T) {
	packets, err := encodeFrames(4)
	if err != nil {
		t.Fatalf("encodeFrames: %v", err)
	}
	merged, err := merge(packets)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	samples, err := decodeAll(merged)
	if err != nil {
		t.Fatalf("decodeAll: %v", err)
	}
	if want := 4 * frameSize; samples != want {
		t.Fatalf("decoded samples = %d, want %d", samples, want)
	}
}
