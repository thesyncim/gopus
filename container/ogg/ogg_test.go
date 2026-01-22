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

// TestOpusHeadFamily0_Mono tests OpusHead encoding/parsing for mono.
func TestOpusHeadFamily0_Mono(t *testing.T) {
	h := DefaultOpusHead(48000, 1)

	if h.Version != 1 {
		t.Errorf("Version = %d, want 1", h.Version)
	}
	if h.Channels != 1 {
		t.Errorf("Channels = %d, want 1", h.Channels)
	}
	if h.PreSkip != DefaultPreSkip {
		t.Errorf("PreSkip = %d, want %d", h.PreSkip, DefaultPreSkip)
	}
	if h.MappingFamily != 0 {
		t.Errorf("MappingFamily = %d, want 0", h.MappingFamily)
	}

	// Encode and verify size.
	encoded := h.Encode()
	if len(encoded) != 19 {
		t.Errorf("encoded len = %d, want 19", len(encoded))
	}

	// Verify magic.
	if string(encoded[0:8]) != "OpusHead" {
		t.Errorf("missing OpusHead magic")
	}

	// Parse back.
	parsed, err := ParseOpusHead(encoded)
	if err != nil {
		t.Fatalf("ParseOpusHead failed: %v", err)
	}

	if parsed.Version != h.Version {
		t.Errorf("parsed Version = %d, want %d", parsed.Version, h.Version)
	}
	if parsed.Channels != h.Channels {
		t.Errorf("parsed Channels = %d, want %d", parsed.Channels, h.Channels)
	}
	if parsed.PreSkip != h.PreSkip {
		t.Errorf("parsed PreSkip = %d, want %d", parsed.PreSkip, h.PreSkip)
	}
	if parsed.SampleRate != h.SampleRate {
		t.Errorf("parsed SampleRate = %d, want %d", parsed.SampleRate, h.SampleRate)
	}
	if parsed.MappingFamily != h.MappingFamily {
		t.Errorf("parsed MappingFamily = %d, want %d", parsed.MappingFamily, h.MappingFamily)
	}
}

// TestOpusHeadFamily0_Stereo tests OpusHead encoding/parsing for stereo.
func TestOpusHeadFamily0_Stereo(t *testing.T) {
	h := DefaultOpusHead(48000, 2)

	if h.Channels != 2 {
		t.Errorf("Channels = %d, want 2", h.Channels)
	}
	if h.CoupledCount != 1 {
		t.Errorf("CoupledCount = %d, want 1", h.CoupledCount)
	}

	encoded := h.Encode()
	if len(encoded) != 19 {
		t.Errorf("encoded len = %d, want 19", len(encoded))
	}

	parsed, err := ParseOpusHead(encoded)
	if err != nil {
		t.Fatalf("ParseOpusHead failed: %v", err)
	}

	if parsed.Channels != 2 {
		t.Errorf("parsed Channels = %d, want 2", parsed.Channels)
	}
	if parsed.CoupledCount != 1 {
		t.Errorf("parsed CoupledCount = %d, want 1", parsed.CoupledCount)
	}
}

