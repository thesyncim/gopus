package ogg

import (
	"bytes"
	"io"
	"testing"
)

func fuzzValidOggStream() []byte {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, 48000, 2)
	if err != nil {
		return nil
	}
	for i := 0; i < 3; i++ {
		packet := make([]byte, 40+i*15)
		packet[0] = 0xF8
		for j := 1; j < len(packet); j++ {
			packet[j] = byte(i + j)
		}
		if err := w.WritePacket(packet, 960); err != nil {
			return nil
		}
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

func FuzzOggReaderNeverPanics(f *testing.F) {
	valid := fuzzValidOggStream()

	seeds := [][]byte{
		{},
		[]byte("OggS"),
		[]byte("not an ogg stream"),
		{0x4f, 0x67, 0x67, 0x53, 0x00, 0x00},
		valid,
	}
	if len(valid) > 8 {
		seeds = append(seeds, valid[:len(valid)/2], valid[:len(valid)-8])
	}

	for _, seed := range seeds {
		f.Add(append([]byte(nil), seed...))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		r, err := NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}

		for i := 0; i < 64; i++ {
			packet, _, err := r.ReadPacket()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			if len(packet) > len(data) {
				t.Fatalf("packet len=%d exceeds input len=%d", len(packet), len(data))
			}
		}
	})
}
