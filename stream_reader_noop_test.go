package gopus

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

type noOpReadPacketSource struct {
	packet []byte
	calls  int
}

func (s *noOpReadPacketSource) ReadPacketInto(dst []byte) (int, uint64, error) {
	s.calls++
	if s.calls > 1 {
		return 0, 0, io.EOF
	}
	return copy(dst, s.packet), 960, nil
}

func TestReaderZeroLengthReadDoesNotAdvanceSourceOrPCM(t *testing.T) {
	packet, err := generateTestPacket(48000, 1, 960)
	if err != nil {
		t.Fatal(err)
	}

	expectedDecoder, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	expectedPCM := make([]float32, 960)
	nSamples, err := expectedDecoder.Decode(packet, expectedPCM)
	if err != nil {
		t.Fatal(err)
	}
	expectedBytes := make([]byte, nSamples*4)
	for i := 0; i < nSamples; i++ {
		binary.LittleEndian.PutUint32(expectedBytes[i*4:], math.Float32bits(expectedPCM[i]))
	}

	source := &noOpReadPacketSource{packet: packet}
	reader, err := NewReader(DefaultDecoderConfig(48000, 1), source, FormatFloat32LE)
	if err != nil {
		t.Fatal(err)
	}

	if n, err := reader.Read(nil); n != 0 || err != nil {
		t.Fatalf("initial Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if source.calls != 0 {
		t.Fatalf("initial Read(nil) advanced source %d times, want 0", source.calls)
	}

	var emptyN int
	var emptyErr error
	if allocs := testing.AllocsPerRun(100, func() {
		emptyN, emptyErr = reader.Read(nil)
	}); allocs != 0 {
		t.Fatalf("buffered Read(nil) allocs = %g, want 0", allocs)
	}
	if emptyN != 0 || emptyErr != nil || source.calls != 0 {
		t.Fatalf("repeated initial Read(nil) = (%d, %v), source calls=%d; want (0, nil), 0", emptyN, emptyErr, source.calls)
	}

	first := make([]byte, 37)
	if n, err := reader.Read(first); n != len(first) || err != nil {
		t.Fatalf("first audio Read = (%d, %v), want (%d, nil)", n, err, len(first))
	}
	if !bytes.Equal(first, expectedBytes[:len(first)]) {
		t.Fatal("first audio Read does not match decoding the source packet")
	}
	if source.calls != 1 {
		t.Fatalf("first audio Read source calls = %d, want 1", source.calls)
	}

	if n, err := reader.Read(nil); n != 0 || err != nil {
		t.Fatalf("buffered Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if source.calls != 1 {
		t.Fatalf("buffered Read(nil) advanced source to %d calls, want 1", source.calls)
	}

	remaining := make([]byte, len(expectedBytes)-len(first))
	if n, err := reader.Read(remaining); n != len(remaining) || err != nil {
		t.Fatalf("remaining audio Read = (%d, %v), want (%d, nil)", n, err, len(remaining))
	}
	if !bytes.Equal(remaining, expectedBytes[len(first):]) {
		t.Fatal("buffered PCM changed across Read(nil)")
	}

	if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("read after packet = (%d, %v), want (0, EOF)", n, err)
	}
	if n, err := reader.Read(nil); n != 0 || err != io.EOF {
		t.Fatalf("Read(nil) after known EOF = (%d, %v), want (0, EOF)", n, err)
	}
	if source.calls != 2 {
		t.Fatalf("reads after EOF advanced source to %d calls, want 2", source.calls)
	}
}
