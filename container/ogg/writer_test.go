package ogg

import (
	"bytes"
	"testing"
)

// TestNewWriter_Mono tests creating a mono writer.
func TestNewWriter_Mono(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Verify headers were written.
	if buf.Len() == 0 {
		t.Error("expected headers to be written")
	}

	// Should have written 2 pages (OpusHead + OpusTags).
	if w.PageCount() != 2 {
		t.Errorf("PageCount = %d, want 2", w.PageCount())
	}

	// Verify BOS page has OpusHead magic.
	data := buf.Bytes()
	if string(data[0:4]) != "OggS" {
		t.Error("missing OggS magic")
	}

	// Find OpusHead in payload.
	found := false
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			found = true
			// Verify channels.
			if data[i+9] != 1 {
				t.Errorf("OpusHead channels = %d, want 1", data[i+9])
			}
			break
		}
	}
	if !found {
		t.Error("OpusHead not found in output")
	}
}

// TestNewWriter_Stereo tests creating a stereo writer.
func TestNewWriter_Stereo(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if w.PageCount() != 2 {
		t.Errorf("PageCount = %d, want 2", w.PageCount())
	}

	// Verify serial number is non-zero (random).
	if w.Serial() == 0 {
		t.Log("Serial is 0 (possible but unlikely)")
	}

	// Find OpusHead and verify channels.
	data := buf.Bytes()
	for i := 0; i < len(data)-8; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			if data[i+9] != 2 {
				t.Errorf("OpusHead channels = %d, want 2", data[i+9])
			}
			break
		}
	}
}

// TestNewWriter_InvalidChannels tests that invalid channel counts are rejected.
func TestNewWriter_InvalidChannels(t *testing.T) {
	var buf bytes.Buffer

	// Zero channels.
	_, err := NewWriter(&buf, 48000, 0)
	if err == nil {
		t.Error("expected error for 0 channels")
	}

	// 3 channels with mapping family 0.
	_, err = NewWriter(&buf, 48000, 3)
	if err == nil {
		t.Error("expected error for 3 channels (requires family 1)")
	}

	// 255 channels with mapping family 0.
	_, err = NewWriter(&buf, 48000, 255)
	if err == nil {
		t.Error("expected error for 255 channels")
	}
}

// TestWritePacket_Single tests writing a single packet.
func TestWritePacket_Single(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write a test packet.
	packet := make([]byte, 100)
	packet[0] = 0xFC // Opus TOC byte
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i)
	}

	err = w.WritePacket(packet, 960) // 20ms at 48kHz
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Should have 3 pages now (head + tags + audio).
	if w.PageCount() != 3 {
		t.Errorf("PageCount = %d, want 3", w.PageCount())
	}

	// Verify granule position updated.
	if w.GranulePos() != 960 {
		t.Errorf("GranulePos = %d, want 960", w.GranulePos())
	}

	// Parse the output and verify page structure.
	data := buf.Bytes()
	offset := 0
	pageNum := 0
	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			t.Fatalf("ParsePage at offset %d failed: %v", offset, err)
		}

		switch pageNum {
		case 0:
			// OpusHead page.
			if !page.IsBOS() {
				t.Error("page 0 should have BOS flag")
			}
			if page.GranulePos != 0 {
				t.Errorf("page 0 granule = %d, want 0", page.GranulePos)
			}
		case 1:
			// OpusTags page.
			if page.IsBOS() {
				t.Error("page 1 should not have BOS flag")
			}
			if page.GranulePos != 0 {
				t.Errorf("page 1 granule = %d, want 0", page.GranulePos)
			}
		case 2:
			// Audio page.
			if page.GranulePos != 960 {
				t.Errorf("page 2 granule = %d, want 960", page.GranulePos)
			}
			packets := page.Packets()
			if len(packets) != 1 {
				t.Errorf("page 2 packet count = %d, want 1", len(packets))
			}
			if len(packets[0]) != len(packet) {
				t.Errorf("packet len = %d, want %d", len(packets[0]), len(packet))
			}
		}

		offset += consumed
		pageNum++
	}

	if pageNum != 3 {
		t.Errorf("parsed %d pages, want 3", pageNum)
	}
}