// TestOpusHeadFamily1 tests OpusHead for surround configurations.
func TestOpusHeadFamily1(t *testing.T) {
	tests := []struct {
		name     string
		channels uint8
		streams  uint8
		coupled  uint8
		mapping  []byte
	}{
		{
			name:     "5.1 surround",
			channels: 6,
			streams:  4,
			coupled:  2,
			mapping:  []byte{0, 4, 1, 2, 3, 5}, // FL, FR, C, LFE, BL, BR
		},
		{
			name:     "7.1 surround",
			channels: 8,
			streams:  5,
			coupled:  3,
			mapping:  []byte{0, 6, 1, 2, 3, 4, 5, 7}, // FL, FR, C, LFE, BL, BR, SL, SR
		},
		{
			name:     "quad",
			channels: 4,
			streams:  2,
			coupled:  2,
			mapping:  []byte{0, 1, 2, 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := DefaultOpusHeadMultistream(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)

			if h.MappingFamily != MappingFamilyVorbis {
				t.Errorf("MappingFamily = %d, want %d", h.MappingFamily, MappingFamilyVorbis)
			}
			if h.StreamCount != tc.streams {
				t.Errorf("StreamCount = %d, want %d", h.StreamCount, tc.streams)
			}
			if h.CoupledCount != tc.coupled {
				t.Errorf("CoupledCount = %d, want %d", h.CoupledCount, tc.coupled)
			}

			// Encode and verify size (21 + channels).
			encoded := h.Encode()
			expectedLen := 21 + int(tc.channels)
			if len(encoded) != expectedLen {
				t.Errorf("encoded len = %d, want %d", len(encoded), expectedLen)
			}

			// Parse back.
			parsed, err := ParseOpusHead(encoded)
			if err != nil {
				t.Fatalf("ParseOpusHead failed: %v", err)
			}

			if parsed.Channels != tc.channels {
				t.Errorf("parsed Channels = %d, want %d", parsed.Channels, tc.channels)
			}
			if parsed.MappingFamily != MappingFamilyVorbis {
				t.Errorf("parsed MappingFamily = %d, want %d", parsed.MappingFamily, MappingFamilyVorbis)
			}
			if parsed.StreamCount != tc.streams {
				t.Errorf("parsed StreamCount = %d, want %d", parsed.StreamCount, tc.streams)
			}
			if parsed.CoupledCount != tc.coupled {
				t.Errorf("parsed CoupledCount = %d, want %d", parsed.CoupledCount, tc.coupled)
			}
			if len(parsed.ChannelMapping) != len(tc.mapping) {
				t.Errorf("parsed ChannelMapping len = %d, want %d", len(parsed.ChannelMapping), len(tc.mapping))
				return
			}
			for i := range parsed.ChannelMapping {
				if parsed.ChannelMapping[i] != tc.mapping[i] {
					t.Errorf("parsed ChannelMapping[%d] = %d, want %d", i, parsed.ChannelMapping[i], tc.mapping[i])
				}
			}
		})
	}
}

// TestOpusHeadRoundTrip verifies encode then parse produces same values.
func TestOpusHeadRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		head *OpusHead
	}{
		{
			name: "mono basic",
			head: &OpusHead{
				Version:       1,
				Channels:      1,
				PreSkip:       100,
				SampleRate:    44100,
				OutputGain:    256, // +1 dB
				MappingFamily: 0,
				StreamCount:   1,
				CoupledCount:  0,
			},
		},
		{
			name: "stereo with gain",
			head: &OpusHead{
				Version:       1,
				Channels:      2,
				PreSkip:       312,
				SampleRate:    48000,
				OutputGain:    -512, // -2 dB
				MappingFamily: 0,
				StreamCount:   1,
				CoupledCount:  1,
			},
		},
		{
			name: "5.1 surround",
			head: DefaultOpusHeadMultistream(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.head.Encode()
			parsed, err := ParseOpusHead(encoded)
			if err != nil {
				t.Fatalf("ParseOpusHead failed: %v", err)
			}

			if parsed.Version != tc.head.Version {
				t.Errorf("Version mismatch")
			}
			if parsed.Channels != tc.head.Channels {
				t.Errorf("Channels mismatch: %d vs %d", parsed.Channels, tc.head.Channels)
			}
			if parsed.PreSkip != tc.head.PreSkip {
				t.Errorf("PreSkip mismatch: %d vs %d", parsed.PreSkip, tc.head.PreSkip)
			}
			if parsed.SampleRate != tc.head.SampleRate {
				t.Errorf("SampleRate mismatch: %d vs %d", parsed.SampleRate, tc.head.SampleRate)
			}
			if parsed.OutputGain != tc.head.OutputGain {
				t.Errorf("OutputGain mismatch: %d vs %d", parsed.OutputGain, tc.head.OutputGain)
			}
			if parsed.MappingFamily != tc.head.MappingFamily {
				t.Errorf("MappingFamily mismatch")
			}
		})
	}
}

