package ogg

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func emptyPageContinuationStream(emptyFlags byte, sequenceGap bool) ([]byte, [][]byte, []uint64) {
	const serial = 0x4e71
	first := []byte{0xf8, 0x10}
	spanning := bytes.Repeat([]byte{0x55}, 300)
	spanning[0] = 0xf8
	last := []byte{0xf8, 0x12}
	seq := uint32(3)
	if sequenceGap {
		seq++
	}
	stream := eosGranuleStream(
		readerBoundaryPage(serial, 2, 0, 960, []byte{2, 255}, append(append([]byte(nil), first...), spanning[:255]...)),
		readerBoundaryPage(serial, seq, emptyFlags, ^uint64(0), nil, nil),
		readerBoundaryPage(0xabcd, 42, 0, ^uint64(0), nil, nil),
		readerBoundaryPage(serial, seq+1, emptyFlags, ^uint64(0), nil, nil),
		readerBoundaryPage(serial, seq+2, PageFlagContinuation|PageFlagEOS, 2500, []byte{45, 2}, append(append([]byte(nil), spanning[255:]...), last...)),
	)
	if sequenceGap {
		return stream, [][]byte{first, last}, []uint64{960, 2500}
	}
	return stream, [][]byte{first, spanning, last}, []uint64{960, 1920, 2500}
}

// RFC 7845 section 3 checks the continuation flag on the next page with
// packet data. A page with no lacing entries neither continues nor abandons
// the pending packet, while a sequence gap still invalidates its prefix.
func TestReaderSpanningPacketAcrossEmptyPages(t *testing.T) {
	for _, flags := range []byte{0, PageFlagContinuation} {
		for _, gap := range []bool{false, true} {
			stream, packets, granules := emptyPageContinuationStream(flags, gap)
			for _, bounded := range []bool{false, true} {
				r, err := NewReader(bytes.NewReader(stream))
				if err != nil {
					t.Fatal(err)
				}
				dst := make([]byte, 512)
				for i, want := range packets {
					var packet []byte
					var gp uint64
					if bounded {
						var n int
						n, gp, err = r.ReadPacketInto(dst)
						packet = dst[:n]
					} else {
						packet, gp, err = r.ReadPacket()
					}
					if err != nil || !bytes.Equal(packet, want) || gp != granules[i] {
						t.Fatalf("flags=%d gap=%v bounded=%v packet %d = (%d bytes, %d, %v), want (%d bytes, %d, nil)", flags, gap, bounded, i, len(packet), gp, err, len(want), granules[i])
					}
				}
				if _, _, err := r.ReadPacket(); err != io.EOF {
					t.Fatalf("trailing read = %v, want EOF", err)
				}
				if !gap {
					if err := r.SeekGranule(1800); err != nil {
						t.Fatal(err)
					}
					packet, gp, err := r.ReadPacket()
					if err != nil || !bytes.Equal(packet, packets[1]) || gp != 1920 {
						t.Fatalf("packet after seek = (%d bytes, %d, %v), want spanning packet at 1920", len(packet), gp, err)
					}
				}
			}
		}
	}
}

func TestReadPacketIntoAcrossEmptyPagesZeroAlloc(t *testing.T) {
	for _, limit := range []int{16, 512} {
		stream, packets, _ := emptyPageContinuationStream(0, false)
		r, err := NewReader(bytes.NewReader(stream))
		if err != nil {
			t.Fatal(err)
		}
		dst := make([]byte, limit)
		read := func() {
			if err := r.SeekGranule(0); err != nil {
				t.Fatal(err)
			}
			if _, _, err := r.ReadPacketInto(dst); err != nil {
				t.Fatal(err)
			}
			n, gp, err := r.ReadPacketInto(dst)
			if limit < len(packets[1]) {
				if n != 0 || gp != 0 || !errors.Is(err, ErrPacketTooLarge) || r.GranulePos() != 1920 {
					t.Fatalf("oversized spanning packet = (%d, %d, %v), consumed granule %d", n, gp, err, r.GranulePos())
				}
			} else if err != nil || !bytes.Equal(dst[:n], packets[1]) || gp != 1920 {
				t.Fatalf("spanning packet = (%d, %d, %v), want (300, 1920, nil)", n, gp, err)
			}
			if n, gp, err := r.ReadPacketInto(dst); err != nil || !bytes.Equal(dst[:n], packets[2]) || gp != 2500 {
				t.Fatalf("last packet = (%d, %d, %v), want (2, 2500, nil)", n, gp, err)
			}
		}
		read()
		if allocs := testing.AllocsPerRun(100, read); allocs != 0 {
			t.Fatalf("limit %d: allocations = %g, want 0", limit, allocs)
		}
	}
}
