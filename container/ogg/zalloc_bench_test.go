package ogg

import (
	"bytes"
	"io"
	"testing"
)

// TestWriterZeroAlloc locks the steady-state allocation-free contract for the
// writer: once the embedded page buffer is warm, WritePacket allocates nothing.
func TestWriterZeroAlloc(t *testing.T) {
	w, err := NewWriter(io.Discard, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	pkt := []byte{0x78, 0x01, 0x02, 0x03, 0x04, 0x05}
	// Warm up (header pages + first audio page), then measure.
	if err := w.WritePacket(pkt, 960); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(200, func() {
		_ = w.WritePacket(pkt, 960)
	}); n != 0 {
		t.Errorf("WritePacket allocs/op = %v, want 0", n)
	}
}

func BenchmarkWritePacket(b *testing.B) {
	w, err := NewWriter(io.Discard, 48000, 2)
	if err != nil {
		b.Fatal(err)
	}
	pkt := []byte{0x78, 0x01, 0x02, 0x03, 0x04, 0x05}
	_ = w.WritePacket(pkt, 960) // warm
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.WritePacket(pkt, 960); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReadStream(b *testing.B) {
	stream := buildValidOpusStream(2, 50)
	dst := make([]byte, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := NewReader(bytes.NewReader(stream))
		if err != nil {
			b.Fatal(err)
		}
		for {
			_, _, err := r.ReadPacketInto(dst)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkWriteStream(b *testing.B) {
	pkt := []byte{0x78, 0x01, 0x02, 0x03, 0x04, 0x05}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, err := NewWriter(io.Discard, 48000, 2)
		if err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 50; j++ {
			if err := w.WritePacket(pkt, 960); err != nil {
				b.Fatal(err)
			}
		}
		_ = w.Close()
	}
}