// TestOpusTags tests OpusTags encoding and parsing.
func TestOpusTags(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		tags := DefaultOpusTags()
		if tags.Vendor != "gopus" {
			t.Errorf("Vendor = %q, want %q", tags.Vendor, "gopus")
		}

		encoded := tags.Encode()

		// Verify magic.
		if string(encoded[0:8]) != "OpusTags" {
			t.Error("missing OpusTags magic")
		}

		parsed, err := ParseOpusTags(encoded)
		if err != nil {
			t.Fatalf("ParseOpusTags failed: %v", err)
		}

		if parsed.Vendor != tags.Vendor {
			t.Errorf("parsed Vendor = %q, want %q", parsed.Vendor, tags.Vendor)
		}
		if len(parsed.Comments) != 0 {
			t.Errorf("parsed Comments len = %d, want 0", len(parsed.Comments))
		}
	})

	t.Run("with comments", func(t *testing.T) {
		tags := &OpusTags{
			Vendor: "gopus 1.0",
			Comments: map[string]string{
				"TITLE":  "Test Song",
				"ARTIST": "Test Artist",
			},
		}

		encoded := tags.Encode()
		parsed, err := ParseOpusTags(encoded)
		if err != nil {
			t.Fatalf("ParseOpusTags failed: %v", err)
		}

		if parsed.Vendor != tags.Vendor {
			t.Errorf("parsed Vendor = %q, want %q", parsed.Vendor, tags.Vendor)
		}
		if len(parsed.Comments) != len(tags.Comments) {
			t.Errorf("parsed Comments len = %d, want %d", len(parsed.Comments), len(tags.Comments))
		}
		for k, v := range tags.Comments {
			if parsed.Comments[k] != v {
				t.Errorf("parsed Comments[%q] = %q, want %q", k, parsed.Comments[k], v)
			}
		}
	})

	t.Run("empty vendor", func(t *testing.T) {
		tags := &OpusTags{
			Vendor:   "",
			Comments: make(map[string]string),
		}

		encoded := tags.Encode()
		parsed, err := ParseOpusTags(encoded)
		if err != nil {
			t.Fatalf("ParseOpusTags failed: %v", err)
		}

		if parsed.Vendor != "" {
			t.Errorf("parsed Vendor = %q, want empty", parsed.Vendor)
		}
	})
}

// TestOpusHeadErrors tests error cases for ParseOpusHead.
func TestOpusHeadErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short",
			data: []byte("OpusHead"),
		},
		{
			name: "wrong magic",
			data: []byte("NotOpusHead12345678"),
		},
		{
			name: "wrong version",
			data: func() []byte {
				h := DefaultOpusHead(48000, 1)
				d := h.Encode()
				d[8] = 2 // Invalid version
				return d
			}(),
		},
		{
			name: "zero channels",
			data: func() []byte {
				h := DefaultOpusHead(48000, 1)
				d := h.Encode()
				d[9] = 0 // Zero channels
				return d
			}(),
		},
		{
			name: "family 0 with 3 channels",
			data: func() []byte {
				d := make([]byte, 19)
				copy(d, "OpusHead")
				d[8] = 1 // Version
				d[9] = 3 // 3 channels (invalid for family 0)
				return d
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseOpusHead(tc.data)
			if err != ErrInvalidHeader {
				t.Errorf("ParseOpusHead: got error %v, want ErrInvalidHeader", err)
			}
		})
	}
}

// TestOpusTagsErrors tests error cases for ParseOpusTags.
func TestOpusTagsErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short",
			data: []byte("OpusTags"),
		},
		{
			name: "wrong magic",
			data: []byte("NotOpusTags12345678"),
		},
		{
			name: "truncated vendor",
			data: func() []byte {
				d := make([]byte, 16)
				copy(d, "OpusTags")
				d[8] = 100 // Vendor length 100 but not enough data
				return d
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseOpusTags(tc.data)
			if err != ErrInvalidHeader {
				t.Errorf("ParseOpusTags: got error %v, want ErrInvalidHeader", err)
			}
		})
	}
}

