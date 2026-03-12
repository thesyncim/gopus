package gopus

import "testing"

func newMonoTestDecoder(t *testing.T) *Decoder {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	return dec
}

func decodeMinimalHybrid20ms(t *testing.T, dec *Decoder) int {
	t.Helper()

	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	return n
}
