package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
)

type wavRecorder struct {
	file       *os.File
	path       string
	channels   int
	sampleRate int
	dataBytes  uint32
}

func newWAVRecorder(dir string, channels, sampleRate int) (*wavRecorder, error) {
	if dir == "" {
		dir = "recordings"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "loopback-"+time.Now().Format("20060102-150405")+".wav")
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &wavRecorder{file: f, path: path, channels: channels, sampleRate: sampleRate}
	if err := w.writeHeader(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return w, nil
}

func (w *wavRecorder) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

func (w *wavRecorder) WriteFloat32(pcm []float32) error {
	if w == nil || w.file == nil || len(pcm) == 0 {
		return nil
	}
	var scratch [4096]byte
	for len(pcm) > 0 {
		n := len(scratch) / 2
		if n > len(pcm) {
			n = len(pcm)
		}
		for i := 0; i < n; i++ {
			s := float64(pcm[i])
			if s > 1 {
				s = 1
			} else if s < -1 {
				s = -1
			}
			v := int16(math.Round(s * 32767))
			binary.LittleEndian.PutUint16(scratch[i*2:], uint16(v))
		}
		if _, err := w.file.Write(scratch[:n*2]); err != nil {
			return err
		}
		w.dataBytes += uint32(n * 2)
		pcm = pcm[n:]
	}
	return nil
}

func (w *wavRecorder) Close() error {
	if w == nil || w.file == nil {
		return nil
	}
	err := w.patchHeader()
	closeErr := w.file.Close()
	w.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func (w *wavRecorder) writeHeader() error {
	header := make([]byte, 44)
	copy(header[0:], "RIFF")
	copy(header[8:], "WAVE")
	copy(header[12:], "fmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 1)
	binary.LittleEndian.PutUint16(header[22:], uint16(w.channels))
	binary.LittleEndian.PutUint32(header[24:], uint32(w.sampleRate))
	byteRate := uint32(w.sampleRate * w.channels * 2)
	binary.LittleEndian.PutUint32(header[28:], byteRate)
	binary.LittleEndian.PutUint16(header[32:], uint16(w.channels*2))
	binary.LittleEndian.PutUint16(header[34:], 16)
	copy(header[36:], "data")
	_, err := w.file.Write(header)
	return err
}

func (w *wavRecorder) patchHeader() error {
	if _, err := w.file.Seek(4, 0); err != nil {
		return err
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], 36+w.dataBytes)
	if _, err := w.file.Write(b[:]); err != nil {
		return err
	}
	if _, err := w.file.Seek(40, 0); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(b[:], w.dataBytes)
	_, err := w.file.Write(b[:])
	return err
}

func readWAVFloat32(path string) ([]float32, int, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, 0, fmt.Errorf("not a RIFF/WAVE file")
	}

	var channels, sampleRate, bitsPerSample int
	var pcmData []byte
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4:]))
		body := off + 8
		if size < 0 || body+size > len(data) {
			return nil, 0, 0, fmt.Errorf("invalid WAV chunk size")
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, 0, fmt.Errorf("short fmt chunk")
			}
			if binary.LittleEndian.Uint16(data[body:]) != 1 {
				return nil, 0, 0, fmt.Errorf("unsupported WAV encoding")
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2:]))
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4:]))
			bitsPerSample = int(binary.LittleEndian.Uint16(data[body+14:]))
		case "data":
			pcmData = data[body : body+size]
		}
		off = body + size
		if off%2 != 0 {
			off++
		}
	}
	if channels <= 0 || sampleRate <= 0 || bitsPerSample != 16 || len(pcmData) == 0 {
		return nil, 0, 0, fmt.Errorf("unsupported WAV format")
	}

	samples := make([]float32, len(pcmData)/2)
	for i := range samples {
		v := int16(binary.LittleEndian.Uint16(pcmData[i*2:]))
		samples[i] = float32(v) / 32768
	}
	return samples, channels, sampleRate, nil
}

func playWAVFile(path string) error {
	samples, channels, rate, err := readWAVFloat32(path)
	if err != nil {
		return err
	}
	return playPCM(samples, channels, rate)
}
