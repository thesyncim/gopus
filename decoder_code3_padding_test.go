package gopus

import (
	"errors"
	"testing"
)

func TestDecodeCode3OverflowPaddingMatchesParsePacket(t *testing.T) {
	// Code-3 packet with padding flag (0x40), 1 frame, and two 0xFF padding
	// length bytes (254+254 > len(data)).
	malformed := []byte{0x03, 0x41, 0xFF, 0xFF}
	if _, err := ParsePacket(malformed); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("ParsePacket err = %v, want ErrInvalidPacket", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 960)
	if _, err := dec.Decode(malformed, out); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Decode err = %v, want ErrInvalidPacket", err)
	}
}
