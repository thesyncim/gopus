package gopus

import (
	"encoding/hex"
	"reflect"
	"testing"
)

func mustHexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("DecodeString(%q): %v", s, err)
	}
	return b
}

func TestGeneratePacketExtensionsMatchesLibopusCases(t *testing.T) {
	tests := []struct {
		name       string
		extensions []packetExtensionData
		nbFrames   int
		length     int
		pad        bool
		wantHex    string
	}{
		{
			name: "mixed",
			extensions: []packetExtensionData{
				{ID: 32, Frame: 0, Data: []byte("AB")},
				{ID: 32, Frame: 1, Data: []byte("CD")},
				{ID: 3, Frame: 1},
				{ID: 33, Frame: 2, Data: []byte("xyz")},
			},
			nbFrames: 3,
			length:   15,
			wantHex:  "41024142024102434406024278797a",
		},
		{
			name: "repeat_short",
			extensions: []packetExtensionData{
				{ID: 3, Frame: 0},
				{ID: 3, Frame: 1},
				{ID: 3, Frame: 2},
			},
			nbFrames: 3,
			length:   2,
			wantHex:  "0604",
		},
		{
			name: "repeat_long",
			extensions: []packetExtensionData{
				{ID: 32, Frame: 0, Data: []byte("AB")},
				{ID: 32, Frame: 1, Data: []byte("CD")},
				{ID: 32, Frame: 2, Data: []byte("EF")},
			},
			nbFrames: 3,
			length:   10,
			wantHex:  "41024142040243444546",
		},
		{
			name:       "pad_front",
			extensions: []packetExtensionData{{ID: 3, Frame: 0}},
			nbFrames:   1,
			length:     20,
			pad:        true,
			wantHex:    "0101010101010101010101010101010101010106",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := make([]byte, tc.length)
			n, err := generatePacketExtensions(dst, tc.length, tc.extensions, tc.nbFrames, tc.pad)
			if err != nil {
				t.Fatalf("generatePacketExtensions() error = %v", err)
			}
			if n != tc.length {
				t.Fatalf("generatePacketExtensions() length = %d want %d", n, tc.length)
			}
			if gotHex := hex.EncodeToString(dst[:n]); gotHex != tc.wantHex {
				t.Fatalf("generatePacketExtensions() = %s want %s", gotHex, tc.wantHex)
			}

			sizeOnly, err := generatePacketExtensions(nil, tc.length, tc.extensions, tc.nbFrames, tc.pad)
			if err != nil {
				t.Fatalf("generatePacketExtensions(nil) error = %v", err)
			}
			if sizeOnly != tc.length {
				t.Fatalf("generatePacketExtensions(nil) = %d want %d", sizeOnly, tc.length)
			}
		})
	}
}

func TestPacketExtensionIteratorParseAndCount(t *testing.T) {
	data := mustHexBytes(t, "41024142024102434406024278797a")

	count, err := countPacketExtensions(data, 3)
	if err != nil {
		t.Fatalf("countPacketExtensions() error = %v", err)
	}
	if count != 4 {
		t.Fatalf("countPacketExtensions() = %d want 4", count)
	}

	counts := make([]int, 3)
	count, err = countPacketExtensionsByFrame(data, 3, counts)
	if err != nil {
		t.Fatalf("countPacketExtensionsByFrame() error = %v", err)
	}
	if count != 4 {
		t.Fatalf("countPacketExtensionsByFrame() total = %d want 4", count)
	}
	if !reflect.DeepEqual(counts, []int{1, 2, 1}) {
		t.Fatalf("countPacketExtensionsByFrame() = %v want [1 2 1]", counts)
	}

	got := make([]packetExtensionData, 4)
	n, err := parsePacketExtensions(data, 3, got)
	if err != nil {
		t.Fatalf("parsePacketExtensions() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("parsePacketExtensions() count = %d want 4", n)
	}

	want := []packetExtensionData{
		{ID: 32, Frame: 0, Data: []byte("AB")},
		{ID: 32, Frame: 1, Data: []byte("CD")},
		{ID: 3, Frame: 1, Data: nil},
		{ID: 33, Frame: 2, Data: []byte("xyz")},
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Frame != want[i].Frame || string(got[i].Data) != string(want[i].Data) {
			t.Fatalf("parsePacketExtensions()[%d] = %+v want %+v", i, got[i], want[i])
		}
	}

	frameOrder := make([]packetExtensionData, 4)
	n, err = parsePacketExtensionsFrameOrder(data, 3, counts, frameOrder)
	if err != nil {
		t.Fatalf("parsePacketExtensionsFrameOrder() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("parsePacketExtensionsFrameOrder() count = %d want 4", n)
	}
	for i := range want {
		if frameOrder[i].ID != want[i].ID || frameOrder[i].Frame != want[i].Frame || string(frameOrder[i].Data) != string(want[i].Data) {
			t.Fatalf("parsePacketExtensionsFrameOrder()[%d] = %+v want %+v", i, frameOrder[i], want[i])
		}
	}

	ext, ok, err := findPacketExtension(data, 3, 33)
	if err != nil {
		t.Fatalf("findPacketExtension() error = %v", err)
	}
	if !ok {
		t.Fatal("findPacketExtension() did not find id 33")
	}
	if ext.Frame != 2 || string(ext.Data) != "xyz" {
		t.Fatalf("findPacketExtension() = %+v want frame=2 data=xyz", ext)
	}
}

func TestPacketExtensionIteratorRepeatExpansion(t *testing.T) {
	data := mustHexBytes(t, "0604")
	got := make([]packetExtensionData, 3)
	n, err := parsePacketExtensions(data, 3, got)
	if err != nil {
		t.Fatalf("parsePacketExtensions() error = %v", err)
	}
	if n != 3 {
		t.Fatalf("parsePacketExtensions() count = %d want 3", n)
	}
	for i := 0; i < 3; i++ {
		if got[i].ID != 3 || got[i].Frame != i || len(got[i].Data) != 0 {
			t.Fatalf("parsePacketExtensions()[%d] = %+v want id=3 frame=%d", i, got[i], i)
		}
	}
}

func TestPacketExtensionIteratorRejectsInvalidSeparator(t *testing.T) {
	var iter packetExtensionIterator
	initPacketExtensionIterator(&iter, []byte{0x03, 0x02}, 1)
	if ok, err := iter.next(nil); err == nil || ok {
		t.Fatalf("iter.next() = (%v,%v) want invalid packet error", ok, err)
	}
}
