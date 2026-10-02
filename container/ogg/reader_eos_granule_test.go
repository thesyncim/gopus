package ogg

import (
	"bytes"
	"errors"
	"testing"
)

func eosGranuleStream(pages ...[]byte) []byte {
	const serial = 0x4e71
	stream := readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, DefaultOpusHead(48000, 2).Encode())
	stream = append(stream, readerBoundaryPacketPage(serial, 1, 0, 0, DefaultOpusTags().Encode())...)
	for _, page := range pages {
		stream = append(stream, page...)
	}
	return stream
}

func TestReaderFirstEOSPageGranules(t *testing.T) {
	packets := [][]byte{{0xf8, 0x11}, {0xf8, 0x22}}
	for _, tc := range []struct {
		name   string
		pageGP uint64
		want   []uint64
	}{
		{name: "trimmed from initial position", pageGP: 1500, want: []uint64{960, 1500}},
		{name: "positive initial position", pageGP: 2000, want: []uint64{1040, 2000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := eosGranuleStream(readerBoundaryPacketPage(0x4e71, 2, PageFlagEOS, tc.pageGP, packets...))
			r, err := NewReader(bytes.NewReader(stream))
			if err != nil {
				t.Fatal(err)
			}
			for i, wantPacket := range packets {
				got, gp, err := r.ReadPacket()
				if err != nil || !bytes.Equal(got, wantPacket) || gp != tc.want[i] {
					t.Fatalf("packet %d = (%x, %d, %v), want (%x, %d, nil)", i, got, gp, err, wantPacket, tc.want[i])
				}
			}
			if err := r.SeekGranule(800); err != nil {
				t.Fatalf("SeekGranule after consuming EOS page: %v", err)
			}
			got, gp, err := r.ReadPacket()
			if err != nil || !bytes.Equal(got, packets[0]) || gp != tc.want[0] {
				t.Fatalf("first packet after seek = (%x, %d, %v), want (%x, %d, nil)", got, gp, err, packets[0], tc.want[0])
			}
			got, gp, err = r.ReadPacket()
			if err != nil || !bytes.Equal(got, packets[1]) || gp != tc.want[1] {
				t.Fatalf("second packet after seek = (%x, %d, %v), want (%x, %d, nil)", got, gp, err, packets[1], tc.want[1])
			}
		})
	}
}

func TestReaderTrimmedEOSGranulesAfterCompletedPage(t *testing.T) {
	p0 := []byte{0xf8, 0x10}
	p1 := []byte{0xf8, 0x11}
	p2 := []byte{0xf8, 0x12}
	stream := eosGranuleStream(
		readerBoundaryPacketPage(0x4e71, 2, 0, 960, p0),
		readerBoundaryPacketPage(0x4e71, 3, PageFlagEOS, 2500, p1, p2),
	)

	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		packet []byte
		gp     uint64
	}{{p0, 960}, {p1, 1920}, {p2, 2500}} {
		got, gp, err := r.ReadPacket()
		if err != nil || !bytes.Equal(got, want.packet) || gp != want.gp {
			t.Fatalf("packet %d = (%x, %d, %v), want (%x, %d, nil)", i, got, gp, err, want.packet, want.gp)
		}
	}

	r, err = NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekGranule(1800); err != nil {
		t.Fatalf("SeekGranule(1800): %v", err)
	}
	got, gp, err := r.ReadPacket()
	if err != nil || !bytes.Equal(got, p1) || gp != 1920 {
		t.Fatalf("packet after seek = (%x, %d, %v), want (%x, 1920, nil)", got, gp, err, p1)
	}
}

func TestReaderEOSGranuleForSpanningOversizedPacket(t *testing.T) {
	p0 := []byte{0xf8, 0x10}
	p1 := bytes.Repeat([]byte{0x55}, 300)
	p1[0] = 0xf8 // one 20 ms frame; the packet spans the page boundary.
	p2 := []byte{0xf8, 0x12}
	page2Segments := append(BuildSegmentTable(len(p0)), 255)
	page2 := readerBoundaryPage(0x4e71, 2, 0, 960, page2Segments, append(append([]byte(nil), p0...), p1[:255]...))
	page3Payload := append(append([]byte(nil), p1[255:]...), p2...)
	page3 := readerBoundaryPage(0x4e71, 3, PageFlagContinuation|PageFlagEOS, 2500, []byte{45, 2}, page3Payload)
	stream := eosGranuleStream(page2, page3)

	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if got, gp, err := r.ReadPacket(); err != nil || !bytes.Equal(got, p0) || gp != 960 {
		t.Fatalf("first packet = (%x, %d, %v), want (%x, 960, nil)", got, gp, err, p0)
	}
	if n, gp, err := r.ReadPacketInto(make([]byte, 16)); n != 0 || gp != 0 || !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("oversized packet = (%d, %d, %v), want (0, 0, ErrPacketTooLarge)", n, gp, err)
	}
	if got := r.GranulePos(); got != 1920 {
		t.Fatalf("GranulePos after oversized packet = %d, want 1920", got)
	}
	if got, gp, err := r.ReadPacket(); err != nil || !bytes.Equal(got, p2) || gp != 2500 {
		t.Fatalf("last packet = (%x, %d, %v), want (%x, 2500, nil)", got, gp, err, p2)
	}

	r, err = NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SeekGranule(1800); err != nil {
		t.Fatalf("SeekGranule(1800): %v", err)
	}
	got, gp, err := r.ReadPacket()
	if err != nil || !bytes.Equal(got, p1) || gp != 1920 {
		t.Fatalf("packet after seek = (%d bytes, %d, %v), want (%d bytes, 1920, nil)", len(got), gp, err, len(p1))
	}
}

