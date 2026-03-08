package benchutil

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRepeatedRawFloat32(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "samples.f32")
	samples := []float32{0.25, -0.5, 1.0}
	if err := WriteRepeatedRawFloat32(path, samples, 2); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32 failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) != len(samples)*2*4 {
		t.Fatalf("unexpected byte length: got=%d want=%d", len(data), len(samples)*2*4)
	}

	got := make([]float32, 0, len(samples)*2)
	for i := 0; i < len(data); i += 4 {
		got = append(got, math.Float32frombits(binary.LittleEndian.Uint32(data[i:i+4])))
	}
	want := []float32{0.25, -0.5, 1.0, 0.25, -0.5, 1.0}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample %d mismatch: got=%08x want=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestWriteRepeatedOpusDemoBitstream(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "packets.bit")
	packets := [][]byte{{0x01, 0x02}, {0xAA}}
	if err := WriteRepeatedOpusDemoBitstream(path, packets, 2); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	offset := 0
	for repeat := 0; repeat < 2; repeat++ {
		for packetIdx, packet := range packets {
			if offset+8+len(packet) > len(data) {
				t.Fatalf("truncated stream at repeat=%d packet=%d", repeat, packetIdx)
			}
			if got := binary.BigEndian.Uint32(data[offset : offset+4]); got != uint32(len(packet)) {
				t.Fatalf("repeat=%d packet=%d len mismatch: got=%d want=%d", repeat, packetIdx, got, len(packet))
			}
			if got := binary.BigEndian.Uint32(data[offset+4 : offset+8]); got != 0 {
				t.Fatalf("repeat=%d packet=%d final range mismatch: got=%d want=0", repeat, packetIdx, got)
			}
			offset += 8
			for i, b := range packet {
				if data[offset+i] != b {
					t.Fatalf("repeat=%d packet=%d byte=%d mismatch: got=%02x want=%02x", repeat, packetIdx, i, data[offset+i], b)
				}
			}
			offset += len(packet)
		}
	}
	if offset != len(data) {
		t.Fatalf("unexpected trailing bytes: got=%d total=%d", offset, len(data))
	}
}
