package ogg

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

func TestWriterPacketsSpanningPages(t *testing.T) {
	const pagePayload = 255 * 255
	for _, size := range []int{pagePayload - 1, pagePayload, pagePayload + 1, 2 * pagePayload} {
		t.Run(fmt.Sprintf("bytes=%d", size), func(t *testing.T) {
			packet := bytes.Repeat([]byte{0x08}, size)
			var stream bytes.Buffer
			w, err := NewWriter(&stream, 48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			headerBytes := stream.Len()
			if err := w.WritePacket(packet, 960); err != nil {
				t.Fatal(err)
			}
			raw := stream.Bytes()[headerBytes:]
			for pageIndex := 0; pageIndex <= size/pagePayload; pageIndex++ {
				page, n, err := ParsePage(raw)
				if err != nil {
					t.Fatalf("audio page %d: %v", pageIndex, err)
				}
				complete := pageIndex == size/pagePayload
				wantGranule := ^uint64(0)
				if complete {
					wantGranule = 960
				}
				if page.PageSequence != uint32(pageIndex+2) || page.IsContinuation() != (pageIndex > 0) || page.GranulePos != wantGranule {
					t.Fatalf("page %d: sequence=%d flags=%d granule=%d, want granule=%d", pageIndex, page.PageSequence, page.HeaderType, page.GranulePos, wantGranule)
				}
				if len(page.Segments) == 0 || (page.Segments[len(page.Segments)-1] < 255) != complete {
					t.Fatalf("page %d has incorrect packet termination", pageIndex)
				}
				raw = raw[n:]
			}
			if len(raw) != 0 || w.PageCount() != uint32(size/pagePayload+3) {
				t.Fatalf("unexpected extra pages: bytes=%d count=%d", len(raw), w.PageCount())
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := NewReader(bytes.NewReader(stream.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]byte, size)
			n, granule, err := r.ReadPacketInto(out)
			if err != nil || n != size || granule != 960 || !bytes.Equal(out, packet) {
				t.Fatalf("ReadPacketInto = (%d, %d, %v); want identical %d-byte packet at granule 960", n, granule, err, size)
			}
			if _, _, err := r.ReadPacketInto(out); err != io.EOF {
				t.Fatalf("trailing read = %v, want EOF", err)
			}
		})
	}
}

func TestWriterLargePacketZeroAlloc(t *testing.T) {
	w, err := NewWriter(io.Discard, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 2*255*255)
	if err := w.WritePacket(packet, 960); err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if err := w.WritePacket(packet, 960); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("large packet allocs/op = %g, want 0", allocs)
	}
}

func TestWriterRejectsIDHeaderLargerThanOnePage(t *testing.T) {
	var stream bytes.Buffer
	_, err := NewWriterWithConfig(&stream, WriterConfig{
		SampleRate: 48000, Channels: 255, MappingFamily: MappingFamilyProjection,
		StreamCount: 255,
	})
	if err != ErrInvalidHeader || stream.Len() != 0 {
		t.Fatalf("oversized OpusHead: error=%v bytes=%d, want ErrInvalidHeader and no output", err, stream.Len())
	}
}
