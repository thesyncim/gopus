package testvectors

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// createTestBitstream creates a synthetic .bit format byte slice
// Uses big-endian (network byte order) as per opus_demo format
func createTestBitstream(packets [][]byte, finalRanges []uint32) []byte {
	var data []byte
	for i, pkt := range packets {
		// Write packet length (4 bytes, big-endian)
		lenBytes := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBytes, uint32(len(pkt)))
		data = append(data, lenBytes...)

		// Write final range (4 bytes, big-endian)
		rangeBytes := make([]byte, 4)
		fr := uint32(0)
		if i < len(finalRanges) {
			fr = finalRanges[i]
		}
		binary.BigEndian.PutUint32(rangeBytes, fr)
		data = append(data, rangeBytes...)

		// Write packet data
		data = append(data, pkt...)
	}
	return data
}

func TestParseOpusDemoBitstream_SinglePacket(t *testing.T) {
	// Create a single packet with known data
	packetData := []byte{0xFC, 0x01, 0x02, 0x03} // TOC + some data
	finalRange := uint32(0x12345678)

	data := createTestBitstream([][]byte{packetData}, []uint32{finalRange})

	packets, err := ParseOpusDemoBitstream(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	if len(packets[0].Data) != len(packetData) {
		t.Errorf("packet data length: expected %d, got %d", len(packetData), len(packets[0].Data))
	}

	for i, b := range packets[0].Data {
		if b != packetData[i] {
			t.Errorf("packet data[%d]: expected 0x%02X, got 0x%02X", i, packetData[i], b)
		}
	}

	if packets[0].FinalRange != finalRange {
		t.Errorf("final range: expected 0x%08X, got 0x%08X", finalRange, packets[0].FinalRange)
	}
}

func TestParseOpusDemoBitstream_MultiplePackets(t *testing.T) {
	packets := [][]byte{
		{0xFC, 0x01},        // 2 bytes
		{0xFC, 0x02, 0x03},  // 3 bytes
		{0xFC},              // 1 byte (minimal)
		{0xFC, 0x04, 0x05, 0x06, 0x07}, // 5 bytes
	}
	ranges := []uint32{0x11111111, 0x22222222, 0x33333333, 0x44444444}

	data := createTestBitstream(packets, ranges)

	result, err := ParseOpusDemoBitstream(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != len(packets) {
		t.Fatalf("expected %d packets, got %d", len(packets), len(result))
	}

	for i := range result {
		if len(result[i].Data) != len(packets[i]) {
			t.Errorf("packet %d: expected len %d, got %d", i, len(packets[i]), len(result[i].Data))
		}
		if result[i].FinalRange != ranges[i] {
			t.Errorf("packet %d: expected range 0x%08X, got 0x%08X", i, ranges[i], result[i].FinalRange)
		}
	}
}

func TestParseOpusDemoBitstream_LargePacket(t *testing.T) {
	// Test with packet larger than 255 bytes
	packetData := make([]byte, 500)
	packetData[0] = 0xFC // TOC byte
	for i := 1; i < len(packetData); i++ {
		packetData[i] = byte(i % 256)
	}
	finalRange := uint32(0xDEADBEEF)

	data := createTestBitstream([][]byte{packetData}, []uint32{finalRange})

	packets, err := ParseOpusDemoBitstream(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	if len(packets[0].Data) != 500 {
		t.Errorf("expected packet length 500, got %d", len(packets[0].Data))
	}

	// Verify data integrity
	for i, b := range packets[0].Data {
		expected := packetData[i]
		if b != expected {
			t.Errorf("data mismatch at index %d: expected 0x%02X, got 0x%02X", i, expected, b)
			break
		}
	}
}

func TestParseOpusDemoBitstream_EmptyFile(t *testing.T) {
	packets, err := ParseOpusDemoBitstream([]byte{})
	if err != nil {
		t.Fatalf("unexpected error for empty data: %v", err)
	}
	if packets != nil && len(packets) != 0 {
		t.Errorf("expected nil or empty slice for empty data, got %d packets", len(packets))
	}
}

func TestParseOpusDemoBitstream_TruncatedHeader(t *testing.T) {
	// Only 4 bytes (missing finalRange)
	data := []byte{0x04, 0x00, 0x00, 0x00}

	_, err := ParseOpusDemoBitstream(data)
	if err == nil {
		t.Fatal("expected error for truncated header, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestParseOpusDemoBitstream_TruncatedPacketData(t *testing.T) {
	// Header says 100 bytes, but only provide 10
	data := make([]byte, 8+10)
	binary.BigEndian.PutUint32(data[0:], 100) // packet length = 100
	binary.BigEndian.PutUint32(data[4:], 0)   // final range

	_, err := ParseOpusDemoBitstream(data)
	if err == nil {
		t.Fatal("expected error for truncated packet data, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestParseOpusDemoBitstream_ZeroLengthPacket(t *testing.T) {
	// Packet with 0 bytes of data (valid edge case)
	data := createTestBitstream([][]byte{{}}, []uint32{0x12345678})

	packets, err := ParseOpusDemoBitstream(data)
	if err != nil {
		t.Fatalf("unexpected error for zero-length packet: %v", err)
	}

	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	if len(packets[0].Data) != 0 {
		t.Errorf("expected empty packet data, got %d bytes", len(packets[0].Data))
	}
}

func TestReadBitstreamFile(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.bit")

	packetData := []byte{0xFC, 0x01, 0x02, 0x03}
	finalRange := uint32(0xCAFEBABE)
	data := createTestBitstream([][]byte{packetData}, []uint32{finalRange})

	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	packets, err := ReadBitstreamFile(tmpFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(packets))
	}

	if packets[0].FinalRange != finalRange {
		t.Errorf("final range mismatch: expected 0x%08X, got 0x%08X", finalRange, packets[0].FinalRange)
	}
}

func TestReadBitstreamFile_NotExists(t *testing.T) {
	_, err := ReadBitstreamFile("/nonexistent/file.bit")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

func TestGetBitstreamInfo(t *testing.T) {
	// Create packets with CELT FB 20ms TOC (config 31)
	// TOC = (31 << 3) | 0x00 = 0xF8
	packets := []Packet{
		{Data: []byte{0xF8, 0x01, 0x02}, FinalRange: 0x11111111},
		{Data: []byte{0xF8, 0x03, 0x04, 0x05}, FinalRange: 0x22222222},
		{Data: []byte{0xF8, 0x06}, FinalRange: 0x33333333},
	}

	info := GetBitstreamInfo(packets)

	if info.PacketCount != 3 {
		t.Errorf("expected PacketCount 3, got %d", info.PacketCount)
	}

	expectedBytes := 3 + 4 + 2
	if info.TotalBytes != expectedBytes {
		t.Errorf("expected TotalBytes %d, got %d", expectedBytes, info.TotalBytes)
	}

	if info.FirstTOC != 0xF8 {
		t.Errorf("expected FirstTOC 0xF8, got 0x%02X", info.FirstTOC)
	}

	// Config 31 = CELT FB 20ms = 960 samples
	expectedDuration := 3 * 960
	if info.Duration != expectedDuration {
		t.Errorf("expected Duration %d, got %d", expectedDuration, info.Duration)
	}
}

func TestGetBitstreamInfo_Empty(t *testing.T) {
	info := GetBitstreamInfo(nil)
	if info.PacketCount != 0 {
		t.Errorf("expected PacketCount 0 for nil, got %d", info.PacketCount)
	}

	info = GetBitstreamInfo([]Packet{})
	if info.PacketCount != 0 {
		t.Errorf("expected PacketCount 0 for empty slice, got %d", info.PacketCount)
	}
}

func TestGetFrameSizeFromConfig(t *testing.T) {
	testCases := []struct {
		config       byte
		expectedSize int
		name         string
	}{
		{0, 480, "SILK NB 10ms"},
		{1, 960, "SILK NB 20ms"},
		{2, 1920, "SILK NB 40ms"},
		{3, 2880, "SILK NB 60ms"},
		{8, 480, "SILK WB 10ms"},
		{9, 960, "SILK WB 20ms"},
		{12, 480, "Hybrid SWB 10ms"},
		{13, 960, "Hybrid SWB 20ms"},
		{14, 480, "Hybrid FB 10ms"},
		{15, 960, "Hybrid FB 20ms"},
		{16, 120, "CELT NB 2.5ms"},
		{17, 240, "CELT NB 5ms"},
		{18, 480, "CELT NB 10ms"},
		{19, 960, "CELT NB 20ms"},
		{28, 120, "CELT FB 2.5ms"},
		{31, 960, "CELT FB 20ms"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			size := getFrameSizeFromConfig(tc.config)
			if size != tc.expectedSize {
				t.Errorf("config %d: expected %d samples, got %d", tc.config, tc.expectedSize, size)
			}
		})
	}
}
