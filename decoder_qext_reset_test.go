//go:build gopus_qext

package gopus

import "testing"

func TestDecoderReset96kLastFrameSize(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(96000, 2))
	if err != nil {
		t.Fatalf("NewDecoder(96000): %v", err)
	}
	dec.Reset()
	if dec.lastFrameSize != 1920 {
		t.Fatalf("lastFrameSize after Reset() = %d, want 1920", dec.lastFrameSize)
	}
}
