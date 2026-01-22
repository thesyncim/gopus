package ogg

import (
	"bytes"
	"io"
	"testing"
)

// TestNewReader_Valid tests parsing output from Writer.
func TestNewReader_Valid(t *testing.T) {
	// Create a valid Ogg Opus stream using Writer.
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write some packets.
	for i := 0; i < 5; i++ {
		packet := make([]byte, 50+i*10)
		packet[0] = 0xFC
		err = w.WritePacket(packet, 960)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}

	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read it back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Verify header.
	if r.Header == nil {
		t.Fatal("Header is nil")
	}
	if r.Header.Channels != 2 {
		t.Errorf("Channels = %d, want 2", r.Header.Channels)
	}
	if r.Header.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", r.Header.SampleRate)
	}
	if r.Header.PreSkip != DefaultPreSkip {
		t.Errorf("PreSkip = %d, want %d", r.Header.PreSkip, DefaultPreSkip)
	}

	// Verify tags.
	if r.Tags == nil {
		t.Fatal("Tags is nil")
	}
	if r.Tags.Vendor != "gopus" {
		t.Errorf("Vendor = %q, want %q", r.Tags.Vendor, "gopus")
	}
}

// TestNewReader_NotOgg tests that non-Ogg data returns an error.
func TestNewReader_NotOgg(t *testing.T) {
	// Random non-Ogg data.
	data := []byte("This is not an Ogg file at all")
	_, err := NewReader(bytes.NewReader(data))
	if err == nil {
		t.Error("expected error for non-Ogg data")
	}
}

// TestNewReader_BadMagic tests that invalid OpusHead magic returns an error.
func TestNewReader_BadMagic(t *testing.T) {
	// Create a valid Ogg page with invalid OpusHead.
	page := &Page{
		Version:      0,
		HeaderType:   PageFlagBOS,
		GranulePos:   0,
		SerialNumber: 1,
		PageSequence: 0,
		Segments:     []byte{19},
		Payload:      []byte("NotOpusHead12345678"), // Wrong magic
	}
	encoded := page.Encode()

	_, err := NewReader(bytes.NewReader(encoded))
	if err == nil {
		t.Error("expected error for bad OpusHead magic")
	}
}

// TestReadPacket_Single tests reading a single packet.
func TestReadPacket_Single(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write a packet.
	originalPacket := make([]byte, 100)
	originalPacket[0] = 0xFC
	for i := 1; i < len(originalPacket); i++ {
		originalPacket[i] = byte(i)
	}

	err = w.WritePacket(originalPacket, 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read it back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	packet, granule, err := r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}

	if len(packet) != len(originalPacket) {
		t.Errorf("packet len = %d, want %d", len(packet), len(originalPacket))
	}

	for i := range packet {
		if packet[i] != originalPacket[i] {
			t.Errorf("packet[%d] = %d, want %d", i, packet[i], originalPacket[i])
			break
		}
	}

	if granule != 960 {
		t.Errorf("granule = %d, want 960", granule)
	}
}

// TestReadPacket_Multiple tests reading multiple packets.
func TestReadPacket_Multiple(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write 10 packets.
	packetLengths := make([]int, 10)
	for i := 0; i < 10; i++ {
		packet := make([]byte, 50+i*10)
		packet[0] = 0xFC
		packetLengths[i] = len(packet)
		err = w.WritePacket(packet, 960)
		if err != nil {
			t.Fatalf("WritePacket %d failed: %v", i, err)
		}
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read them back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		packet, granule, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d failed: %v", i, err)
		}

		if len(packet) != packetLengths[i] {
			t.Errorf("packet %d len = %d, want %d", i, len(packet), packetLengths[i])
		}

		expectedGranule := uint64((i + 1) * 960)
		if granule != expectedGranule {
			t.Errorf("packet %d granule = %d, want %d", i, granule, expectedGranule)
		}
	}
}

// TestReadPacket_EOF tests that io.EOF is returned after EOS.
func TestReadPacket_EOF(t *testing.T) {
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
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// First packet.
	_, _, err = r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}

	// Second read should return EOF.
	_, _, err = r.ReadPacket()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}

	// Verify EOF flag.
	if !r.EOF() {
		t.Error("EOF() should return true")
	}
}

// TestReader_HeaderFields tests the convenience header accessor methods.
func TestReader_HeaderFields(t *testing.T) {
	var buf bytes.Buffer

	config := WriterConfig{
		SampleRate:    44100,
		Channels:      2,
		PreSkip:       500,
		OutputGain:    -256,
		MappingFamily: 0,
	}

	_, err := NewWriterWithConfig(&buf, config)
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	if r.Channels() != 2 {
		t.Errorf("Channels() = %d, want 2", r.Channels())
	}
	if r.SampleRate() != 44100 {
		t.Errorf("SampleRate() = %d, want 44100", r.SampleRate())
	}
	if r.PreSkip() != 500 {
		t.Errorf("PreSkip() = %d, want 500", r.PreSkip())
	}
}

