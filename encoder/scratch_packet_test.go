package encoder

import "testing"

func TestEncoderScratchPacketStartsAtCorePacketCap(t *testing.T) {
	enc := NewEncoder(48000, 1)
	if got := len(enc.scratchPacket); got != defaultScratchPacketBytes {
		t.Fatalf("scratchPacket len=%d want %d", got, defaultScratchPacketBytes)
	}
}
