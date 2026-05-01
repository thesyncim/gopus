//go:build gopus_qext
// +build gopus_qext

package encoder

import "testing"

func TestEncoderSetQEXTGrowsScratchPacketWhenArmed(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := len(enc.scratchPacket); got != defaultScratchPacketBytes {
		t.Fatalf("fresh scratchPacket len=%d want %d", got, defaultScratchPacketBytes)
	}

	enc.SetQEXT(true)
	if got := len(enc.scratchPacket); got != extensionScratchPacketBytes {
		t.Fatalf("armed QEXT scratchPacket len=%d want %d", got, extensionScratchPacketBytes)
	}
}
