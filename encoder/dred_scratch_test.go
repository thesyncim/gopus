//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import "testing"

func TestEncoderSetDREDDurationGrowsScratchPacketWhenArmed(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := len(enc.scratchPacket); got != defaultScratchPacketBytes {
		t.Fatalf("fresh scratchPacket len=%d want %d", got, defaultScratchPacketBytes)
	}

	if err := enc.SetDREDDuration(4); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	if got := len(enc.scratchPacket); got != extensionScratchPacketBytes {
		t.Fatalf("armed DRED scratchPacket len=%d want %d", got, extensionScratchPacketBytes)
	}
}
