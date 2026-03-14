package gopus

import (
	"math"
	"testing"
)

type nopPacketSink struct{}

func (nopPacketSink) WritePacket(packet []byte) (int, error) {
	return len(packet), nil
}

func testCELTPacket() []byte {
	packet := make([]byte, 50)
	packet[0] = 0xF8 // config=31 (CELT FB 20ms), mono, code 0
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i * 7)
	}
	return packet
}

func testSineFrame(samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return pcm
}

func TestHotPathAllocsEncodeFloat32(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := testSineFrame(960)
	packet := make([]byte, 4000)

	for i := 0; i < 5; i++ {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("warmup Encode: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Encode(float32) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsEncodeInt16(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	pcm := make([]int16, 960)
	packet := make([]byte, 4000)

	for i := 0; i < 5; i++ {
		if _, err := enc.EncodeInt16(pcm, packet); err != nil {
			t.Fatalf("warmup EncodeInt16: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := enc.EncodeInt16(pcm, packet); err != nil {
			t.Fatalf("EncodeInt16: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Encode(int16) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsDecodeFloat32(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]float32, 960)

	if _, err := dec.Decode(packet, pcm); err != nil {
		t.Fatalf("warmup Decode: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.Decode(packet, pcm); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(float32) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsDecodeInt16(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	packet := testCELTPacket()
	pcm := make([]int16, 960)

	if _, err := dec.DecodeInt16(packet, pcm); err != nil {
		t.Fatalf("warmup DecodeInt16: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := dec.DecodeInt16(packet, pcm); err != nil {
			t.Fatalf("DecodeInt16: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Decode(int16) allocs/op = %.2f, want 0", allocs)
	}
}

func TestHotPathAllocsStreamWriterFloat32(t *testing.T) {
	writer, err := NewWriter(48000, 2, nopPacketSink{}, FormatFloat32LE, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	pcmBytes := generateFloat32Bytes(48000, 2, 960, 440.0)

	for i := 0; i < 5; i++ {
		if _, err := writer.Write(pcmBytes); err != nil {
			t.Fatalf("warmup Write: %v", err)
		}
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := writer.Write(pcmBytes); err != nil {
			t.Fatalf("Write: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("stream Writer.Write allocs/op = %.2f, want 0", allocs)
	}
}
