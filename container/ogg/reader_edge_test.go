package ogg

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type chunkCountingReader struct {
	data      []byte
	maxChunk  int
	bytesRead int
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
