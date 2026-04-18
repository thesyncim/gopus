package gopus

import "testing"

func mustNewTestEncoder(t *testing.T, sampleRate, channels int, application Application) *Encoder {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: application})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	return enc
}

func mustNewTestDecoder(t *testing.T, sampleRate, channels int) *Decoder {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	return dec
}

func mustNewDefaultMultistreamEncoder(t *testing.T, sampleRate, channels int, application Application) *MultistreamEncoder {
	t.Helper()

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, application)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}
	return enc
}

func mustNewDefaultMultistreamDecoder(t *testing.T, sampleRate, channels int) *MultistreamDecoder {
	t.Helper()

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}
	return dec
}

func newMonoTestDecoder(t *testing.T) *Decoder {
	t.Helper()

	return mustNewTestDecoder(t, 48000, 1)
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