// TestWritePacket_Multiple tests writing multiple packets.
func TestWritePacket_Multiple(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write 10 packets.
	for i := 0; i < 10; i++ {
		packet := make([]byte, 50+i*10)
		packet[0] = 0xFC
		err = w.WritePacket(packet, 960)
		if err != nil {
			t.Fatalf("WritePacket %d failed: %v", i, err)
		}
	}

	// Should have 12 pages (2 headers + 10 audio).
	if w.PageCount() != 12 {
		t.Errorf("PageCount = %d, want 12", w.PageCount())
	}

	// Verify granule position.
	expectedGranule := uint64(960 * 10)
	if w.GranulePos() != expectedGranule {
		t.Errorf("GranulePos = %d, want %d", w.GranulePos(), expectedGranule)
	}

	// Parse and verify granule positions increase.
	data := buf.Bytes()
	offset := 0
	pageNum := 0
	var lastGranule uint64

	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			break
		}

		if pageNum >= 2 { // Skip header pages.
			// Audio pages should have increasing granule.
			expectedPageGranule := uint64((pageNum - 1) * 960)
			if page.GranulePos != expectedPageGranule {
				t.Errorf("page %d granule = %d, want %d", pageNum, page.GranulePos, expectedPageGranule)
			}
			if page.GranulePos < lastGranule {
				t.Errorf("page %d granule %d < previous %d", pageNum, page.GranulePos, lastGranule)
			}
			lastGranule = page.GranulePos
		}

		offset += consumed
		pageNum++
	}
}

// TestWritePacket_LargePacket tests handling packets > 255 bytes.
func TestWritePacket_LargePacket(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write a large packet (600 bytes, spanning multiple segments).
	packet := make([]byte, 600)
	packet[0] = 0xFC
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i % 256)
	}

	err = w.WritePacket(packet, 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Parse the audio page and verify packet.
	data := buf.Bytes()
	offset := 0

	for i := 0; i < 3; i++ { // Skip to page 3 (first audio page).
		_, consumed, err := ParsePage(data[offset:])
		if err != nil {
			t.Fatalf("ParsePage failed: %v", err)
		}
		offset += consumed
	}

	// This should not happen in our test, but verify we got to the audio page.
	// Actually, we parsed 3 pages already (0, 1, 2), so we need to back up.
	offset = 0
	pageNum := 0
	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			break
		}
		if pageNum == 2 { // Third page (index 2) is audio.
			packets := page.Packets()
			if len(packets) != 1 {
				t.Errorf("expected 1 packet, got %d", len(packets))
				break
			}
			if len(packets[0]) != 600 {
				t.Errorf("packet len = %d, want 600", len(packets[0]))
			}
			// Verify segment table spans multiple segments.
			if len(page.Segments) < 3 {
				t.Errorf("expected at least 3 segments for 600-byte packet, got %d", len(page.Segments))
			}
			// Verify content.
			for i := 1; i < len(packets[0]); i++ {
				if packets[0][i] != byte(i%256) {
					t.Errorf("packet content mismatch at byte %d", i)
					break
				}
			}
		}
		offset += consumed
		pageNum++
	}
}

// TestClose tests that Close writes the EOS page.
func TestClose(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write one packet.
	err = w.WritePacket(make([]byte, 50), 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Close the writer.
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should have 4 pages (head + tags + audio + EOS).
	if w.PageCount() != 4 {
		t.Errorf("PageCount = %d, want 4", w.PageCount())
	}

	// Find EOS page.
	data := buf.Bytes()
	offset := 0
	foundEOS := false

	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			break
		}
		if page.IsEOS() {
			foundEOS = true
			// EOS page should have same granule as last audio.
			if page.GranulePos != 960 {
				t.Errorf("EOS page granule = %d, want 960", page.GranulePos)
			}
		}
		offset += consumed
	}

	if !foundEOS {
		t.Error("EOS page not found")
	}

	// Writing after close should fail.
	err = w.WritePacket(make([]byte, 50), 960)
	if err == nil {
		t.Error("expected error writing after Close")
	}
}

