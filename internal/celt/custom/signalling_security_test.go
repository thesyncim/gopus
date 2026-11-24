//go:build gopus_custom_modes

package custom

import "testing"

func TestParseCustomSignallingRejectsTruncatedPaddingRun(t *testing.T) {
	mode, err := NewMode(48000, 960)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	// Header with code=3, padding bit set, followed by 0xff run exceeding packet length.
	pkt := []byte{0xff, 0x41, 0xff, 0xff, 0x01, 0x00}
	if _, err := parseCustomSignallingPacket(mode, pkt, 960); err != ErrInvalidPacket {
		t.Fatalf("parseCustomSignallingPacket err = %v, want ErrInvalidPacket", err)
	}
}
