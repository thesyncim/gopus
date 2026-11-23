package ogg

import (
	"bytes"
	"io"
	"testing"
)

func buildMultiPacketOggStream(t *testing.T, packets [][]byte, pageGranule uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	header := DefaultOpusHead(48000, 2)
	headPage := &Page{
		HeaderType:   PageFlagBOS,
		GranulePos:   0,
		SerialNumber: 42,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(header.Encode())),
		Payload:      header.Encode(),
	}
	buf.Write(headPage.Encode())

	tags := DefaultOpusTags()
	tagsBytes := tags.Encode()
	tagsPage := &Page{
		GranulePos:   0,
		SerialNumber: 42,
		PageSequence: 1,
		Segments:     BuildSegmentTable(len(tagsBytes)),
		Payload:      tagsBytes,
	}
	buf.Write(tagsPage.Encode())

	var segs []byte
	var payload []byte
	for _, p := range packets {
		segs = append(segs, BuildSegmentTable(len(p))...)
		payload = append(payload, p...)
	}
	// Non-EOS data page so ReadPacket exercises packetGranule() and the
	// incremental trailing-duration cache.
	dataPage := &Page{
		GranulePos:   pageGranule,
		SerialNumber: 42,
		PageSequence: 2,
		Segments:     segs,
		Payload:      payload,
	}
	buf.Write(dataPage.Encode())
	return buf.Bytes()
}

func TestOggMultiPacketUnknownGranulePreserved(t *testing.T) {
	// 4 valid 20ms CELT packets packed onto one non-EOS page with unknown granule (^uint64(0)).
	packets := [][]byte{
		{0xfc, 0x01, 0x02},
		{0xfc, 0x03, 0x04},
		{0xfc, 0x05, 0x06},
		{0xfc, 0x07, 0x08},
	}
	stream := buildMultiPacketOggStream(t, packets, ^uint64(0))
	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	for i := range packets {
		_, gran, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if gran != ^uint64(0) {
			t.Fatalf("packet %d granule = %d, want ^uint64(0)", i, gran)
		}
	}
	if _, _, err := r.ReadPacket(); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestOggMultiPacketPageGranuleIncremental(t *testing.T) {
	// Mix 10ms (0xf0 -> 480) and 20ms (0xfc -> 960) packets on one non-EOS page.
	durations := []uint64{480, 960, 480, 960, 960, 480, 960, 480, 960, 960}
	packets := make([][]byte, len(durations))
	var total uint64
	for i, d := range durations {
		toc := byte(0xfc)
		if d == 480 {
			toc = 0xf0
		}
		packets[i] = []byte{toc, byte(i), 0xaa}
		total += d
	}
	const baseGranule uint64 = 10000
	stream := buildMultiPacketOggStream(t, packets, baseGranule+total)
	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	want := baseGranule
	for i, d := range durations {
		want += d
		_, gran, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
		if gran != want {
			t.Fatalf("packet %d granule = %d, want %d", i, gran, want)
		}
	}
}
