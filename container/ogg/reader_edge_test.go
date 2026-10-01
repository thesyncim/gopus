package ogg

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type chunkCountingReader struct {
	data      []byte
	maxChunk  int
	bytesRead int
}

type dataThenErrorReader struct {
	data []byte
	err  error
	done bool
}

func (r *dataThenErrorReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), r.err
	}
	return 0, io.EOF
}

func (r *chunkCountingReader) Read(p []byte) (int, error) {
	if r.bytesRead == len(r.data) {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.maxChunk {
		n = r.maxChunk
	}
	if n > len(r.data)-r.bytesRead {
		n = len(r.data) - r.bytesRead
	}
	copy(p, r.data[r.bytesRead:r.bytesRead+n])
	r.bytesRead += n
	return n, nil
}

func audioPageStreamPrefix() []byte {
	stream := buildAudioPageStream(0, nil)
	_, firstPageSize, _ := ParsePage(stream)
	_, secondPageSize, _ := ParsePage(stream[firstPageSize:])
	return append([]byte(nil), stream[:firstPageSize+secondPageSize]...)
}

func TestReaderReadPageReturnsPermanentErrorsPromptly(t *testing.T) {
	const trailingBytes = 64 * 1024
	trailing := bytes.Repeat([]byte{0x5a}, trailingBytes)
	headers := audioPageStreamPrefix()

	t.Run("bad capture pattern", func(t *testing.T) {
		data := append(append([]byte(nil), headers...), 'X')
		data = append(data, trailing...)
		raw := &chunkCountingReader{data: data, maxChunk: 1}
		reader, err := NewReader(raw)
		if err != nil {
			t.Fatalf("NewReader() error = %v", err)
		}
		_, _, err = reader.ReadPacket()
		if !errors.Is(err, ErrInvalidPage) {
			t.Fatalf("ReadPacket() error = %v, want ErrInvalidPage", err)
		}
		if want := len(headers) + 1; raw.bytesRead != want {
			t.Fatalf("reader consumed %d bytes, want %d", raw.bytesRead, want)
		}
	})

	t.Run("bad CRC", func(t *testing.T) {
		pageData := Page{
			SerialNumber: 0x1234,
			PageSequence: 2,
			Segments:     BuildSegmentTable(1),
			Payload:      []byte{0x08},
		}
		page := pageData.Encode()
		page[22] ^= 1
		data := append(append([]byte(nil), headers...), page...)
		data = append(data, trailing...)
		raw := &chunkCountingReader{data: data, maxChunk: 1}
		reader, err := NewReader(raw)
		if err != nil {
			t.Fatalf("NewReader() error = %v", err)
		}
		_, _, err = reader.ReadPacket()
		if !errors.Is(err, ErrBadCRC) {
			t.Fatalf("ReadPacket() error = %v, want ErrBadCRC", err)
		}
		if want := len(headers) + len(page); raw.bytesRead != want {
			t.Fatalf("reader consumed %d bytes, want %d", raw.bytesRead, want)
		}
	})
}

func TestReaderReadPageDistinguishesTruncationFromEOF(t *testing.T) {
	headers := audioPageStreamPrefix()
	pageData := Page{
		SerialNumber: 0x1234,
		PageSequence: 2,
		Segments:     BuildSegmentTable(1),
		Payload:      []byte{0x08},
	}
	page := pageData.Encode()
	truncated := append(append([]byte(nil), headers...), page[:len(page)-1]...)

	t.Run("incomplete page at EOF", func(t *testing.T) {
		reader, err := NewReader(&chunkCountingReader{data: truncated, maxChunk: 3})
		if err != nil {
			t.Fatalf("NewReader() error = %v", err)
		}
		_, _, err = reader.ReadPacket()
		if !errors.Is(err, ErrInvalidPage) {
			t.Fatalf("ReadPacket() error = %v, want ErrInvalidPage", err)
		}
	})

	t.Run("empty stream tail", func(t *testing.T) {
		reader, err := NewReader(&chunkCountingReader{data: headers, maxChunk: 3})
		if err != nil {
			t.Fatalf("NewReader() error = %v", err)
		}
		_, _, err = reader.ReadPacket()
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadPacket() error = %v, want io.EOF", err)
		}
	})
}

