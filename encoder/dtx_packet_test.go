package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestEncodeFrameBuildsPacketForSILK(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	packet, err := enc.encodeFrame(make([]float64, 960), 960)
	if err != nil {
		t.Fatalf("encodeFrame failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("encodeFrame returned empty packet")
	}

	toc := packet[0]
	config := toc >> 3
	frameCode := toc & 0x03
	if frameCode != 0 {
		t.Fatalf("frameCode = %d, want 0", frameCode)
	}
	if int(config) >= len(configTable) {
		t.Fatalf("invalid config=%d", config)
	}
	if configTable[config].Mode != types.ModeSILK {
		t.Fatalf("TOC mode = %v, want %v", configTable[config].Mode, types.ModeSILK)
	}
}

func TestEncodeFrameBuildsPacketForLongCELT(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	packet, err := enc.encodeFrame(make([]float64, 1920), 1920)
	if err != nil {
		t.Fatalf("encodeFrame failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("encodeFrame returned empty packet")
	}

	toc := packet[0]
	config := toc >> 3
	frameCode := toc & 0x03
	if frameCode != 3 {
		t.Fatalf("frameCode = %d, want 3", frameCode)
	}
	if int(config) >= len(configTable) {
		t.Fatalf("invalid config=%d", config)
	}
	if configTable[config].Mode != types.ModeCELT {
		t.Fatalf("TOC mode = %v, want %v", configTable[config].Mode, types.ModeCELT)
	}
	if len(packet) < 2 {
		t.Fatalf("short packet: %d", len(packet))
	}
	if frameCount := int(packet[1] & 0x3F); frameCount != 2 {
		t.Fatalf("frameCount = %d, want 2", frameCount)
	}
}