// TestWriterWithConfig_Multistream tests creating a 5.1 writer with family 1 config.
func TestWriterWithConfig_Multistream(t *testing.T) {
	var buf bytes.Buffer

	// 5.1 surround: 6 channels, 4 streams, 2 coupled.
	mapping := []byte{0, 4, 1, 2, 3, 5} // FL, FR, C, LFE, BL, BR

	config := WriterConfig{
		SampleRate:     48000,
		Channels:       6,
		PreSkip:        312,
		OutputGain:     0,
		MappingFamily:  MappingFamilyVorbis,
		StreamCount:    4,
		CoupledCount:   2,
		ChannelMapping: mapping,
	}

	w, err := NewWriterWithConfig(&buf, config)
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	// Verify headers written.
	if w.PageCount() != 2 {
		t.Errorf("PageCount = %d, want 2", w.PageCount())
	}

	// Find OpusHead and verify multistream config.
	data := buf.Bytes()
	for i := 0; i < len(data)-21; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			// Verify mapping family.
			if data[i+18] != MappingFamilyVorbis {
				t.Errorf("MappingFamily = %d, want %d", data[i+18], MappingFamilyVorbis)
			}
			// Verify channels.
			if data[i+9] != 6 {
				t.Errorf("Channels = %d, want 6", data[i+9])
			}
			// Verify stream count.
			if data[i+19] != 4 {
				t.Errorf("StreamCount = %d, want 4", data[i+19])
			}
			// Verify coupled count.
			if data[i+20] != 2 {
				t.Errorf("CoupledCount = %d, want 2", data[i+20])
			}
			break
		}
	}

	// Write a packet and close.
	err = w.WritePacket(make([]byte, 200), 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestWriterWithConfig_PreservesMappingFamily(t *testing.T) {
	cases := []struct {
		name          string
		mappingFamily uint8
		channels      uint8
		streams       uint8
		coupled       uint8
		mapping       []byte
		demixing      []byte
		wantDemixing  int
	}{
		{
			name:          "family2-ambisonics",
			mappingFamily: MappingFamilyAmbisonics,
			channels:      6,
			streams:       5,
			coupled:       1,
			mapping:       []byte{2, 3, 4, 5, 0, 1},
			wantDemixing:  0,
		},
		{
			name:          "family3-projection",
			mappingFamily: MappingFamilyProjection,
			channels:      6,
			streams:       3,
			coupled:       3,
			wantDemixing:  expectedDemixingMatrixSize(6, 3, 3),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			_, err := NewWriterWithConfig(&buf, WriterConfig{
				SampleRate:     48000,
				Channels:       tc.channels,
				PreSkip:        DefaultPreSkip,
				OutputGain:     0,
				MappingFamily:  tc.mappingFamily,
				StreamCount:    tc.streams,
				CoupledCount:   tc.coupled,
				ChannelMapping: tc.mapping,
				DemixingMatrix: tc.demixing,
			})
			if err != nil {
				t.Fatalf("NewWriterWithConfig failed: %v", err)
			}

			page, _, err := ParsePage(buf.Bytes())
			if err != nil {
				t.Fatalf("ParsePage failed: %v", err)
			}
			packets := page.Packets()
			if len(packets) == 0 {
				t.Fatal("OpusHead packet missing")
			}
			head, err := ParseOpusHead(packets[0])
			if err != nil {
				t.Fatalf("ParseOpusHead failed: %v", err)
			}

			if head.MappingFamily != tc.mappingFamily {
				t.Fatalf("MappingFamily = %d, want %d", head.MappingFamily, tc.mappingFamily)
			}
			if head.StreamCount != tc.streams {
				t.Fatalf("StreamCount = %d, want %d", head.StreamCount, tc.streams)
			}
			if head.CoupledCount != tc.coupled {
				t.Fatalf("CoupledCount = %d, want %d", head.CoupledCount, tc.coupled)
			}
			if got := len(head.DemixingMatrix); got != tc.wantDemixing {
				t.Fatalf("DemixingMatrix length = %d, want %d", got, tc.wantDemixing)
			}
		})
	}
}

