package ogg

import (
	"testing"
)

// TestOggCRC verifies the Ogg CRC-32 implementation properties.
// The implementation uses polynomial 0x04C11DB7 (not IEEE).
// This has been verified to produce correct checksums accepted by opusdec.
func TestOggCRC(t *testing.T) {
	// Verify empty data returns 0.
	t.Run("empty", func(t *testing.T) {
		got := oggCRC([]byte{})
		if got != 0 {
			t.Errorf("oggCRC([]) = 0x%08x, want 0", got)
		}
	})

	// Verify update produces same result as full computation.
	t.Run("update consistency", func(t *testing.T) {
		data := []byte("hello world")
		full := oggCRC(data)
		partial := oggCRCUpdate(oggCRC(data[:5]), data[5:])
		if full != partial {
			t.Errorf("oggCRCUpdate inconsistent: full=0x%08x, partial=0x%08x", full, partial)
		}
	})

	// Verify that different data produces different CRCs.
	t.Run("uniqueness", func(t *testing.T) {
		crc1 := oggCRC([]byte("test1"))
		crc2 := oggCRC([]byte("test2"))
		if crc1 == crc2 {
			t.Errorf("different data produced same CRC: 0x%08x", crc1)
		}
	})

	// Verify CRC changes when data changes (detect corruption).
	t.Run("corruption detection", func(t *testing.T) {
		data := []byte("OggS test data for CRC")
		original := oggCRC(data)

		corrupted := make([]byte, len(data))
		copy(corrupted, data)
		corrupted[10] ^= 0x01 // Flip one bit

		corrupted_crc := oggCRC(corrupted)
		if original == corrupted_crc {
			t.Errorf("CRC did not detect corruption")
		}
	})

	// Verify polynomial is NOT IEEE (would give different results).
	t.Run("non-IEEE polynomial", func(t *testing.T) {
		// IEEE CRC-32 for "OggS" would be different.
		// Our polynomial 0x04C11DB7 should produce 0x5fb0a94f.
		got := oggCRC([]byte("OggS"))
		expected := uint32(0x5fb0a94f)
		if got != expected {
			t.Errorf("oggCRC(OggS) = 0x%08x, want 0x%08x", got, expected)
		}
	})
}

// TestBuildSegmentTable tests segment table creation for various packet sizes.
func TestBuildSegmentTable(t *testing.T) {
	tests := []struct {
		name      string
		packetLen int
		expected  []byte
	}{
		{
			name:      "zero length",
			packetLen: 0,
			expected:  []byte{0},
		},
		{
			name:      "1 byte",
			packetLen: 1,
			expected:  []byte{1},
		},
		{
			name:      "100 bytes",
			packetLen: 100,
			expected:  []byte{100},
		},
		{
			name:      "254 bytes",
			packetLen: 254,
			expected:  []byte{254},
		},
		{
			name:      "255 bytes exact",
			packetLen: 255,
			expected:  []byte{255, 0},
		},
		{
			name:      "256 bytes",
			packetLen: 256,
			expected:  []byte{255, 1},
		},
		{
			name:      "510 bytes (2x255)",
			packetLen: 510,
			expected:  []byte{255, 255, 0},
		},
		{
			name:      "600 bytes",
			packetLen: 600,
			expected:  []byte{255, 255, 90},
		},
		{
			name:      "1000 bytes",
			packetLen: 1000,
			expected:  []byte{255, 255, 255, 235},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildSegmentTable(tc.packetLen)
			if len(got) != len(tc.expected) {
				t.Errorf("BuildSegmentTable(%d) len=%d, want %d", tc.packetLen, len(got), len(tc.expected))
				return
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("BuildSegmentTable(%d)[%d] = %d, want %d", tc.packetLen, i, got[i], tc.expected[i])
				}
			}

			// Verify total equals packet length.
			total := 0
			for _, seg := range got {
				total += int(seg)
			}
			if total != tc.packetLen {
				t.Errorf("BuildSegmentTable(%d) sums to %d, want %d", tc.packetLen, total, tc.packetLen)
			}
		})
	}
}

