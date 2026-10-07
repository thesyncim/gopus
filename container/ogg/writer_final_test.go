package ogg

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func parseWriterPages(t *testing.T, data []byte) []*Page {
	t.Helper()
	var pages []*Page
	for len(data) > 0 {
		page, consumed, err := ParsePage(data)
		if err != nil {
			t.Fatalf("ParsePage: %v", err)
		}
		pages = append(pages, page)
		data = data[consumed:]
	}
	return pages
}

func TestWriteFinalPacketTrimmedEOSGranule(t *testing.T) {
	var stream bytes.Buffer
	w, err := NewWriter(&stream, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}

	first := []byte{0xf8, 0x01}
	final := []byte{0xf8, 0x02}
	if err := w.WritePacket(first, 960); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}
	if err := w.WriteFinalPacket(final, 317); err != nil {
		t.Fatalf("WriteFinalPacket: %v", err)
	}

	const wantGranule = 960 + 317
	if got := w.GranulePos(); got != wantGranule {
		t.Fatalf("GranulePos = %d, want %d", got, wantGranule)
	}
	if !w.closed {
		t.Fatal("WriteFinalPacket succeeded without closing the Writer")
	}
	if err := w.WritePacket([]byte{0xf8}, 960); !errors.Is(err, ErrUnexpectedEOS) {
		t.Fatalf("WritePacket after final packet = %v, want ErrUnexpectedEOS", err)
	}

	pages := parseWriterPages(t, stream.Bytes())
	if len(pages) != 4 {
		t.Fatalf("page count = %d, want headers + 2 audio pages", len(pages))
	}
	if pages[2].IsEOS() || pages[2].GranulePos != 960 {
		t.Fatalf("prior audio page flags/granule = %d/%d, want non-EOS/960", pages[2].HeaderType, pages[2].GranulePos)
	}
	last := pages[3]
	if !last.IsEOS() || last.IsContinuation() || last.GranulePos != wantGranule {
		t.Fatalf("final page flags/granule = %d/%d, want EOS, no continuation, %d", last.HeaderType, last.GranulePos, wantGranule)
	}
	if !bytes.Equal(last.Payload, final) {
		t.Fatalf("final page payload = %x, want %x", last.Payload, final)
	}

	pageCount, byteCount := w.PageCount(), stream.Len()
	if err := w.Close(); err != nil {
		t.Fatalf("Close after WriteFinalPacket: %v", err)
	}
	if w.PageCount() != pageCount || stream.Len() != byteCount {
		t.Fatal("Close after WriteFinalPacket emitted another page")
	}

	r, err := NewReader(bytes.NewReader(stream.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	for i, want := range [][]byte{first, final} {
		got, granule, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("packet %d = %x, want %x", i, got, want)
		}
		wantGranule := uint64(960)
		if i == 1 {
			wantGranule = wantGranule + 317
		}
		if granule != wantGranule {
			t.Fatalf("packet %d granule = %d, want %d", i, granule, wantGranule)
		}
	}
	if _, _, err := r.ReadPacket(); err != io.EOF {
		t.Fatalf("read after final packet = %v, want EOF", err)
	}
}

func TestWriteFinalPacketSpanningPages(t *testing.T) {
	for _, size := range []int{maxPagePayload - 1, maxPagePayload, maxPagePayload + 1} {
		t.Run(map[int]string{
			maxPagePayload - 1: "below_page_limit",
			maxPagePayload:     "exact_page_limit",
			maxPagePayload + 1: "above_page_limit",
		}[size], func(t *testing.T) {
			packet := make([]byte, size)
			for i := range packet {
				packet[i] = byte(i)
			}

			var stream bytes.Buffer
			w, err := NewWriter(&stream, 48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.WriteFinalPacket(packet, 123); err != nil {
				t.Fatalf("WriteFinalPacket: %v", err)
			}

			pages := parseWriterPages(t, stream.Bytes())
			firstAudioPage := 2
			wantAudioPages := size/maxPagePayload + 1
			if got := len(pages) - firstAudioPage; got != wantAudioPages {
				t.Fatalf("audio page count = %d, want %d", got, wantAudioPages)
			}
			for i := range wantAudioPages {
				page := pages[firstAudioPage+i]
				finalPage := i == wantAudioPages-1
				if page.IsEOS() != finalPage {
					t.Fatalf("audio page %d EOS=%v, want %v", i, page.IsEOS(), finalPage)
				}
				if page.IsContinuation() != (i > 0) {
					t.Fatalf("audio page %d continuation=%v, want %v", i, page.IsContinuation(), i > 0)
				}
				wantGranule := ^uint64(0)
				if finalPage {
					wantGranule = 123
				}
				if page.GranulePos != wantGranule {
					t.Fatalf("audio page %d granule = %d, want %d", i, page.GranulePos, wantGranule)
				}
				if len(page.Segments) == 0 || (page.Segments[len(page.Segments)-1] < 255) != finalPage {
					t.Fatalf("audio page %d has incorrect packet termination: %v", i, page.Segments)
				}
			}

			r, err := NewReader(bytes.NewReader(stream.Bytes()))
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			got := make([]byte, size)
			n, granule, err := r.ReadPacketInto(got)
			got = got[:n]
			if err != nil || !bytes.Equal(got, packet) || granule != 123 {
				t.Fatalf("ReadPacketInto = (%d bytes, granule %d, err %v), want packet of %d bytes at granule 123", len(got), granule, err, size)
			}
		})
	}
}

func TestWriteFinalPacketWriteFailureDoesNotClose(t *testing.T) {
	sw := &shortWriteWriter{shortAt: 3, shortBytes: 0}
	w, err := NewWriter(sw, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteFinalPacket([]byte{0xf8}, 17); err != io.ErrShortWrite {
		t.Fatalf("WriteFinalPacket error = %v, want %v", err, io.ErrShortWrite)
	}
	if w.closed {
		t.Fatal("Writer marked closed after final page write failed")
	}
	if got := w.GranulePos(); got != 0 {
		t.Fatalf("GranulePos after failed final page = %d, want 0", got)
	}
}

func TestWriteFinalPacketZeroAlloc(t *testing.T) {
	const runs = 100
	writers := make([]*Writer, runs+1)
	for i := range writers {
		w, err := NewWriter(io.Discard, 48000, 1)
		if err != nil {
			t.Fatal(err)
		}
		writers[i] = w
	}
	packet := []byte{0xf8, 0x01}
	next := 0
	allocs := testing.AllocsPerRun(runs, func() {
		if err := writers[next].WriteFinalPacket(packet, 1); err != nil {
			t.Fatalf("WriteFinalPacket: %v", err)
		}
		next++
	})
	if allocs != 0 {
		t.Fatalf("WriteFinalPacket allocs/op = %g, want 0", allocs)
	}
}
