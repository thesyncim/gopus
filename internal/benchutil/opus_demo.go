package benchutil

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

// OpusDemoPath resolves the pinned libopus reference binary used by parity tooling.
func OpusDemoPath() (string, error) {
	if p, ok := libopustooling.FindOrEnsureOpusDemo(libopustooling.DefaultVersion, libopustooling.DefaultSearchRoots()); ok {
		return p, nil
	}
	return "", fmt.Errorf("opus_demo not found under tmp_check/opus-%s (run: make ensure-libopus)", libopustooling.DefaultVersion)
}

// FrameSizeArg converts a frame size in samples at 48 kHz to the opus_demo CLI value.
func FrameSizeArg(frameSize int) (string, error) {
	switch frameSize {
	case 120:
		return "2.5", nil
	case 240:
		return "5", nil
	case 480:
		return "10", nil
	case 960:
		return "20", nil
	case 1920:
		return "40", nil
	case 2880:
		return "60", nil
	case 3840:
		return "80", nil
	case 4800:
		return "100", nil
	case 5760:
		return "120", nil
	default:
		return "", fmt.Errorf("unsupported frame size %d", frameSize)
	}
}

func WriteRepeatedRawFloat32(path string, samples []float32, repeat int) error {
	if repeat < 1 {
		return fmt.Errorf("repeat must be >= 1")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	if len(samples) == 0 {
		return nil
	}
	chunkSamples := min(len(samples), 1<<15)
	buf := make([]byte, chunkSamples*4)
	for r := 0; r < repeat; r++ {
		for start := 0; start < len(samples); start += chunkSamples {
			end := min(start+chunkSamples, len(samples))
			chunk := buf[:(end-start)*4]
			j := 0
			for _, sample := range samples[start:end] {
				binary.LittleEndian.PutUint32(chunk[j:j+4], math.Float32bits(sample))
				j += 4
			}
			if _, err := w.Write(chunk); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}

// WriteRepeatedOpusDemoBitstream writes an opus_demo .bit stream with zero final ranges.
func WriteRepeatedOpusDemoBitstream(path string, packets [][]byte, repeat int) error {
	if repeat < 1 {
		return fmt.Errorf("repeat must be >= 1")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 1<<20)
	defer w.Flush()

	var header [8]byte
	for r := 0; r < repeat; r++ {
		for _, packet := range packets {
			binary.BigEndian.PutUint32(header[:4], uint32(len(packet)))
			// final range stays zero so opus_demo skips encoder/decoder range checks.
			binary.BigEndian.PutUint32(header[4:], 0)
			if _, err := w.Write(header[:]); err != nil {
				return err
			}
			if _, err := w.Write(packet); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}