// TestWriterWithConfig_InvalidConfig tests that invalid configs are rejected.
func TestWriterWithConfig_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config WriterConfig
	}{
		{
			name: "zero channels",
			config: WriterConfig{
				SampleRate: 48000,
				Channels:   0,
			},
		},
		{
			name: "family 0 with 3 channels",
			config: WriterConfig{
				SampleRate:    48000,
				Channels:      3,
				MappingFamily: 0,
			},
		},
		{
			name: "family 1 zero streams",
			config: WriterConfig{
				SampleRate:     48000,
				Channels:       6,
				MappingFamily:  1,
				StreamCount:    0,
				CoupledCount:   2,
				ChannelMapping: []byte{0, 1, 2, 3, 4, 5},
			},
		},
		{
			name: "coupled > streams",
			config: WriterConfig{
				SampleRate:     48000,
				Channels:       6,
				MappingFamily:  1,
				StreamCount:    2,
				CoupledCount:   3,
				ChannelMapping: []byte{0, 1, 2, 3, 4, 5},
			},
		},
		{
			name: "mapping length mismatch",
			config: WriterConfig{
				SampleRate:     48000,
				Channels:       6,
				MappingFamily:  1,
				StreamCount:    4,
				CoupledCount:   2,
				ChannelMapping: []byte{0, 1, 2}, // Too short
			},
		},
		{
			name: "invalid mapping value",
			config: WriterConfig{
				SampleRate:     48000,
				Channels:       6,
				MappingFamily:  1,
				StreamCount:    4,
				CoupledCount:   2,
				ChannelMapping: []byte{0, 1, 2, 3, 4, 100}, // 100 > 4+2
			},
		},
		{
			name: "family3 invalid demixing size",
			config: WriterConfig{
				SampleRate:     48000,
				Channels:       6,
				MappingFamily:  MappingFamilyProjection,
				StreamCount:    3,
				CoupledCount:   3,
				DemixingMatrix: []byte{1, 2, 3}, // too short
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			_, err := NewWriterWithConfig(&buf, tc.config)
			if err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

// TestWriterPreSkip tests that PreSkip is properly encoded.
func TestWriterPreSkip(t *testing.T) {
	var buf bytes.Buffer

	config := WriterConfig{
		SampleRate:    48000,
		Channels:      2,
		PreSkip:       500, // Custom pre-skip
		MappingFamily: 0,
	}

	_, err := NewWriterWithConfig(&buf, config)
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	// Find OpusHead and verify pre-skip.
	data := buf.Bytes()
	for i := 0; i < len(data)-12; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			// Pre-skip is at offset 10-11 (little-endian).
			preskip := uint16(data[i+10]) | uint16(data[i+11])<<8
			if preskip != 500 {
				t.Errorf("PreSkip = %d, want 500", preskip)
			}
			break
		}
	}
}

// TestWriterOutputGain tests that OutputGain is properly encoded.
func TestWriterOutputGain(t *testing.T) {
	var buf bytes.Buffer

	config := WriterConfig{
		SampleRate:    48000,
		Channels:      1,
		PreSkip:       312,
		OutputGain:    -512, // -2 dB
		MappingFamily: 0,
	}

	_, err := NewWriterWithConfig(&buf, config)
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	// Find OpusHead and verify output gain.
	data := buf.Bytes()
	for i := 0; i < len(data)-18; i++ {
		if string(data[i:i+8]) == "OpusHead" {
			// Output gain is at offset 16-17 (little-endian, signed).
			gain := int16(uint16(data[i+16]) | uint16(data[i+17])<<8)
			if gain != -512 {
				t.Errorf("OutputGain = %d, want -512", gain)
			}
			break
		}
	}
}

// TestWriterGranulePositionSequence verifies granule positions across pages.
func TestWriterGranulePositionSequence(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write packets with different sample counts.
	sampleCounts := []int{480, 960, 1920, 480, 960}
	for _, samples := range sampleCounts {
		err = w.WritePacket(make([]byte, 50), samples)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	// Parse and verify granule positions.
	data := buf.Bytes()
	offset := 0
	pageNum := 0
	expectedGranule := uint64(0)

	for offset < len(data) {
		page, consumed, err := ParsePage(data[offset:])
		if err != nil {
			break
		}

		if pageNum >= 2 { // Audio pages.
			audioIdx := pageNum - 2
			if audioIdx < len(sampleCounts) {
				expectedGranule += uint64(sampleCounts[audioIdx])
			}
			if page.GranulePos != expectedGranule {
				t.Errorf("page %d granule = %d, want %d", pageNum, page.GranulePos, expectedGranule)
			}
		}

		offset += consumed
		pageNum++
	}
}