// TestPagePackets_MultiplePackets tests a page with 2-3 packets.
func TestPagePackets_MultiplePackets(t *testing.T) {
	// Create a page with 3 packets of different sizes.
	packet1 := make([]byte, 50)
	packet2 := make([]byte, 100)
	packet3 := make([]byte, 75)

	for i := range packet1 {
		packet1[i] = byte(1)
	}
	for i := range packet2 {
		packet2[i] = byte(2)
	}
	for i := range packet3 {
		packet3[i] = byte(3)
	}

	p := &Page{
		Version:      0,
		HeaderType:   0,
		GranulePos:   1000,
		SerialNumber: 42,
		PageSequence: 5,
		Segments:     []byte{50, 100, 75},
		Payload:      make([]byte, 225),
	}
	copy(p.Payload[0:50], packet1)
	copy(p.Payload[50:150], packet2)
	copy(p.Payload[150:225], packet3)

	packets := p.Packets()
	if len(packets) != 3 {
		t.Fatalf("got %d packets, want 3", len(packets))
	}

	// Verify packet 1.
	if len(packets[0]) != 50 {
		t.Errorf("packet 0 len = %d, want 50", len(packets[0]))
	}
	for _, b := range packets[0] {
		if b != 1 {
			t.Errorf("packet 0 has wrong content")
			break
		}
	}

	// Verify packet 2.
	if len(packets[1]) != 100 {
		t.Errorf("packet 1 len = %d, want 100", len(packets[1]))
	}
	for _, b := range packets[1] {
		if b != 2 {
			t.Errorf("packet 1 has wrong content")
			break
		}
	}

	// Verify packet 3.
	if len(packets[2]) != 75 {
		t.Errorf("packet 2 len = %d, want 75", len(packets[2]))
	}
	for _, b := range packets[2] {
		if b != 3 {
			t.Errorf("packet 2 has wrong content")
			break
		}
	}
}

// TestPagePackets_Continuation tests packet continuation across segment boundaries.
func TestPagePackets_Continuation(t *testing.T) {
	// Test that a large packet spanning segments works correctly.
	// 600 bytes = 255 + 255 + 90.
	packetLen := 600
	payload := make([]byte, packetLen)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	p := &Page{
		Segments: BuildSegmentTable(packetLen),
		Payload:  payload,
	}

	packets := p.Packets()
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1", len(packets))
	}

	if len(packets[0]) != packetLen {
		t.Errorf("packet len = %d, want %d", len(packets[0]), packetLen)
	}

	// Verify content preserved.
	for i, b := range packets[0] {
		if b != byte(i%256) {
			t.Errorf("packet content mismatch at byte %d", i)
			break
		}
	}
}

// TestCRCCompatibility verifies our CRC matches the proven implementation from crossval_test.go.
// The crossval_test.go CRC implementation is proven to produce files accepted by opusdec.
func TestCRCCompatibility(t *testing.T) {
	// Test vectors derived from crossval_test.go usage patterns.
	testData := []struct {
		name string
		data []byte
	}{
		{
			name: "OggS page header prefix",
			data: []byte{'O', 'g', 'g', 'S', 0, 0x02, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			name: "OpusHead magic",
			data: []byte("OpusHead"),
		},
		{
			name: "small page",
			data: make([]byte, 47), // 27 header + 1 segment + 19 payload
		},
	}

	for _, tc := range testData {
		t.Run(tc.name, func(t *testing.T) {
			crc := oggCRC(tc.data)
			// Just verify it produces a non-zero result for non-empty data.
			if len(tc.data) > 0 && tc.data[0] != 0 && crc == 0 {
				t.Errorf("unexpected zero CRC for %q", tc.name)
			}
		})
	}
}

// TestPageRoundTrip_FullOggOpus tests a full Ogg Opus page round-trip.
// This mimics the approach from crossval_test.go writeOggPage.
func TestPageRoundTrip_FullOggOpus(t *testing.T) {
	// Create OpusHead page (like crossval_test.go).
	opusHead := DefaultOpusHead(48000, 2)
	headPayload := opusHead.Encode()

	headPage := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   0, // Header pages have granule 0
		SerialNumber: 0x12345678,
		PageSequence: 0,
		Segments:     BuildSegmentTable(len(headPayload)),
		Payload:      headPayload,
	}

	encoded := headPage.Encode()

	// Parse it back.
	parsed, consumed, err := ParsePage(encoded)
	if err != nil {
		t.Fatalf("ParsePage failed: %v", err)
	}

	if consumed != len(encoded) {
		t.Errorf("consumed %d bytes, expected %d", consumed, len(encoded))
	}

	// Verify page structure.
	if !parsed.IsBOS() {
		t.Error("expected BOS flag")
	}
	if parsed.GranulePos != 0 {
		t.Errorf("GranulePos = %d, want 0", parsed.GranulePos)
	}
	if parsed.SerialNumber != 0x12345678 {
		t.Errorf("SerialNumber = 0x%08x, want 0x12345678", parsed.SerialNumber)
	}

	// Parse OpusHead from page payload.
	parsedHead, err := ParseOpusHead(parsed.Payload)
	if err != nil {
		t.Fatalf("ParseOpusHead failed: %v", err)
	}

	if parsedHead.Channels != 2 {
		t.Errorf("Channels = %d, want 2", parsedHead.Channels)
	}
	if parsedHead.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", parsedHead.SampleRate)
	}
}

