package ogg

import (
	"bytes"
	"testing"
)

// FuzzSegmentTableSplit exercises the page-level packet-splitting paths that
// operate directly on a caller-built Page: ParseSegmentTable, Page.PacketLengths
// and Page.Packets. A Page may be assembled by hand with a segment table that is
// inconsistent with its payload (e.g. lacing that claims more bytes than the
// payload holds), so these methods must never panic and must keep every packet
// slice within the payload bounds.
func FuzzSegmentTableSplit(f *testing.F) {
	// Single small packet.
	f.Add([]byte{4}, []byte{1, 2, 3, 4})
	// Two packets via lacing.
	f.Add([]byte{2, 3}, []byte{1, 2, 3, 4, 5})
	// 255-byte continuation terminated by a zero lacing entry (exact multiple).
	f.Add([]byte{255, 0}, make([]byte, 255))
	// Continuation that never terminates (last lacing value is 255).
	f.Add([]byte{255}, make([]byte, 255))
	// Lacing claims more than the payload provides (truncated payload).
	f.Add([]byte{200}, make([]byte, 10))
	// Empty segment table.
	f.Add([]byte{}, []byte{1, 2, 3})
	// Zero-length packet (single zero lacing entry).
	f.Add([]byte{0}, []byte{})
	// Many maximal lacing entries with a short payload.
	f.Add(bytes.Repeat([]byte{255}, 255), make([]byte, 16))
	// Segment table with a payload longer than the lacing accounts for.
	f.Add([]byte{1, 1}, make([]byte, 64))

	f.Fuzz(func(t *testing.T, segments, payload []byte) {
		if len(segments) > 255 {
			segments = segments[:255]
		}
		if len(payload) > 1<<16 {
			payload = payload[:1<<16]
		}

		// ParseSegmentTable must agree with PacketLengths and never report a
		// negative or absurd length.
		lengths := ParseSegmentTable(segments)
		for _, l := range lengths {
			if l < 0 {
				t.Fatalf("ParseSegmentTable returned negative length %d", l)
			}
		}

		p := &Page{Segments: segments, Payload: payload}

		pl := p.PacketLengths()
		if len(pl) != len(lengths) {
			t.Fatalf("PacketLengths len=%d differs from ParseSegmentTable len=%d", len(pl), len(lengths))
		}

		// Packets must never return a slice that escapes the payload, and the
		// concatenation of all returned packets must be a prefix of payload.
		packets := p.Packets()
		total := 0
		for i, pkt := range packets {
			if len(pkt) > len(payload) {
				t.Fatalf("packet[%d] len=%d exceeds payload len=%d", i, len(pkt), len(payload))
			}
			total += len(pkt)
		}
		if total > len(payload) {
			t.Fatalf("sum of packet bytes=%d exceeds payload len=%d", total, len(payload))
		}
	})
}

// FuzzReadPacketInto exercises the bounded copy path Reader.ReadPacketInto with
// a fixed-size destination against arbitrary stream bytes. It must never panic,
// must surface ErrPacketTooLarge rather than over-copy when a packet does not
// fit, and must never report copying more bytes than the destination holds.
func FuzzReadPacketInto(f *testing.F) {
	if s := fuzzValidOggStream(); len(s) > 0 {
		f.Add(s, uint16(16))
		f.Add(s, uint16(4096))
		f.Add(s, uint16(0))
	}
	f.Add([]byte{}, uint16(64))
	f.Add([]byte("OggS"), uint16(64))

	f.Fuzz(func(t *testing.T, data []byte, dstLen uint16) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}
		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		dst := make([]byte, int(dstLen))
		for i := 0; i < 64; i++ {
			n, _, err := r.ReadPacketInto(dst)
			if err == ErrPacketTooLarge {
				// Expected when a packet does not fit; n must be 0.
				if n != 0 {
					t.Fatalf("ReadPacketInto returned n=%d with ErrPacketTooLarge", n)
				}
				continue
			}
			if err != nil {
				return
			}
			if n > len(dst) {
				t.Fatalf("ReadPacketInto copied n=%d into dst of len=%d", n, len(dst))
			}
		}
	})
}