func TestReaderReadPageAcceptsFragmentedPages(t *testing.T) {
	packet := []byte{0x08, 0x01}
	stream := buildAudioPageStream(960, [][]byte{packet})
	reader, err := NewReader(&chunkCountingReader{data: stream, maxChunk: 3})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	got, granule, err := reader.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	if !bytes.Equal(got, packet) {
		t.Fatalf("ReadPacket() packet = %v, want %v", got, packet)
	}
	if granule != 960 {
		t.Fatalf("ReadPacket() granule = %d, want 960", granule)
	}
}

func TestReaderProcessesBytesReturnedWithReadError(t *testing.T) {
	const serial = 0x44556677
	head := DefaultOpusHead(48000, 1).Encode()
	tags := DefaultOpusTags().Encode()
	audio := []byte{0xf8, 0x00}
	var stream []byte
	for _, page := range [][]byte{
		readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head),
		readerBoundaryPacketPage(serial, 1, 0, 0, tags),
		readerBoundaryPacketPage(serial, 2, 0, 960, audio),
	} {
		stream = append(stream, page...)
	}
	readErr := errors.New("terminal read error")
	r, err := NewReader(&dataThenErrorReader{data: stream, err: readErr})
	if err != nil {
		t.Fatalf("NewReader returned %v before processing buffered headers", err)
	}
	packet, granule, err := r.ReadPacket()
	if err != nil || !bytes.Equal(packet, audio) || granule != 960 {
		t.Fatalf("ReadPacket = (%x, %d, %v), want (%x, 960, nil)", packet, granule, err, audio)
	}
	if _, _, err := r.ReadPacket(); !errors.Is(err, readErr) {
		t.Fatalf("ReadPacket after buffered page = %v, want terminal read error", err)
	}
}

// TestReaderAccessors_NilHeader verifies the zero-value fallbacks of the Reader
// metadata accessors when no OpusHead has been parsed (Header == nil). NewReader
// always populates Header, but the accessors guard against a nil header and
// return zero; this exercises that guard.
func TestReaderAccessors_NilHeader(t *testing.T) {
	var r Reader // zero value: Header == nil

	if got := r.PreSkip(); got != 0 {
		t.Errorf("PreSkip() with nil Header = %d, want 0", got)
	}
	if got := r.Channels(); got != 0 {
		t.Errorf("Channels() with nil Header = %d, want 0", got)
	}
	if got := r.SampleRate(); got != 0 {
		t.Errorf("SampleRate() with nil Header = %d, want 0", got)
	}
}

// tocByte builds an Opus TOC byte from a config index (0-31) and a frame-count
// code (0-3) per RFC 6716 §3.1.
func tocByte(config uint8, code uint8) byte {
	return byte(config<<3) | (code & 0x03)
}

// TestPacketDuration48k covers the per-packet duration decoder across all four
// frame-count codes and its rejection paths. The frame sizes come from the Opus
// config table at 48 kHz (RFC 6716 Table 2).
func TestPacketDuration48k(t *testing.T) {
	tests := []struct {
		name    string
		packet  []byte
		wantDur uint64
		wantOK  bool
	}{
		{
			name:   "empty packet",
			packet: nil,
			wantOK: false,
		},
		{
			// config 16 (CELT NB-ish) frame size 120, code 0 = single frame.
			name:    "code0 single frame",
			packet:  []byte{tocByte(16, 0)},
			wantDur: 120,
			wantOK:  true,
		},
		{
			// config 1 frame size 960, code 1 = two frames same size.
			name:    "code1 two frames",
			packet:  []byte{tocByte(1, 1)},
			wantDur: 1920,
			wantOK:  true,
		},
		{
			// code 2 = two frames (CBR/VBR signalled), duration is 2x frame size.
			name:    "code2 two frames",
			packet:  []byte{tocByte(1, 2)},
			wantDur: 1920,
			wantOK:  true,
		},
		{
			// code 3 with a valid frame count in byte 1 (low 6 bits).
			name:    "code3 multi frame",
			packet:  []byte{tocByte(0, 3), 3}, // config 0 -> 480 samples, 3 frames
			wantDur: 1440,
			wantOK:  true,
		},
		{
			name:   "code3 missing count byte",
			packet: []byte{tocByte(0, 3)},
			wantOK: false,
		},
		{
			name:   "code3 zero frame count",
			packet: []byte{tocByte(0, 3), 0},
			wantOK: false,
		},
		{
			name:   "code3 frame count over 48",
			packet: []byte{tocByte(0, 3), 49},
			wantOK: false,
		},
		{
			name:    "code3 frame count exactly 48",
			packet:  []byte{tocByte(0, 3), 48}, // 480 * 48
			wantDur: 480 * 48,
			wantOK:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dur, ok := packetDuration48k(tc.packet)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && dur != tc.wantDur {
				t.Fatalf("dur=%d want %d", dur, tc.wantDur)
			}
		})
	}
}

