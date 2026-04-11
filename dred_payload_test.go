package gopus

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/dred"
)

func TestFindDREDPayload(t *testing.T) {
	frames := [][]byte{
		{0x11, 0x22, 0x33},
		{0x44, 0x55, 0x66},
	}
	wantPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xf0, 0x12, 0x34}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 1, Data: append([]byte{'D', dred.ExperimentalVersion}, wantPayload...)},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if !ok {
		t.Fatal("findDREDPayload ok=false want true")
	}
	if frameOffset != 8 {
		t.Fatalf("frameOffset=%d want 8", frameOffset)
	}
	if !bytes.Equal(payload, wantPayload) {
		t.Fatalf("payload=%x want %x", payload, wantPayload)
	}
}

func TestFindDREDPayloadSkipsWrongExperimentalHeader(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0xaa}},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if ok || payload != nil || frameOffset != 0 {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (nil,0,false)", payload, frameOffset, ok)
	}
}

func TestFindDREDPayloadSkipsTooShortExperimentalPayload(t *testing.T) {
	frames := [][]byte{{0x11, 0x22, 0x33}}
	extensions := []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'D', dred.ExperimentalVersion, 0xaa, 0xbb}},
	}
	packet := make([]byte, 64)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions error: %v", err)
	}

	payload, frameOffset, ok, err := findDREDPayload(packet[:n])
	if err != nil {
		t.Fatalf("findDREDPayload error: %v", err)
	}
	if ok || payload != nil || frameOffset != 0 {
		t.Fatalf("findDREDPayload=(%x,%d,%v) want (nil,0,false)", payload, frameOffset, ok)
	}
}
