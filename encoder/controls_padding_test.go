package encoder

import (
	"bytes"
	"testing"
)

func TestPadToSize_RepacketizeCode0ToCode3NoPadding(t *testing.T) {
	frame := bytes.Repeat([]byte{0x7a}, 78)
	packet := append([]byte{0x48}, frame...) // code-0, 1 frame

	got := padToSize(packet, len(packet)+1)
	if len(got) != len(packet)+1 {
		t.Fatalf("len=%d want=%d", len(got), len(packet)+1)
	}
	if got[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want=3", got[0]&0x03)
	}
	if got[1] != 0x01 { // M=1, CBR, no padding
		t.Fatalf("count byte=0x%02x want=0x01", got[1])
	}
	if !bytes.Equal(got[2:], frame) {
		t.Fatalf("frame payload mismatch after repacketize")
	}
}

func TestPadToSize_RepacketizeCode1ToCode3NoPadding(t *testing.T) {
	frameA := bytes.Repeat([]byte{0x11}, 10)
	frameB := bytes.Repeat([]byte{0x22}, 10)
	packet := append([]byte{0x49}, append(frameA, frameB...)...) // code-1, 2 CBR frames

	got := padToSize(packet, len(packet)+1)
	if len(got) != len(packet)+1 {
		t.Fatalf("len=%d want=%d", len(got), len(packet)+1)
	}
	if got[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want=3", got[0]&0x03)
	}
	if got[1] != 0x02 { // M=2, CBR, no padding
		t.Fatalf("count byte=0x%02x want=0x02", got[1])
	}
	if !bytes.Equal(got[2:12], frameA) {
		t.Fatalf("frameA mismatch after repacketize")
	}
	if !bytes.Equal(got[12:22], frameB) {
		t.Fatalf("frameB mismatch after repacketize")
	}
}

func TestPadToSize_Code3PaddingUsesTotalPadAmount(t *testing.T) {
	frame := bytes.Repeat([]byte{0x5a}, 78)
	packet := append([]byte{0x48}, frame...) // code-0, 1 frame

	// code-3 base is +1 byte, so +3 total means pad_amount=2.
	got := padToSize(packet, len(packet)+3)
	if len(got) != len(packet)+3 {
		t.Fatalf("len=%d want=%d", len(got), len(packet)+3)
	}
	if got[0]&0x03 != 0x03 {
		t.Fatalf("toc code=%d want=3", got[0]&0x03)
	}
	if got[1] != 0x41 { // M=1 + padding flag
		t.Fatalf("count byte=0x%02x want=0x41", got[1])
	}
	if got[2] != 0x01 { // pad_amount(2) -> final pad byte 1
		t.Fatalf("pad byte=0x%02x want=0x01", got[2])
	}
	if !bytes.Equal(got[3:81], frame) {
		t.Fatalf("frame payload mismatch with padding")
	}
	if got[81] != 0x00 {
		t.Fatalf("expected trailing zero padding byte")
	}
}