// TestPacketGranuleDistribution covers the back-to-front granule assignment
// across a page's packets through the public reader: the normal case, the
// underflow clamp (a back-computed position that would go negative pins to 0),
// and the undecodable-packet fallback (every packet inherits the page granule).
func TestPacketGranuleDistribution(t *testing.T) {
	read2 := func(t *testing.T, stream []byte) (uint64, uint64) {
		t.Helper()
		r, err := NewReader(bytes.NewReader(stream))
		if err != nil {
			t.Fatal(err)
		}
		_, g0, err := r.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		_, g1, err := r.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		return g0, g1
	}

	t.Run("two decodable packets back-compute from page granule", func(t *testing.T) {
		// Two 20ms@48k frames (config 1, code 0 -> 960 samples each); the page
		// granule is the end position of the last packet.
		g0, g1 := read2(t, buildAudioPageStream(1920, [][]byte{{tocByte(1, 0)}, {tocByte(1, 0)}}))
		if g0 != 960 || g1 != 1920 {
			t.Errorf("granules = (%d,%d), want (960,1920)", g0, g1)
		}
	})

	t.Run("underflow clamps earlier packet to zero", func(t *testing.T) {
		// Page granule (500) is smaller than the trailing duration (960), so the
		// first packet pins to 0.
		g0, g1 := read2(t, buildAudioPageStream(500, [][]byte{{tocByte(1, 0)}, {tocByte(1, 0)}}))
		if g0 != 0 || g1 != 500 {
			t.Errorf("granules = (%d,%d), want (0,500)", g0, g1)
		}
	})

	t.Run("undecodable packet falls back to page granule for all", func(t *testing.T) {
		// A code-3 packet with no frame-count byte is undecodable, so every packet
		// on the page inherits the page granule.
		g0, g1 := read2(t, buildAudioPageStream(1234, [][]byte{{tocByte(1, 0)}, {tocByte(1, 3)}}))
		if g0 != 1234 || g1 != 1234 {
			t.Errorf("granules = (%d,%d), want (1234,1234)", g0, g1)
		}
	})
}

// buildAudioPageStream builds a minimal Ogg Opus stream — an OpusHead BOS page,
// an OpusTags page, then one audio page carrying the given packets at the given
// granule position.
func buildAudioPageStream(granule uint64, packets [][]byte) []byte {
	const serial = 0x1234
	seq := uint32(0)
	page := func(headerType byte, gran uint64, segments, payload []byte) []byte {
		p := Page{
			HeaderType:   headerType,
			GranulePos:   gran,
			SerialNumber: serial,
			PageSequence: seq,
			Segments:     segments,
			Payload:      payload,
		}
		seq++
		return p.Encode()
	}

	head := DefaultOpusHead(48000, 2).Encode()
	tags := DefaultOpusTags().Encode()

	var seg, pay []byte
	for _, pkt := range packets {
		seg = append(seg, BuildSegmentTable(len(pkt))...)
		pay = append(pay, pkt...)
	}

	var out []byte
	out = append(out, page(PageFlagBOS, 0, BuildSegmentTable(len(head)), head)...)
	out = append(out, page(0, 0, BuildSegmentTable(len(tags)), tags)...)
	out = append(out, page(0, granule, seg, pay)...)
	return out
}