// TestParseSegmentTable tests segment table parsing.
func TestParseSegmentTable(t *testing.T) {
	tests := []struct {
		name     string
		segments []byte
		expected []int
	}{
		{
			name:     "empty",
			segments: []byte{},
			expected: nil,
		},
		{
			name:     "single small packet",
			segments: []byte{100},
			expected: []int{100},
		},
		{
			name:     "two small packets",
			segments: []byte{100, 50},
			expected: []int{100, 50},
		},
		{
			name:     "spanning packet",
			segments: []byte{255, 100},
			expected: []int{355},
		},
		{
			name:     "exact 255 boundary",
			segments: []byte{255, 0},
			expected: []int{255},
		},
		{
			name:     "multiple large packets",
			segments: []byte{255, 255, 90, 200},
			expected: []int{600, 200},
		},
		{
			name:     "continuation (ends with 255)",
			segments: []byte{255, 255},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseSegmentTable(tc.segments)
			if len(got) != len(tc.expected) {
				t.Errorf("ParseSegmentTable(%v) len=%d, want %d", tc.segments, len(got), len(tc.expected))
				return
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("ParseSegmentTable(%v)[%d] = %d, want %d", tc.segments, i, got[i], tc.expected[i])
				}
			}
		})
	}
}

// TestSegmentTableRoundTrip verifies BuildSegmentTable and ParseSegmentTable are inverse.
func TestSegmentTableRoundTrip(t *testing.T) {
	lengths := []int{0, 1, 100, 254, 255, 256, 510, 511, 600, 1000, 2000}
	for _, length := range lengths {
		t.Run("", func(t *testing.T) {
			segments := BuildSegmentTable(length)
			parsed := ParseSegmentTable(segments)
			if len(parsed) != 1 {
				t.Errorf("expected 1 packet, got %d", len(parsed))
				return
			}
			if parsed[0] != length {
				t.Errorf("BuildSegmentTable(%d) -> ParseSegmentTable = %d", length, parsed[0])
			}
		})
	}
}

// TestPageEncode verifies page encoding produces valid Ogg page.
func TestPageEncode(t *testing.T) {
	p := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   0,
		SerialNumber: 0x12345678,
		PageSequence: 0,
		Segments:     []byte{19}, // 19 bytes for OpusHead
		Payload:      make([]byte, 19),
	}
	copy(p.Payload, "OpusHead")

	encoded := p.Encode()

	// Verify magic.
	if string(encoded[0:4]) != "OggS" {
		t.Errorf("encoded page missing OggS magic")
	}

	// Verify version.
	if encoded[4] != 0 {
		t.Errorf("encoded page version = %d, want 0", encoded[4])
	}

	// Verify header type.
	if encoded[5] != PageFlagBOS {
		t.Errorf("encoded page header type = 0x%02x, want 0x%02x", encoded[5], PageFlagBOS)
	}

	// Verify serial number.
	if encoded[14] != 0x78 || encoded[15] != 0x56 || encoded[16] != 0x34 || encoded[17] != 0x12 {
		t.Errorf("encoded page has wrong serial number")
	}

	// Verify segment count.
	if encoded[26] != 1 {
		t.Errorf("encoded page segment count = %d, want 1", encoded[26])
	}

	// Verify total length (27 header + 1 segment + 19 payload).
	expectedLen := 27 + 1 + 19
	if len(encoded) != expectedLen {
		t.Errorf("encoded page len = %d, want %d", len(encoded), expectedLen)
	}
}

// TestParsePage tests page parsing including CRC verification.
func TestParsePage(t *testing.T) {
	// Create a page, encode it, then parse it back.
	original := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   12345,
		SerialNumber: 0xDEADBEEF,
		PageSequence: 42,
		Segments:     []byte{100},
		Payload:      make([]byte, 100),
	}
	for i := range original.Payload {
		original.Payload[i] = byte(i)
	}

	encoded := original.Encode()

	parsed, consumed, err := ParsePage(encoded)
	if err != nil {
		t.Fatalf("ParsePage failed: %v", err)
	}

	if consumed != len(encoded) {
		t.Errorf("ParsePage consumed %d bytes, want %d", consumed, len(encoded))
	}

	if parsed.Version != original.Version {
		t.Errorf("Version = %d, want %d", parsed.Version, original.Version)
	}
	if parsed.HeaderType != original.HeaderType {
		t.Errorf("HeaderType = 0x%02x, want 0x%02x", parsed.HeaderType, original.HeaderType)
	}
	if parsed.GranulePos != original.GranulePos {
		t.Errorf("GranulePos = %d, want %d", parsed.GranulePos, original.GranulePos)
	}
	if parsed.SerialNumber != original.SerialNumber {
		t.Errorf("SerialNumber = 0x%08x, want 0x%08x", parsed.SerialNumber, original.SerialNumber)
	}
	if parsed.PageSequence != original.PageSequence {
		t.Errorf("PageSequence = %d, want %d", parsed.PageSequence, original.PageSequence)
	}
	if len(parsed.Payload) != len(original.Payload) {
		t.Errorf("Payload len = %d, want %d", len(parsed.Payload), len(original.Payload))
	}
	for i := range parsed.Payload {
		if parsed.Payload[i] != original.Payload[i] {
			t.Errorf("Payload[%d] = %d, want %d", i, parsed.Payload[i], original.Payload[i])
			break
		}
	}
}

