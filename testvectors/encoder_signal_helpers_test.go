package testvectors

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/testsignal"
)

const defaultEncoderSignalVariant = testsignal.EncoderVariantAMMultisineV1

func generateEncoderTestSignal(samples int, channels int) []float32 {
	signal, err := testsignal.GenerateEncoderSignalVariant(defaultEncoderSignalVariant, 48000, samples, channels)
	if err != nil {
		panic(fmt.Sprintf("generate encoder signal: %v", err))
	}
	return signal
}

func writeFloat32LEFile(path string, samples []float32) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf [float32LEWriteBufferSize]byte
	for len(samples) > 0 {
		count := min(len(samples), len(buf)/4)
		chunk := buf[:count*4]
		for i, s := range samples[:count] {
			binary.LittleEndian.PutUint32(chunk[i*4:], math.Float32bits(s))
		}
		if n, err := f.Write(chunk); err != nil {
			return err
		} else if n != len(chunk) {
			return fmt.Errorf("write float32 PCM: %w", io.ErrShortWrite)
		}
		samples = samples[count:]
	}
	return nil
}

const float32LEWriteBufferSize = 64 << 10

func TestWriteFloat32LEFilePreservesSamplesAcrossChunks(t *testing.T) {
	t.Parallel()

	bits := []uint32{0x00000000, 0x80000000, 0x7f800000, 0xff800000, 0x7fc12345, 0x3f800000}
	samples := make([]float32, float32LEWriteBufferSize/4+3)
	want := make([]byte, len(samples)*4)
	for i := range samples {
		bitsValue := bits[i%len(bits)] ^ uint32(i&0x3f)
		samples[i] = math.Float32frombits(bitsValue)
		binary.LittleEndian.PutUint32(want[i*4:], bitsValue)
	}

	path := filepath.Join(t.TempDir(), "input.f32")
	if err := writeFloat32LEFile(path, samples); err != nil {
		t.Fatalf("write PCM: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read PCM: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PCM bytes differ: got %d bytes, want %d", len(got), len(want))
	}
}

func parseOpusDemoEncodeBitstream(path string) ([][]byte, []uint32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	packets, err := ParseOpusDemoBitstream(raw)
	if err != nil {
		return nil, nil, err
	}
	outPackets := make([][]byte, len(packets))
	outRanges := make([]uint32, len(packets))
	for i := range packets {
		outPackets[i] = packets[i].Data
		outRanges[i] = packets[i].FinalRange
	}
	return outPackets, outRanges, nil
}
