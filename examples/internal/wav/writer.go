// Package wav writes interleaved float32 PCM to classic RIFF/WAVE files.
package wav

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	headerSize     = 44
	bytesPerSample = 2
	riffOverhead   = uint64(36)
)

var (
	// ErrRIFFSizeLimit indicates that the requested samples exceed the size
	// representable by a classic RIFF/WAVE header.
	ErrRIFFSizeLimit = errors.New("WAV data exceeds the classic RIFF size limit")
	// ErrPCMFrameAlignment indicates that the interleaved samples do not contain
	// complete channel frames.
	ErrPCMFrameAlignment = errors.New("WAV samples do not contain complete channel frames")
)

// Writer writes signed 16-bit PCM samples to a classic RIFF/WAVE file.
type Writer struct {
	file         *os.File
	dataBytes    uint64
	maxDataBytes uint64
	sampleRate   uint32
	channels     uint16
	blockAlign   uint16
	byteRate     uint32
}

// NewWriter creates a classic RIFF/WAVE writer. It rejects format fields that
// do not fit the WAV header before creating or truncating path.
func NewWriter(path string, sampleRate, channels int) (*Writer, error) {
	if sampleRate <= 0 || uint64(sampleRate) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("invalid WAV sample rate %d", sampleRate)
	}
	if channels <= 0 || channels > int(^uint16(0))/bytesPerSample {
		return nil, fmt.Errorf("invalid WAV channel count %d", channels)
	}

	blockAlign := uint64(channels) * bytesPerSample
	byteRate := uint64(sampleRate) * blockAlign
	if byteRate > uint64(^uint32(0)) {
		return nil, fmt.Errorf("WAV byte rate %d does not fit the header", byteRate)
	}
	maxDataBytes := uint64(^uint32(0)) - riffOverhead
	maxDataBytes -= maxDataBytes % blockAlign

	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{
		file:         f,
		maxDataBytes: maxDataBytes,
		sampleRate:   uint32(sampleRate),
		channels:     uint16(channels),
		blockAlign:   uint16(blockAlign),
		byteRate:     uint32(byteRate),
	}
	var header [headerSize]byte
	w.writeHeader(header[:])
	if n, err := f.Write(header[:]); err != nil || n != len(header) {
		if err == nil {
			err = io.ErrShortWrite
		}
		return nil, errors.Join(fmt.Errorf("write WAV header: %w", err), f.Close())
	}
	return w, nil
}

// WriteSamples writes complete interleaved channel frames. The sample
// conversion clamps to signed 16-bit range and rounds ties to even.
func (w *Writer) WriteSamples(samples []float32) error {
	if w == nil || w.file == nil {
		return os.ErrClosed
	}
	if len(samples) == 0 {
		return nil
	}
	if len(samples)%int(w.channels) != 0 {
		return ErrPCMFrameAlignment
	}
	if w.dataBytes > w.maxDataBytes || uint64(len(samples)) > (w.maxDataBytes-w.dataBytes)/bytesPerSample {
		return ErrRIFFSizeLimit
	}

	buf := make([]byte, len(samples)*bytesPerSample)
	for i, sample := range samples {
		scaled := float64(sample) * 32768.0
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		value := int16(math.RoundToEven(scaled))
		binary.LittleEndian.PutUint16(buf[i*bytesPerSample:], uint16(value))
	}

	written, err := w.file.Write(buf)
	if written > 0 {
		w.dataBytes += uint64(written)
	}
	if err != nil {
		return err
	}
	if written != len(buf) {
		return io.ErrShortWrite
	}
	return nil
}

// Close patches the RIFF and data sizes from the bytes written and closes the
// file. Repeated calls return nil.
func (w *Writer) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	f := w.file
	w.file = nil

	var finalizeErr error
	if w.dataBytes > w.maxDataBytes {
		finalizeErr = ErrRIFFSizeLimit
	} else {
		var header [headerSize]byte
		w.writeHeader(header[:])
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			finalizeErr = err
		} else if n, err := f.Write(header[:]); err != nil {
			finalizeErr = err
		} else if n != len(header) {
			finalizeErr = io.ErrShortWrite
		}
	}
	return errors.Join(finalizeErr, f.Close())
}

func (w *Writer) writeHeader(dst []byte) {
	dataBytes := uint32(w.dataBytes)
	copy(dst[0:4], "RIFF")
	binary.LittleEndian.PutUint32(dst[4:8], uint32(riffOverhead)+dataBytes)
	copy(dst[8:12], "WAVE")
	copy(dst[12:16], "fmt ")
	binary.LittleEndian.PutUint32(dst[16:20], 16)
	binary.LittleEndian.PutUint16(dst[20:22], 1)
	binary.LittleEndian.PutUint16(dst[22:24], w.channels)
	binary.LittleEndian.PutUint32(dst[24:28], w.sampleRate)
	binary.LittleEndian.PutUint32(dst[28:32], w.byteRate)
	binary.LittleEndian.PutUint16(dst[32:34], w.blockAlign)
	binary.LittleEndian.PutUint16(dst[34:36], 16)
	copy(dst[36:40], "data")
	binary.LittleEndian.PutUint32(dst[40:44], dataBytes)
}