// TestParsePage_BadCRC verifies that corrupted pages are detected.
func TestParsePage_BadCRC(t *testing.T) {
	p := &Page{
		Version:      0,
		HeaderType:   0,
		GranulePos:   0,
		SerialNumber: 1,
		PageSequence: 0,
		Segments:     []byte{10},
		Payload:      []byte("0123456789"),
	}

	encoded := p.Encode()

	// Corrupt the CRC.
	encoded[22] ^= 0xFF

	_, _, err := ParsePage(encoded)
	if err != ErrBadCRC {
		t.Errorf("ParsePage with bad CRC: got error %v, want ErrBadCRC", err)
	}
}

// TestParsePage_Truncated verifies that truncated data is rejected.
func TestParsePage_Truncated(t *testing.T) {
	// Too short for header.
	_, _, err := ParsePage([]byte("OggS"))
	if err != ErrInvalidPage {
		t.Errorf("ParsePage with truncated header: got error %v, want ErrInvalidPage", err)
	}

	// Missing magic.
	_, _, err = ParsePage(make([]byte, 100))
	if err != ErrInvalidPage {
		t.Errorf("ParsePage with no magic: got error %v, want ErrInvalidPage", err)
	}
}

// TestPagePackets tests extracting packets from a page.
func TestPagePackets(t *testing.T) {
	// Page with multiple packets.
	p := &Page{
		Segments: []byte{10, 20, 30},
		Payload:  make([]byte, 60),
	}
	for i := range p.Payload {
		p.Payload[i] = byte(i)
	}

	packets := p.Packets()
	if len(packets) != 3 {
		t.Errorf("got %d packets, want 3", len(packets))
		return
	}

	if len(packets[0]) != 10 {
		t.Errorf("packet 0 len = %d, want 10", len(packets[0]))
	}
	if len(packets[1]) != 20 {
		t.Errorf("packet 1 len = %d, want 20", len(packets[1]))
	}
	if len(packets[2]) != 30 {
		t.Errorf("packet 2 len = %d, want 30", len(packets[2]))
	}
}

// TestPagePackets_LargePacket tests a packet spanning multiple segments.
func TestPagePackets_LargePacket(t *testing.T) {
	packetLen := 600
	segments := BuildSegmentTable(packetLen)
	payload := make([]byte, packetLen)
	for i := range payload {
		payload[i] = byte(i)
	}

	p := &Page{
		Segments: segments,
		Payload:  payload,
	}

	packets := p.Packets()
	if len(packets) != 1 {
		t.Errorf("got %d packets, want 1", len(packets))
		return
	}
	if len(packets[0]) != packetLen {
		t.Errorf("packet len = %d, want %d", len(packets[0]), packetLen)
	}
}

// TestPageFlags tests BOS, EOS, and continuation flag methods.
func TestPageFlags(t *testing.T) {
	p := &Page{HeaderType: PageFlagBOS}
	if !p.IsBOS() {
		t.Error("IsBOS() = false, want true")
	}
	if p.IsEOS() {
		t.Error("IsEOS() = true, want false")
	}
	if p.IsContinuation() {
		t.Error("IsContinuation() = true, want false")
	}

	p.HeaderType = PageFlagEOS
	if p.IsBOS() {
		t.Error("IsBOS() = true, want false")
	}
	if !p.IsEOS() {
		t.Error("IsEOS() = false, want true")
	}

	p.HeaderType = PageFlagContinuation
	if !p.IsContinuation() {
		t.Error("IsContinuation() = false, want true")
	}

	// Combined flags.
	p.HeaderType = PageFlagBOS | PageFlagEOS
	if !p.IsBOS() || !p.IsEOS() {
		t.Error("combined flags not working")
	}
}
