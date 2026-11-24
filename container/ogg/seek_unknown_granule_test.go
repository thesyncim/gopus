package ogg

import (
	"bytes"
	"testing"
)

func TestBuildSegmentTableNegativeLength(t *testing.T) {
	got := BuildSegmentTable(-510)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("BuildSegmentTable(-510) = %v, want [0]", got)
	}
}

func TestSeekGranuleSkipsUnknownGranulePage(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	// First audio page carries RFC 3533 unknown granule (^uint64(0)) via a
	// raw page, followed by a normal page at granule 1920.
	p1 := &Page{
		Version:    0,
		HeaderType: 0,
		GranulePos: ^uint64(0),
		SerialNumber: w.serial,
		PageSequence: 2,
		Segments:   []byte{3},
		Payload:    []byte{0xFC, 0x01, 0x02},
	}
	if _, err := buf.Write(p1.Encode()); err != nil {
		t.Fatalf("write p1: %v", err)
	}
	p2 := &Page{
		Version:    0,
		HeaderType: PageFlagEOS,
		GranulePos: 1920,
		SerialNumber: w.serial,
		PageSequence: 3,
		Segments:   []byte{3},
		Payload:    []byte{0xFC, 0x03, 0x04},
	}
	if _, err := buf.Write(p2.Encode()); err != nil {
		t.Fatalf("write p2: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if err := r.SeekGranule(960); err != nil {
		t.Fatalf("SeekGranule(960): %v", err)
	}
	pkt, granule, err := r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if granule != 1920 {
		t.Fatalf("granule = %d, want 1920", granule)
	}
	if !bytes.Equal(pkt, []byte{0xFC, 0x03, 0x04}) {
		t.Fatalf("pkt = %x, want fc0304", pkt)
	}
}
