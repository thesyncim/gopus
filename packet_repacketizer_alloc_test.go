package gopus

import (
	"bytes"
	"testing"
)

func TestPacketPadUnpadCallerOwnedCapacity(t *testing.T) {
	packet := []byte{0x48, 0x11, 0x22, 0x33}
	storage := bytes.Repeat([]byte{0xa5}, 32)
	copy(storage, packet)
	input := storage[:len(packet)]
	if err := PacketPad(input, len(packet), 24); err != nil {
		t.Fatalf("PacketPad with spare capacity: %v", err)
	}
	if len(input) != len(packet) {
		t.Fatalf("PacketPad changed the caller slice length to %d, want %d", len(input), len(packet))
	}
	if info, err := ParsePacket(storage[:24]); err != nil || info.TotalSize != 24 {
		t.Fatalf("ParsePacket(padded)=(%+v,%v), want a valid 24-byte packet", info, err)
	}
	if !bytes.Equal(storage[24:], bytes.Repeat([]byte{0xa5}, len(storage)-24)) {
		t.Fatalf("PacketPad wrote past newLen: tail=%x", storage[24:])
	}

	n, err := PacketUnpad(storage[:24], 24)
	if err != nil {
		t.Fatalf("PacketUnpad with spare capacity: %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("PacketUnpad=%x, want %x", got, packet)
	}
}

func TestPacketUnpadCanonicalizesAndDropsOpaquePadding(t *testing.T) {
	t.Run("noncanonical framing", func(t *testing.T) {
		packet := code3VBRPacket(18, false, []int{1, 1}, 7)
		want := append([]byte{GenerateTOC(18, false, 1)}, packet[len(packet)-2:]...)
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want canonical code-1 packet %x", got, want)
		}
	})

	t.Run("malformed opaque extension", func(t *testing.T) {
		packet := mustDecodeHex(t, "4b4102112233ffff")
		want := []byte{0x48, 0x11, 0x22, 0x33}
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want %x", got, want)
		}
	})

	t.Run("truncated padding length", func(t *testing.T) {
		packet := []byte{0x4b, 0x41, 0xff}
		before := append([]byte(nil), packet...)
		if _, err := PacketUnpad(packet, len(packet)); err != ErrPacketTooShort {
			t.Fatalf("PacketUnpad error=%v, want %v", err, ErrPacketTooShort)
		}
		if !bytes.Equal(packet, before) {
			t.Fatalf("PacketUnpad changed malformed input: got %x want %x", packet, before)
		}
	})
}

func TestPacketPadUnpad48FrameVBR(t *testing.T) {
	packet := code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
	newLen := len(packet) + 43
	storage := make([]byte, len(packet), newLen)
	copy(storage, packet)
	if err := PacketPad(storage, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(48-frame VBR): %v", err)
	}
	n, err := PacketUnpad(storage[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(48-frame VBR): %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("pad/unpad changed 48-frame VBR packet: got %x want %x", got, packet)
	}
}

func TestPacketPadExtensionFallbackWithShortSliceAndSpareCapacity(t *testing.T) {
	packet := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantPadded := mustDecodeHex(t, "4b410a112233010101010baa50deadbe")
	storage := make([]byte, len(packet), len(wantPadded))
	copy(storage, packet)
	if err := PacketPad(storage[:len(packet)], len(packet), len(wantPadded)); err != nil {
		t.Fatalf("PacketPad(extension packet): %v", err)
	}
	if got := storage[:len(wantPadded)]; !bytes.Equal(got, wantPadded) {
		t.Fatalf("PacketPad(extension packet)=%x, want %x", got, wantPadded)
	}
	n, err := PacketUnpad(storage[:len(wantPadded)], len(wantPadded))
	if err != nil {
		t.Fatalf("PacketUnpad(extension packet): %v", err)
	}
	wantUnpadded := []byte{0x48, 0x11, 0x22, 0x33}
	if got := storage[:n]; !bytes.Equal(got, wantUnpadded) {
		t.Fatalf("PacketUnpad(extension packet)=%x, want %x", got, wantUnpadded)
	}
}

func TestPacketPadLargeExtensionPaddingRoundTrip(t *testing.T) {
	packet := packetWithLargeLongExtension(4096)
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	copy(buf, packet)
	if err := PacketPad(buf, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(large extension): %v", err)
	}
	n, err := PacketUnpad(buf[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(large extension): %v", err)
	}
	want := []byte{GenerateTOC(18, false, 0), 0x11, 0x22, 0x33}
	if got := buf[:n]; !bytes.Equal(got, want) {
		t.Fatalf("PacketUnpad(large extension)=%x, want %x", got, want)
	}
}

func TestPacketPadUnpadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "code 0", data: []byte{0x48, 0x11, 0x22, 0x33}},
		{name: "48-frame VBR", data: makePacket48FrameVBR()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newLen := len(tt.data) + 43
			buf := make([]byte, len(tt.data), newLen)
			pad := func() {
				copy(buf, tt.data)
				if err := PacketPad(buf, len(tt.data), newLen); err != nil {
					panic(err)
				}
			}
			pad()
			if got := testing.AllocsPerRun(100, pad); got != 0 {
				t.Errorf("PacketPad allocs/op=%.2f, want 0", got)
			}

			padded := append([]byte(nil), buf[:newLen]...)
			unpadBuf := make([]byte, newLen)
			unpad := func() {
				copy(unpadBuf, padded)
				if _, err := PacketUnpad(unpadBuf, newLen); err != nil {
					panic(err)
				}
			}
			unpad()
			if got := testing.AllocsPerRun(100, unpad); got != 0 {
				t.Errorf("PacketUnpad allocs/op=%.2f, want 0", got)
			}
		})
	}
}

func makePacket48FrameVBR() []byte {
	return code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
}

func BenchmarkPacketPadCallerCapacity(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketPad48FrameVBR(b *testing.B) {
	benchmarkPacketPad(b, makePacket48FrameVBR())
}

func BenchmarkPacketPadWithExtensions(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe})
}

func BenchmarkPacketPadLargeExtensionPadding(b *testing.B) {
	benchmarkPacketPad(b, packetWithLargeLongExtension(4096))
}

func benchmarkPacketPad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		copy(buf, packet)
		if err := PacketPad(buf, len(packet), newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacketUnpadCallerCapacity(b *testing.B) {
	benchmarkPacketUnpad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketUnpad48FrameVBR(b *testing.B) {
	benchmarkPacketUnpad(b, makePacket48FrameVBR())
}

func benchmarkPacketUnpad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	padded := make([]byte, newLen)
	copy(padded, packet)
	if err := PacketPad(padded, len(packet), newLen); err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, newLen)
	b.ReportAllocs()
	b.SetBytes(int64(newLen))
	b.ResetTimer()
	for range b.N {
		copy(buf, padded)
		if _, err := PacketUnpad(buf, newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParsePacket48FrameVBR(b *testing.B) {
	packet := makePacket48FrameVBR()
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		if _, err := ParsePacket(packet); err != nil {
			b.Fatal(err)
		}
	}
}