func TestReadPacketIntoOversizedPacketPreservesBufferAndNextPacket(t *testing.T) {
	large := bytes.Repeat([]byte{0x08}, 511)
	next := []byte{0x08, 0x42}
	continued := audioPageStreamPrefix()
	for i, part := range [][]byte{large[:255], large[255:510], append(append([]byte(nil), large[510:]...), next...)} {
		page := Page{SerialNumber: 0x1234, PageSequence: uint32(i + 2), Payload: part, Segments: []byte{255}}
		if i > 0 {
			page.HeaderType = PageFlagContinuation
		}
		if i == 2 {
			page.Segments = []byte{1, 2}
			page.GranulePos = 1920
		}
		continued = append(continued, page.Encode()...)
	}
	for _, tc := range []struct {
		name string
		data []byte
		seek bool
	}{
		{"one page", buildAudioPageStream(1920, [][]byte{large, next}), false},
		{"continued packet", continued, false},
		{"seek pushback", continued, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewReader(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			if tc.seek {
				if err := r.SeekGranule(0); err != nil {
					t.Fatal(err)
				}
			}
			backing := bytes.Repeat([]byte{0xa5}, 1024)
			dst := backing[:16]
			if n, granule, err := r.ReadPacketInto(dst); n != 0 || granule != 0 || err != ErrPacketTooLarge {
				t.Fatalf("oversized read = (%d, %d, %v)", n, granule, err)
			}
			if !bytes.Equal(backing[len(dst):], bytes.Repeat([]byte{0xa5}, len(backing)-len(dst))) {
				t.Error("read overwrote bytes beyond len(dst)")
			}
			if r.GranulePos() != 960 {
				t.Errorf("consumed packet granule = %d, want 960", r.GranulePos())
			}
			n, granule, err := r.ReadPacketInto(dst)
			if err != nil || granule != 1920 || !bytes.Equal(dst[:n], next) {
				t.Fatalf("following packet = (%x, %d, %v), want (%x, 1920, nil)", dst[:n], granule, err, next)
			}
		})
	}
}

func TestNewReaderRejectsExtraPacketOnHeaderPage(t *testing.T) {
	const serial = 0x9876
	head := DefaultOpusHead(48000, 1).Encode()
	tags := DefaultOpusTags().Encode()
	audio := []byte{0xf8, 0x00}

	tests := []struct {
		name  string
		pages [][]byte
	}{
		{
			name: "OpusHead page",
			pages: [][]byte{
				readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head, audio),
				readerBoundaryPacketPage(serial, 1, 0, 0, tags),
				readerBoundaryPacketPage(serial, 2, PageFlagEOS, 960, audio),
			},
		},
		{
			name: "OpusTags completion page",
			pages: [][]byte{
				readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head),
				readerBoundaryPacketPage(serial, 1, 0, 0, tags, audio),
				readerBoundaryPacketPage(serial, 2, PageFlagEOS, 960, audio),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stream []byte
			for _, page := range tc.pages {
				stream = append(stream, page...)
			}
			if _, err := NewReader(bytes.NewReader(stream)); !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("NewReader error = %v, want ErrInvalidPage", err)
			}
		})
	}
}

func TestNewReaderRejectsSequenceGapInContinuedOpusTags(t *testing.T) {
	const serial = 0x9877
	head := DefaultOpusHead(48000, 1).Encode()
	tags := (&OpusTags{Vendor: strings.Repeat("v", 700), Comments: []string{"TITLE=test"}}).Encode()
	if len(tags) <= 255 {
		t.Fatal("test OpusTags packet must span pages")
	}
	audio := []byte{0xf8, 0x00}

	pages := [][]byte{
		readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head),
		readerBoundaryPage(serial, 1, 0, ^uint64(0), []byte{255}, tags[:255]),
		readerBoundaryPage(serial, 3, PageFlagContinuation, 0, BuildSegmentTable(len(tags)-255), tags[255:]),
		readerBoundaryPacketPage(serial, 4, PageFlagEOS, 960, audio),
	}
	var stream []byte
	for _, page := range pages {
		stream = append(stream, page...)
	}
	if _, err := NewReader(bytes.NewReader(stream)); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("NewReader error = %v, want ErrInvalidPage", err)
	}
}

func TestNewReaderAcceptsContiguousMultiPageOpusTags(t *testing.T) {
	const serial = 0x9878
	head := DefaultOpusHead(48000, 1).Encode()
	vendor := strings.Repeat("v", 700)
	tags := (&OpusTags{Vendor: vendor, Comments: []string{"TITLE=test"}}).Encode()
	audio := []byte{0xf8, 0x00}

	pages := [][]byte{
		readerBoundaryPacketPage(serial, 0, PageFlagBOS, 0, head),
		readerBoundaryPage(serial, 1, 0, ^uint64(0), []byte{255}, tags[:255]),
		readerBoundaryPage(serial, 2, PageFlagContinuation, 0, BuildSegmentTable(len(tags)-255), tags[255:]),
		readerBoundaryPacketPage(serial, 3, PageFlagEOS, 960, audio),
	}
	var stream []byte
	for _, page := range pages {
		stream = append(stream, page...)
	}
	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("NewReader returned error: %v", err)
	}
	if value, ok := r.Tags.Value("TITLE"); r.Tags.Vendor != vendor || !ok || value != "test" {
		t.Fatalf("parsed tags = (%q, %v), want vendor and TITLE comment", r.Tags.Vendor, r.Tags.Comments)
	}
	packet, _, err := r.ReadPacket()
	if err != nil || !bytes.Equal(packet, audio) {
		t.Fatalf("ReadPacket = (%x, %v), want (%x, nil)", packet, err, audio)
	}
}