func TestReaderDoesNotReinferInitialGranuleAfterSequenceGap(t *testing.T) {
	p1 := bytes.Repeat([]byte{0x55}, 300)
	p1[0] = 0xf8
	p2 := []byte{0xf8, 0x12}
	firstFragment := readerBoundaryPage(0x4e71, 3, 0, ^uint64(0), []byte{255}, p1[:255])
	lastFragment := readerBoundaryPage(0x4e71, 4, PageFlagContinuation|PageFlagEOS, 1500, []byte{45, 2}, append(append([]byte(nil), p1[255:]...), p2...))
	r, err := NewReader(bytes.NewReader(eosGranuleStream(firstFragment, lastFragment)))
	if err != nil {
		t.Fatal(err)
	}
	if got, gp, err := r.ReadPacket(); err != nil || !bytes.Equal(got, p1) || gp != 540 {
		t.Fatalf("spanning packet after sequence gap = (%d bytes, %d, %v), want (%d bytes, 540, nil)", len(got), gp, err, len(p1))
	}
	if got, gp, err := r.ReadPacket(); err != nil || !bytes.Equal(got, p2) || gp != 1500 {
		t.Fatalf("next packet after sequence gap = (%x, %d, %v), want (%x, 1500, nil)", got, gp, err, p2)
	}
}

func TestReaderResumesGranuleTimelineAtCompletedPageAfterGap(t *testing.T) {
	spanning := bytes.Repeat([]byte{0x55}, 300)
	spanning[0] = 0xf8
	p0 := []byte{0xf8, 0x10}
	p1 := []byte{0xf8, 0x11}
	p2 := []byte{0xf8, 0x12}
	firstFragment := readerBoundaryPage(0x4e71, 3, 0, ^uint64(0), []byte{255}, spanning[:255])
	completedPage := readerBoundaryPage(0x4e71, 4, PageFlagContinuation, 1920, []byte{45, 2}, append(append([]byte(nil), spanning[255:]...), p0...))
	trimmedEOSPage := readerBoundaryPacketPage(0x4e71, 5, PageFlagEOS, 3700, p1, p2)
	r, err := NewReader(bytes.NewReader(eosGranuleStream(firstFragment, completedPage, trimmedEOSPage)))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		packet []byte
		gp     uint64
	}{{spanning, 960}, {p0, 1920}, {p1, 2880}, {p2, 3700}} {
		got, gp, err := r.ReadPacket()
		if err != nil || !bytes.Equal(got, want.packet) || gp != want.gp {
			t.Fatalf("packet %d = (%d bytes, %d, %v), want (%d bytes, %d, nil)", i, len(got), gp, err, len(want.packet), want.gp)
		}
	}
}

func BenchmarkReaderReadPacketIntoEOSTimeline(b *testing.B) {
	for _, tc := range []struct {
		name         string
		packetCount  int
		firstPageEOS bool
	}{
		{name: "first_eos_8", packetCount: 8, firstPageEOS: true},
		{name: "later_eos_8", packetCount: 8},
		{name: "first_eos_64", packetCount: 64, firstPageEOS: true},
		{name: "later_eos_64", packetCount: 64},
	} {
		stream := eosTimelineBenchmarkStream(tc.packetCount, tc.firstPageEOS)
		r, err := NewReader(bytes.NewReader(stream))
		if err != nil {
			b.Fatal(err)
		}
		dst := make([]byte, 8)
		readStream := func(b *testing.B) {
			if err := r.SeekGranule(0); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < tc.packetCount; i++ {
				if n, _, err := r.ReadPacketInto(dst); err != nil || n != 2 {
					b.Fatalf("packet %d = (%d, %v), want (2, nil)", i, n, err)
				}
			}
		}
		b.Run(tc.name, func(b *testing.B) {
			readStream(b) // warm the reader and pushback storage outside the timer
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				readStream(b)
			}
		})
	}
}

func eosTimelineBenchmarkStream(packetCount int, firstPageEOS bool) []byte {
	const serial = 0x4e71
	packets := make([][]byte, packetCount)
	for i := range packets {
		packets[i] = []byte{0xf8, byte(i)}
	}
	var pages [][]byte
	if firstPageEOS {
		pages = append(pages, readerBoundaryPacketPage(serial, 2, PageFlagEOS, uint64(packetCount*960-120), packets...))
	} else {
		pages = append(pages, readerBoundaryPacketPage(serial, 2, 0, 960, packets[0]))
		pages = append(pages, readerBoundaryPacketPage(serial, 3, PageFlagEOS, uint64(packetCount*960-120), packets[1:]...))
	}
	return eosGranuleStream(pages...)
}
