package gopus

import (
	"math"
	"math/rand"
	"testing"
)

func TestValidatePacketFramingMatchesParsePacket(t *testing.T) {
	packets := [][]byte{
		nil,
		{0}, {0, 1},
		{1}, {1, 1}, {1, 1, 2},
		{2}, {2, 0}, {2, 2, 1}, {2, 252}, {2, 252, 0},
		{3}, {3, 0}, {3, 1}, {3, 2, 1, 2},
		{3, 0x41}, {3, 0x41, 2, 1, 2},
		{3, 0x82}, {3, 0x82, 1}, {3, 0x82, 1, 1, 2},
		{3, 0xc2}, {3, 0xc2, 0, 1, 1, 2, 3},
		{3, 0x3f},
		{0x83, 0x30}, // 48 CELT 2.5 ms frames = 120 ms.
		{3, 0x41, 255},
	}
	chainedPadding := make([]byte, 2+2+255)
	chainedPadding[0], chainedPadding[1] = 3, 0x41
	chainedPadding[2], chainedPadding[3] = 255, 1
	packets = append(packets, chainedPadding)
	for _, n := range []int{1275, 1276, 2550, 2551} {
		for code := byte(0); code < 4; code++ {
			packet := make([]byte, n+1)
			packet[0] = code
			packets = append(packets, packet)
		}
	}
	rng := rand.New(rand.NewSource(6716))
	for i := range 2000 {
		packet := make([]byte, rng.Intn(80))
		for j := range packet {
			packet[j] = byte(rng.Intn(256))
		}
		if len(packet) != 0 {
			packet[0] = packet[0]&^3 | byte(i&3)
		}
		packets = append(packets, packet)
	}
	for i, packet := range packets {
		_, want := ParsePacket(packet)
		if got := validatePacketFraming(packet); got != want {
			t.Fatalf("packet %d (%x): validator error=%v, parser error=%v", i, packet, got, want)
		}
	}
	for _, packet := range [][]byte{{0, 1}, {1, 1, 2}, {3, 0x82, 1, 1, 2}, {3, 0}, {2, 252}} {
		if allocs := testing.AllocsPerRun(100, func() { _ = validatePacketFraming(packet) }); allocs != 0 {
			t.Fatalf("validator allocations for %x: %g", packet, allocs)
		}
	}
}

func TestDecodeWithFECMalformedPacketPreservesState(t *testing.T) {
	seed := encodeAPIRateCELTPacket(t, 1)
	for _, packet := range [][]byte{{1, 1}, {2}, {2, 252}, {3, 0}, {3, 0x82}, {3, 0x41, 255}} {
		_, wantErr := ParsePacket(packet)
		if wantErr == nil {
			t.Fatalf("packet %x unexpectedly valid", packet)
		}
		dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := NewDecoder(DefaultDecoderConfig(48000, 1))
		if err != nil {
			t.Fatal(err)
		}
		got := make([]float32, 960)
		want := make([]float32, 960)
		if n, err := dec.DecodeWithFEC(packet, got, true); n != 0 || err != wantErr {
			t.Fatalf("packet %x FEC decode=(%d,%v), want (0,%v)", packet, n, err, wantErr)
		}
		gotN, gotErr := dec.Decode(seed, got)
		wantN, freshErr := fresh.Decode(seed, want)
		if gotErr != nil || freshErr != nil || gotN != wantN || dec.FinalRange() != fresh.FinalRange() {
			t.Fatalf("packet %x changed decoder state: recovered=(%d,%v,%08x) fresh=(%d,%v,%08x)",
				packet, gotN, gotErr, dec.FinalRange(), wantN, freshErr, fresh.FinalRange())
		}
		for i := range got[:gotN] {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("packet %x changed decoder state at sample %d", packet, i)
			}
		}
	}
}
