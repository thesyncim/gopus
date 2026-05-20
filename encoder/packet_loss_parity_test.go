package encoder

import "testing"

func TestPacketLossPropagatesToLazyCELTEncoder(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetPacketLoss(37)
	if enc.celtEncoder != nil {
		t.Fatal("new encoder should not allocate CELT before use")
	}

	enc.ensureCELTEncoder()
	if got := enc.celtEncoder.PacketLoss(); got != 37 {
		t.Fatalf("CELT packet loss=%d want=37", got)
	}
}

func TestPacketLossRefreshesOnEnsureCELTEncoder(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.ensureCELTEncoder()

	enc.packetLoss = 23
	enc.ensureCELTEncoder()
	if got := enc.celtEncoder.PacketLoss(); got != 23 {
		t.Fatalf("CELT packet loss=%d want=23", got)
	}
}
