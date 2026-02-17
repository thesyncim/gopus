package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/types"
)

func TestBuildDTXPacketSILK(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
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

func TestBuildDTXPacketCELT(t *testing.T) {
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
	}

	toc := packet[0]
	config := toc >> 3
	if int(config) >= len(configTable) {
		t.Fatalf("invalid config=%d", config)
	}
	if configTable[config].Mode != types.ModeCELT {
		t.Fatalf("TOC mode = %v, want %v", configTable[config].Mode, types.ModeCELT)
	}
}

func TestBuildDTXPacketStereo(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)

	packet, err := enc.buildDTXPacket(960)
	if err != nil {
		t.Fatalf("buildDTXPacket failed: %v", err)
	}
	if len(packet) != 1 {
		t.Fatalf("DTX packet should be 1 byte (TOC only), got %d", len(packet))
	}

	toc := packet[0]
	stereo := (toc & 0x04) != 0
	if !stereo {
		t.Fatal("stereo bit should be set for 2-channel encoder")
	}
}