// TestReader_MultistreamHeader tests parsing family 1 headers.
func TestReader_MultistreamHeader(t *testing.T) {
	var buf bytes.Buffer

	// 5.1 surround config.
	mapping := []byte{0, 4, 1, 2, 3, 5}
	config := WriterConfig{
		SampleRate:     48000,
		Channels:       6,
		PreSkip:        312,
		MappingFamily:  MappingFamilyVorbis,
		StreamCount:    4,
		CoupledCount:   2,
		ChannelMapping: mapping,
	}

	w, err := NewWriterWithConfig(&buf, config)
	if err != nil {
		t.Fatalf("NewWriterWithConfig failed: %v", err)
	}

	// Write a packet.
	err = w.WritePacket(make([]byte, 200), 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read it back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Verify header fields.
	if r.Header.Channels != 6 {
		t.Errorf("Channels = %d, want 6", r.Header.Channels)
	}
	if r.Header.MappingFamily != MappingFamilyVorbis {
		t.Errorf("MappingFamily = %d, want %d", r.Header.MappingFamily, MappingFamilyVorbis)
	}
	if r.Header.StreamCount != 4 {
		t.Errorf("StreamCount = %d, want 4", r.Header.StreamCount)
	}
	if r.Header.CoupledCount != 2 {
		t.Errorf("CoupledCount = %d, want 2", r.Header.CoupledCount)
	}
	if len(r.Header.ChannelMapping) != 6 {
		t.Errorf("ChannelMapping len = %d, want 6", len(r.Header.ChannelMapping))
	}
	for i, m := range mapping {
		if r.Header.ChannelMapping[i] != m {
			t.Errorf("ChannelMapping[%d] = %d, want %d", i, r.Header.ChannelMapping[i], m)
		}
	}
}

// TestReader_LargePacket tests reading packets > 255 bytes.
func TestReader_LargePacket(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write a large packet (600 bytes).
	originalPacket := make([]byte, 600)
	originalPacket[0] = 0xFC
	for i := 1; i < len(originalPacket); i++ {
		originalPacket[i] = byte(i % 256)
	}

	err = w.WritePacket(originalPacket, 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read it back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	packet, _, err := r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket failed: %v", err)
	}

	if len(packet) != len(originalPacket) {
		t.Errorf("packet len = %d, want %d", len(packet), len(originalPacket))
	}

	for i := range packet {
		if packet[i] != originalPacket[i] {
			t.Errorf("packet[%d] = %d, want %d", i, packet[i], originalPacket[i])
			break
		}
	}
}

// TestReader_WriterRoundTrip tests complete write/read cycle.
func TestReader_WriterRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write multiple packets of varying sizes.
	originalPackets := make([][]byte, 20)
	for i := 0; i < 20; i++ {
		packet := make([]byte, 30+i*25)
		packet[0] = 0xFC
		for j := 1; j < len(packet); j++ {
			packet[j] = byte((i + j) % 256)
		}
		originalPackets[i] = packet
		err = w.WritePacket(packet, 960)
		if err != nil {
			t.Fatalf("WritePacket %d failed: %v", i, err)
		}
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Read them back.
	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		packet, _, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d failed: %v", i, err)
		}

		if len(packet) != len(originalPackets[i]) {
			t.Errorf("packet %d len = %d, want %d", i, len(packet), len(originalPackets[i]))
			continue
		}

		for j := range packet {
			if packet[j] != originalPackets[i][j] {
				t.Errorf("packet %d byte %d = %d, want %d", i, j, packet[j], originalPackets[i][j])
				break
			}
		}
	}

	// Should be EOF now.
	_, _, err = r.ReadPacket()
	if err != io.EOF {
		t.Errorf("expected io.EOF after all packets, got %v", err)
	}
}

// TestReader_Serial tests that serial number is captured.
func TestReader_Serial(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	writerSerial := w.Serial()

	err = w.WritePacket(make([]byte, 50), 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	if r.Serial() != writerSerial {
		t.Errorf("Reader serial = 0x%08x, want 0x%08x", r.Serial(), writerSerial)
	}
}

// TestReader_EmptyStream tests reading a stream with no audio packets.
func TestReader_EmptyStream(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Close without writing any packets.
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// Should get EOF immediately.
	_, _, err = r.ReadPacket()
	if err != io.EOF {
		t.Errorf("expected io.EOF for empty stream, got %v", err)
	}
}

// TestReader_GranulePos tests that granule position is tracked correctly.
func TestReader_GranulePos(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	// Write packets with different sample counts.
	samples := []int{480, 960, 1920, 480, 960}
	for _, s := range samples {
		err = w.WritePacket(make([]byte, 50), s)
		if err != nil {
			t.Fatalf("WritePacket failed: %v", err)
		}
	}
	err = w.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	r, err := NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	expectedGranule := uint64(0)
	for i, s := range samples {
		_, granule, err := r.ReadPacket()
		if err != nil {
			t.Fatalf("ReadPacket %d failed: %v", i, err)
		}
		expectedGranule += uint64(s)
		if granule != expectedGranule {
			t.Errorf("packet %d granule = %d, want %d", i, granule, expectedGranule)
		}
	}

	// Final granule position check.
	if r.GranulePos() != expectedGranule {
		t.Errorf("final GranulePos() = %d, want %d", r.GranulePos(), expectedGranule)
	}
}

// TestReader_Truncated tests handling of truncated streams.
func TestReader_Truncated(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 1)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	err = w.WritePacket(make([]byte, 50), 960)
	if err != nil {
		t.Fatalf("WritePacket failed: %v", err)
	}

	// Don't close - just truncate the data.
	data := buf.Bytes()
	truncated := data[:len(data)-10] // Remove last 10 bytes.

	r, err := NewReader(bytes.NewReader(truncated))
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}

	// First packet should work (header pages intact).
	_, _, err = r.ReadPacket()
	// May or may not fail depending on where truncation hits.
	// Just verify it doesn't panic.
	t.Logf("ReadPacket on truncated stream: %v", err)
}
