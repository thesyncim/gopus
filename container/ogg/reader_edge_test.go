package ogg

import (
	"bytes"
	"testing"
)

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

// TestReaderAccessors_FromHeader verifies the accessors read through to a
// populated Header.
func TestReaderAccessors_FromHeader(t *testing.T) {
	r := Reader{Header: &OpusHead{
		PreSkip:    312,
		Channels:   2,
		SampleRate: 48000,
	}}

	if got := r.PreSkip(); got != 312 {
		t.Errorf("PreSkip() = %d, want 312", got)
	}
	if got := r.Channels(); got != 2 {
		t.Errorf("Channels() = %d, want 2", got)
	}
	if got := r.SampleRate(); got != 48000 {
		t.Errorf("SampleRate() = %d, want 48000", got)
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