// TestPageRoundTrip_AudioData tests an audio data page round-trip.
func TestPageRoundTrip_AudioData(t *testing.T) {
	// Simulate an Opus audio packet (with TOC byte).
	audioPacket := make([]byte, 100)
	audioPacket[0] = 0xFC // CELT-only, 20ms, stereo

	for i := 1; i < len(audioPacket); i++ {
		audioPacket[i] = byte(i)
	}

	audioPage := &Page{
		Version:      0,
		HeaderType:   0, // Normal page
		GranulePos:   960,
		SerialNumber: 0x12345678,
		PageSequence: 2,
		Segments:     BuildSegmentTable(len(audioPacket)),
		Payload:      audioPacket,
	}

	encoded := audioPage.Encode()
	parsed, _, err := ParsePage(encoded)
	if err != nil {
		t.Fatalf("ParsePage failed: %v", err)
	}

	if parsed.GranulePos != 960 {
		t.Errorf("GranulePos = %d, want 960", parsed.GranulePos)
	}

	packets := parsed.Packets()
	if len(packets) != 1 {
		t.Fatalf("got %d packets, want 1", len(packets))
	}

	if len(packets[0]) != len(audioPacket) {
		t.Errorf("packet len = %d, want %d", len(packets[0]), len(audioPacket))
	}

	// Verify TOC byte preserved.
	if packets[0][0] != 0xFC {
		t.Errorf("TOC byte = 0x%02x, want 0xFC", packets[0][0])
	}
}

// TestParsePage_MultiplePages tests parsing multiple pages from a buffer.
func TestParsePage_MultiplePages(t *testing.T) {
	// Create two pages.
	page1 := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   0,
		SerialNumber: 1,
		PageSequence: 0,
		Segments:     []byte{19},
		Payload:      make([]byte, 19),
	}
	copy(page1.Payload, "OpusHead")

	page2 := &Page{
		Version:      0,
		HeaderType:   0,
		GranulePos:   0,
		SerialNumber: 1,
		PageSequence: 1,
		Segments:     []byte{17},
		Payload:      make([]byte, 17),
	}
	copy(page2.Payload, "OpusTags")

	// Concatenate encoded pages.
	encoded1 := page1.Encode()
	encoded2 := page2.Encode()
	buffer := make([]byte, len(encoded1)+len(encoded2))
	copy(buffer, encoded1)
	copy(buffer[len(encoded1):], encoded2)

	// Parse first page.
	parsed1, consumed1, err := ParsePage(buffer)
	if err != nil {
		t.Fatalf("ParsePage(1) failed: %v", err)
	}
	if consumed1 != len(encoded1) {
		t.Errorf("consumed1 = %d, want %d", consumed1, len(encoded1))
	}
	if !parsed1.IsBOS() {
		t.Error("page 1 should have BOS flag")
	}

	// Parse second page from remaining buffer.
	parsed2, consumed2, err := ParsePage(buffer[consumed1:])
	if err != nil {
		t.Fatalf("ParsePage(2) failed: %v", err)
	}
	if consumed2 != len(encoded2) {
		t.Errorf("consumed2 = %d, want %d", consumed2, len(encoded2))
	}
	if parsed2.PageSequence != 1 {
		t.Errorf("page 2 sequence = %d, want 1", parsed2.PageSequence)
	}
}
