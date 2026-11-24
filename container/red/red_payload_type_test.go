package red

import (
	"bytes"
	"testing"
)

func TestBuildAppendMasksPrimaryPayloadType(t *testing.T) {
	primary := []byte{0xFC, 0xAA, 0xBB}
	packet, _ := BuildAppend(nil, primary, 1920, nil, 1, 960, 0x80|111)
	if len(packet) == 0 || packet[0] != 111 {
		t.Fatalf("header byte = %#x, want 0x6f", packet[0])
	}
	gotPrimary, redundant, err := Parse(packet, 111)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(redundant) != 0 || !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("unexpected parse result: redundant=%d primary=%x", len(redundant), gotPrimary)
	}
}
