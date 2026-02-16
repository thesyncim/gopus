package gopus

import (
	"bytes"
	"errors"
	"testing"
)

func TestMultistreamPacketPadUnpadSelfDelimitedRoundTrip(t *testing.T) {
	stream0 := []byte{GenerateTOC(31, false, 0), 0x11, 0x22, 0x33, 0x44}
	stream1 := []byte{GenerateTOC(30, false, 0), 0x55, 0x66, 0x77}

	self0, err := makeSelfDelimitedPacket(stream0)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream0): %v", err)
	}

	orig := append(append([]byte{}, self0...), stream1...)
	newLen := len(orig) + 17

	buf := make([]byte, newLen)
	copy(buf, orig)

	if err := MultistreamPacketPad(buf, len(orig), newLen, 2); err != nil {
		t.Fatalf("MultistreamPacketPad: %v", err)
	}

	decoded0, consumed0, err := decodeSelfDelimitedPacket(buf[:newLen])
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(padded): %v", err)
	}
	if !bytes.Equal(decoded0, stream0) {
		t.Fatalf("stream0 changed after pad: got=%v want=%v", decoded0, stream0)
	}
	if consumed0 >= newLen {
		t.Fatalf("invalid consumed0=%d for newLen=%d", consumed0, newLen)
	}
	if _, err := ParsePacket(buf[consumed0:newLen]); err != nil {
		t.Fatalf("last stream parse after pad: %v", err)
	}

	unpaddedLen, err := MultistreamPacketUnpad(buf, newLen, 2)
	if err != nil {
		t.Fatalf("MultistreamPacketUnpad: %v", err)
	}
	if unpaddedLen != len(orig) {
		t.Fatalf("unpaddedLen=%d want=%d", unpaddedLen, len(orig))
	}
	if !bytes.Equal(buf[:unpaddedLen], orig) {
		t.Fatalf("round-trip mismatch: got=%v want=%v", buf[:unpaddedLen], orig)
	}
}

func TestMultistreamPacketPadUnpadThreeStreamsRoundTrip(t *testing.T) {
	stream0 := []byte{GenerateTOC(31, false, 0), 0x10, 0x11, 0x12}
	stream1 := []byte{GenerateTOC(31, false, 2), 0x02, 0x21, 0x22, 0x23}
	stream2 := []byte{GenerateTOC(29, false, 0), 0x31, 0x32, 0x33, 0x34}

	self0, err := makeSelfDelimitedPacket(stream0)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream0): %v", err)
	}
	self1, err := makeSelfDelimitedPacket(stream1)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(stream1): %v", err)
	}

	orig := append(append(append([]byte{}, self0...), self1...), stream2...)
	newLen := len(orig) + 9

	buf := make([]byte, newLen)
	copy(buf, orig)

	if err := MultistreamPacketPad(buf, len(orig), newLen, 3); err != nil {
		t.Fatalf("MultistreamPacketPad: %v", err)
	}

	unpaddedLen, err := MultistreamPacketUnpad(buf, newLen, 3)
	if err != nil {
		t.Fatalf("MultistreamPacketUnpad: %v", err)
	}
	if unpaddedLen != len(orig) {
		t.Fatalf("unpaddedLen=%d want=%d", unpaddedLen, len(orig))
	}
	if !bytes.Equal(buf[:unpaddedLen], orig) {
		t.Fatalf("round-trip mismatch: got=%v want=%v", buf[:unpaddedLen], orig)
	}
}

func TestMultistreamPacketPadRejectsInvalidSelfDelimited(t *testing.T) {
	// stream0 claims a large self-delimited frame size but doesn't provide it.
	invalid := []byte{GenerateTOC(31, false, 0), 10, 0xAA, GenerateTOC(31, false, 0), 0xBB}
	buf := make([]byte, len(invalid)+8)
	copy(buf, invalid)

	err := MultistreamPacketPad(buf, len(invalid), len(invalid)+8, 2)
	if !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("MultistreamPacketPad err=%v want=%v", err, ErrPacketTooShort)
	}
}

func TestMultistreamPacketUnpadRejectsInvalidSelfDelimited(t *testing.T) {
	invalid := []byte{GenerateTOC(31, false, 0), 10, 0xAA, GenerateTOC(31, false, 0), 0xBB}
	_, err := MultistreamPacketUnpad(invalid, len(invalid), 2)
	if !errors.Is(err, ErrPacketTooShort) {
		t.Fatalf("MultistreamPacketUnpad err=%v want=%v", err, ErrPacketTooShort)
	}
}
