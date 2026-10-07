package gopus

import (
	"bytes"
	"testing"
)

func TestRepacketizerResetClearsOwnedPacketReferences(t *testing.T) {
	rp := NewRepacketizer()
	packetWithPadding := []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}
	if err := rp.Cat(packetWithPadding); err != nil {
		t.Fatalf("Cat(packet with padding): %v", err)
	}
	if len(rp.frames) != 1 || rp.frames[0] == nil {
		t.Fatalf("Cat retained frames=%d, want one owned frame", len(rp.frames))
	}
	if len(rp.paddings) != 1 || len(rp.paddings[0]) == 0 {
		t.Fatalf("Cat retained paddings=%d, want one non-empty owned padding block", len(rp.paddings))
	}

	rp.Reset()
	if rp.NumFrames() != 0 {
		t.Fatalf("NumFrames after Reset=%d, want 0", rp.NumFrames())
	}
	for i, frame := range rp.frames[:cap(rp.frames)] {
		if frame != nil {
			t.Fatalf("Reset retained frame reference at slot %d", i)
		}
	}
	for i, padding := range rp.paddings[:cap(rp.paddings)] {
		if padding != nil {
			t.Fatalf("Reset retained padding reference at slot %d", i)
		}
	}

	packetAfterReset := []byte{0x48, 0x44, 0x55, 0x66}
	if err := rp.Cat(packetAfterReset); err != nil {
		t.Fatalf("Cat after Reset: %v", err)
	}
	out := make([]byte, len(packetAfterReset))
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("Out after Reset and Cat: %v", err)
	}
	if got := out[:n]; !bytes.Equal(got, packetAfterReset) {
		t.Fatalf("Out after Reset=%x, want %x", got, packetAfterReset)
	}
}

func TestRepacketizerCatSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		wantAllocs float64
	}{
		{name: "empty frame", packet: []byte{0x48}, wantAllocs: 0},
		{name: "owned frame", packet: []byte{0x48, 0x44, 0x55, 0x66}, wantAllocs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet))
			run := func() {
				if err := rp.Cat(tt.packet); err != nil {
					panic(err)
				}
				if _, err := rp.Out(out); err != nil {
					panic(err)
				}
				rp.Reset()
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("Cat/Out/Reset allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
			}
		})
	}
}

func BenchmarkRepacketizerResetCatOut(b *testing.B) {
	for _, tt := range []struct {
		name   string
		packet []byte
	}{
		{name: "code0", packet: []byte{0x48, 0x44, 0x55, 0x66}},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := rp.Cat(tt.packet); err != nil {
					b.Fatal(err)
				}
				if _, err := rp.Out(out); err != nil {
					b.Fatal(err)
				}
				rp.Reset()
			}
		})
	}
}
