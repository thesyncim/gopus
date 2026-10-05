package ogg

import (
	"bytes"
	"testing"
)

func TestPagePacketsStopsAtTruncatedPayload(t *testing.T) {
	for _, tc := range []struct {
		name     string
		segments []byte
		payload  []byte
		want     [][]byte
	}{
		{name: "first packet truncated", segments: []byte{3, 2, 1}, payload: []byte{0x11, 0x12}, want: [][]byte{{0x11, 0x12}}},
		{name: "middle packet truncated", segments: []byte{2, 3, 1}, payload: []byte{0x11, 0x12, 0x21}, want: [][]byte{{0x11, 0x12}, {0x21}}},
		{name: "missing packet bytes", segments: []byte{2, 3, 1}, payload: []byte{0x11, 0x12}, want: [][]byte{{0x11, 0x12}, {}}},
		{name: "valid empty packet", segments: []byte{2, 0, 1}, payload: []byte{0x11, 0x12, 0x21}, want: [][]byte{{0x11, 0x12}, {}, {0x21}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := Page{Segments: tc.segments, Payload: tc.payload}
			got := page.Packets()
			if len(got) != len(tc.want) {
				t.Fatalf("Packets returns %d packets, want %d", len(got), len(tc.want))
			}
			for i, want := range tc.want {
				if !bytes.Equal(got[i], want) {
					t.Fatalf("packet %d = %x, want %x", i, got[i], want)
				}
			}
		})
	}
}
