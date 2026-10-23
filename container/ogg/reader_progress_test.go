package ogg

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

type progressReaderFunc func([]byte) (int, error)

func (f progressReaderFunc) Read(p []byte) (int, error) { return f(p) }

func TestReaderBoundsConsecutiveEmptyReads(t *testing.T) {
	packet := []byte{0x08, 0x01}
	stream := buildAudioPageStream(960, [][]byte{packet})
	headersSize := len(audioPageStreamPrefix())
	for _, stallAt := range []int{0, headersSize, headersSize + 10} {
		t.Run(fmt.Sprint(stallAt), func(t *testing.T) {
			offset, emptyReads := 0, 0
			blocked := true
			source := progressReaderFunc(func(p []byte) (int, error) {
				if offset >= stallAt && blocked {
					emptyReads++
					if emptyReads > 100 {
						// A finite sentinel exposes an unbounded retry loop without
						// leaving a goroutine spinning after the test fails.
						return 0, io.ErrUnexpectedEOF
					}
					return 0, nil
				}
				end := len(stream)
				if blocked {
					end = stallAt
				}
				if offset == end {
					return 0, io.EOF
				}
				n := copy(p, stream[offset:end])
				offset += n
				return n, nil
			})
			r, err := NewReader(source)
			if stallAt != 0 {
				if err != nil {
					t.Fatal(err)
				}
				if n, gp, readErr := r.ReadPacketInto(make([]byte, 8)); n != 0 || gp != 0 {
					t.Fatalf("stalled read = (%d, %d, %v), want no packet", n, gp, readErr)
				} else {
					err = readErr
				}
			}
			if err != io.ErrNoProgress || emptyReads != 100 {
				t.Fatalf("stalled read = %v after %d empty reads, want io.ErrNoProgress after 100", err, emptyReads)
			}
			blocked = false
			if stallAt == 0 {
				r, err = NewReader(source)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, gp, err := r.ReadPacket()
			if err != nil || gp != 960 || !bytes.Equal(got, packet) {
				t.Fatalf("read after progress resumes = (%x, %d, %v)", got, gp, err)
			}
		})
	}
}

func TestReaderAcceptsTemporaryEmptyReads(t *testing.T) {
	packet := []byte{0x08, 0x01}
	stream := buildAudioPageStream(960, [][]byte{packet})
	offset, emptyReads := 0, 0
	source := progressReaderFunc(func(p []byte) (int, error) {
		if offset == len(stream) {
			return 0, io.EOF
		}
		if emptyReads < 99 {
			emptyReads++
			return 0, nil
		}
		emptyReads = 0
		p[0] = stream[offset]
		offset++
		return 1, nil
	})
	r, err := NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	got, gp, err := r.ReadPacket()
	if err != nil || gp != 960 || !bytes.Equal(got, packet) {
		t.Fatalf("ReadPacket = (%x, %d, %v)", got, gp, err)
	}
}

func TestReaderRetryAfterStalledContinuationHeader(t *testing.T) {
	const serial = 0x1234
	stream := audioPageStreamPrefix()
	stream = append(stream, readerBoundaryPage(serial, 2, 0, ^uint64(0), []byte{255}, bytes.Repeat([]byte{0x55}, 255))...)
	// The incomplete next page exposes a longer lacing table than the consumed
	// page. Its header must not replace that page while its cursor remains live.
	stallAt := len(stream) + pageHeaderSize + 2
	last := []byte{0x08, 0x01}
	stream = append(stream, readerBoundaryPage(serial, 3, PageFlagContinuation, 960, []byte{1, 2}, append([]byte{0x55}, last...))...)
	offset, blocked := 0, true
	source := progressReaderFunc(func(p []byte) (int, error) {
		if blocked && offset >= stallAt {
			return 0, nil
		}
		end := len(stream)
		if blocked {
			end = stallAt
		}
		if offset == end {
			return 0, io.EOF
		}
		n := copy(p, stream[offset:end])
		offset += n
		return n, nil
	})
	r, err := NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadPacket(); err != io.ErrNoProgress {
		t.Fatalf("stalled packet read = %v, want io.ErrNoProgress", err)
	}
	blocked = false
	// The interrupted packet is discarded, while the following complete packet
	// remains readable after the source resumes progress.
	got, gp, err := r.ReadPacket()
	if err != nil || gp != 960 || !bytes.Equal(got, last) {
		t.Fatalf("read after stalled continuation = (%x, %d, %v), want (%x, 960, nil)", got, gp, err, last)
	}
}

func TestReaderRetryAfterStalledEOSHeaderPreservesGranules(t *testing.T) {
	const serial = 0x1234
	packet := []byte{0xf8, 0x00}
	stream := audioPageStreamPrefix()
	stream = append(stream, readerBoundaryPacketPage(serial, 2, 0, 960, packet)...)
	stallAt := len(stream) + pageHeaderSize + 2
	stream = append(stream, readerBoundaryPacketPage(serial, 3, PageFlagEOS, 2500, packet, packet)...)
	offset, blocked := 0, true
	source := progressReaderFunc(func(p []byte) (int, error) {
		if blocked && offset >= stallAt {
			return 0, nil
		}
		end := len(stream)
		if blocked {
			end = stallAt
		}
		if offset == end {
			return 0, io.EOF
		}
		n := copy(p, stream[offset:end])
		offset += n
		return n, nil
	})
	r, err := NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	if got, gp, err := r.ReadPacket(); err != nil || gp != 960 || !bytes.Equal(got, packet) {
		t.Fatalf("first packet = (%x, %d, %v)", got, gp, err)
	}
	if _, _, err := r.ReadPacket(); err != io.ErrNoProgress {
		t.Fatalf("stalled packet read = %v, want io.ErrNoProgress", err)
	}
	blocked = false
	for _, want := range []uint64{1920, 2500} {
		got, gp, err := r.ReadPacket()
		if err != nil || gp != want || !bytes.Equal(got, packet) {
			t.Fatalf("read after stalled EOS header = (%x, %d, %v), want (%x, %d, nil)", got, gp, err, packet, want)
		}
	}
}

func TestReaderRetryAfterBadContinuationCRC(t *testing.T) {
	const serial = 0x1234
	stream := audioPageStreamPrefix()
	stream = append(stream, readerBoundaryPage(serial, 2, 0, ^uint64(0), []byte{255}, bytes.Repeat([]byte{0x55}, 255))...)
	next := readerBoundaryPage(serial, 3, PageFlagContinuation, 960, []byte{1, 2}, []byte{0x55, 0x08, 0x01})
	next[22] ^= 1
	stream = append(stream, next...)
	r, err := NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	// A failed page must not replace the payload while its predecessor's
	// assembly cursor still refers to the consumed 255-byte segment.
	for range 2 {
		if got, gp, err := r.ReadPacket(); err != ErrBadCRC || got != nil || gp != 0 {
			t.Fatalf("read bad continuation = (%x, %d, %v), want ErrBadCRC", got, gp, err)
		}
	}
}

func TestReaderRetryAfterStalledInterleavedPage(t *testing.T) {
	const serial = 0x1234
	stream := audioPageStreamPrefix()
	stream = append(stream, readerBoundaryPage(serial, 2, 0, ^uint64(0), []byte{255}, bytes.Repeat([]byte{0x55}, 255))...)
	stream = append(stream, readerBoundaryPage(serial+1, 42, 0, 0, []byte{1, 1}, []byte{0x11, 0x22})...)
	stallAt := len(stream)
	last := []byte{0x08, 0x01}
	stream = append(stream, readerBoundaryPage(serial, 3, PageFlagContinuation, 960, []byte{1, 2}, append([]byte{0x55}, last...))...)
	offset, blocked := 0, true
	source := progressReaderFunc(func(p []byte) (int, error) {
		if blocked && offset >= stallAt {
			return 0, nil
		}
		end := len(stream)
		if blocked {
			end = stallAt
		}
		if offset == end {
			return 0, io.EOF
		}
		n := copy(p, stream[offset:end])
		offset += n
		return n, nil
	})
	r, err := NewReader(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ReadPacket(); err != io.ErrNoProgress {
		t.Fatalf("stalled packet read = %v, want io.ErrNoProgress", err)
	}
	blocked = false
	// A skipped foreign page cannot replace the selected stream's packet cursor.
	got, gp, err := r.ReadPacket()
	if err != nil || gp != 960 || !bytes.Equal(got, last) {
		t.Fatalf("read after stalled interleaved page = (%x, %d, %v), want (%x, 960, nil)", got, gp, err, last)
	}
}
