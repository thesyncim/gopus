//go:build gopus_dred || gopus_osce

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/dred"
)

func addDREDPayloadFuzzSeed(f *testing.F, extensions []packetExtensionData) {
	frames := [][]byte{{0x11, 0x22, 0x33}, {0x44, 0x55}}
	packet := make([]byte, 128)
	n, err := buildPacketWithOptions(GenerateTOC(31, false, 0)&0xFC, frames, packet, 0, false, extensions, false)
	if err == nil {
		f.Add(append([]byte(nil), packet[:n]...))
	}
}

func FuzzFindDREDPayload_NoPanic(f *testing.F) {
	validPayload := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 1, Data: append([]byte{'D', dred.ExperimentalVersion}, validPayload...)},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0xaa}},
	})
	addDREDPayloadFuzzSeed(f, []packetExtensionData{
		{ID: dred.ExtensionID, Frame: 0, Data: []byte{'X', dred.ExperimentalVersion, 0x10}},
		{ID: dred.ExtensionID, Frame: 0, Data: append([]byte{'D', dred.ExperimentalVersion}, validPayload...)},
	})
	f.Add([]byte{0x7c, 0xc1, 0x03, 0x02, 0xff})
	f.Add([]byte{0xff, 0xff, 0x00, 'D', dred.ExperimentalVersion})
	f.Add([]byte{GenerateTOC(31, false, 3), 0xc1, 0x02, 0x01})

	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > 4096 {
			packet = packet[:4096]
		}
		payload, frameOffset, ok, err := findDREDPayload(packet)
		if err != nil {
			return
		}
		if !ok {
			if payload != nil || frameOffset != 0 {
				t.Fatalf("findDREDPayload returned stale data with ok=false: payload=%x frameOffset=%d", payload, frameOffset)
			}
			return
		}
		if frameOffset < 0 {
			t.Fatalf("negative DRED frame offset: %d", frameOffset)
		}
		if len(payload) > len(packet) {
			t.Fatalf("DRED payload length=%d exceeds packet length=%d", len(payload), len(packet))
		}
	})
}