func readerBoundaryPacketPage(serial, seq uint32, flags byte, granule uint64, packets ...[]byte) []byte {
	var segments, payload []byte
	for _, packet := range packets {
		segments = append(segments, BuildSegmentTable(len(packet))...)
		payload = append(payload, packet...)
	}
	return readerBoundaryPage(serial, seq, flags, granule, segments, payload)
}

func readerBoundaryPage(serial, seq uint32, flags byte, granule uint64, segments, payload []byte) []byte {
	page := Page{HeaderType: flags, GranulePos: granule, SerialNumber: serial, PageSequence: seq, Segments: segments, Payload: payload}
	return page.Encode()
}

func TestReadPacketIntoOversizedZeroAlloc(t *testing.T) {
	// All packets fit in one page so the measurement isolates packet assembly.
	packets := make([][]byte, 120)
	for i := range packets {
		packets[i] = bytes.Repeat([]byte{0x08}, 300)
	}
	r, err := NewReader(bytes.NewReader(buildAudioPageStream(120*960, packets)))
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]byte, 16)
	read := func() {
		if n, _, err := r.ReadPacketInto(dst); n != 0 || err != ErrPacketTooLarge {
			t.Fatalf("oversized read = (%d, %v)", n, err)
		}
	}
	read() // Warm page parsing before measuring packet rejection.
	if allocs := testing.AllocsPerRun(100, read); allocs != 0 {
		t.Fatalf("oversized packet allocs = %g, want 0", allocs)
	}
}

func TestReaderDropsPacketsWithMissingContinuationPages(t *testing.T) {
	first := []byte{0x08, 0x11}
	spanning := bytes.Repeat([]byte{0x08}, 511)
	last := []byte{0x08, 0x22}
	pages := []Page{
		{SerialNumber: 0x1234, PageSequence: 2, GranulePos: 960, Segments: []byte{2}, Payload: first},
		{SerialNumber: 0x1234, PageSequence: 3, GranulePos: ^uint64(0), Segments: []byte{255}, Payload: spanning[:255]},
		{SerialNumber: 0x1234, PageSequence: 4, HeaderType: PageFlagContinuation, GranulePos: ^uint64(0), Segments: []byte{255}, Payload: spanning[255:510]},
		{SerialNumber: 0x1234, PageSequence: 5, HeaderType: PageFlagContinuation | PageFlagEOS, GranulePos: 2880, Segments: []byte{1, 2}, Payload: append(append([]byte(nil), spanning[510:]...), last...)},
	}
	for _, tc := range []struct {
		name    string
		keep    []int
		want    [][]byte
		granule []uint64
	}{
		{"complete", []int{0, 1, 2, 3}, [][]byte{first, spanning, last}, []uint64{960, 1920, 2880}},
		{"missing prefix", []int{0, 2, 3}, [][]byte{first, last}, []uint64{960, 2880}},
		{"missing middle", []int{0, 1, 3}, [][]byte{first, last}, []uint64{960, 2880}},
		{"first audio page continued", []int{2, 3}, [][]byte{last}, []uint64{2880}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := audioPageStreamPrefix()
			for _, index := range tc.keep {
				data = append(data, pages[index].Encode()...)
				// Other serials do not interrupt this stream's page sequence.
				other := Page{SerialNumber: 0xabcd, PageSequence: 42, Segments: []byte{1}, Payload: []byte{0}}
				data = append(data, other.Encode()...)
			}
			for _, bounded := range []bool{false, true} {
				r, err := NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				dst := make([]byte, 1024)
				for i, want := range tc.want {
					var got []byte
					var granule uint64
					if bounded {
						var n int
						n, granule, err = r.ReadPacketInto(dst)
						got = dst[:n]
					} else {
						got, granule, err = r.ReadPacket()
					}
					if err != nil || !bytes.Equal(got, want) || granule != tc.granule[i] {
						t.Fatalf("bounded=%v packet %d: length=%d granule=%d err=%v; want length=%d granule=%d", bounded, i, len(got), granule, err, len(want), tc.granule[i])
					}
				}
				if _, _, err := r.ReadPacket(); err != io.EOF {
					t.Fatalf("bounded=%v trailing read = %v, want EOF", bounded, err)
				}
			}
		})
	}
}
