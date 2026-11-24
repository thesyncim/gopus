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
	if rp.NumFrames() != 1 || rp.frames[0].len == 0 {
		t.Fatalf("Cat retained frames=%d, want one owned frame", rp.NumFrames())
	}
	if rp.paddings[0].len == 0 || !rp.hasPad {
		t.Fatalf("Cat retained paddings=%d, want one non-empty owned padding block", rp.paddings[0].len)
	}

	rp.Reset()
	if rp.NumFrames() != 0 {
		t.Fatalf("NumFrames after Reset=%d, want 0", rp.NumFrames())
	}
	if len(rp.frameBuf) != 0 {
		t.Fatalf("Reset retained frameBuf len=%d, want 0", len(rp.frameBuf))
	}
	if len(rp.padBuf) != 0 {
		t.Fatalf("Reset retained padBuf len=%d, want 0", len(rp.padBuf))
	}
	if rp.hasPad {
		t.Fatalf("Reset retained hasPad=true, want false")
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
		{name: "owned frame", packet: []byte{0x48, 0x44, 0x55, 0x66}, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, wantAllocs: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
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

func TestPacketPadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		newLen     int
		wantAllocs float64
	}{
		{name: "plain", packet: []byte{0x48, 0x11, 0x22, 0x33}, newLen: 16, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, newLen: 20, wantAllocs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.newLen)
			run := func() {
				copy(buf, tt.packet)
				if err := PacketPad(buf, len(tt.packet), tt.newLen); err != nil {
					panic(err)
				}
				if _, err := PacketUnpad(buf, tt.newLen); err != nil {
					panic(err)
				}
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("PacketPad/PacketUnpad allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
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
