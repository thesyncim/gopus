package smoke

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

func TestExternalConsumerEncodeDecodeAndOgg(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}

	pcmIn := make([]float32, frameSize*channels)
	for i := range pcmIn {
		pcmIn[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / sampleRate))
	}

	packetBuf := make([]byte, 4000)
	nPacket, err := enc.Encode(pcmIn, packetBuf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if nPacket == 0 {
		t.Fatal("Encode returned an empty packet")
	}

	var oggBuf bytes.Buffer
	w, err := ogg.NewWriter(&oggBuf, sampleRate, channels)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WritePacket(packetBuf[:nPacket], frameSize); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := ogg.NewReader(bytes.NewReader(oggBuf.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if got := r.SampleRate(); got != uint32(sampleRate) {
		t.Fatalf("SampleRate = %d, want %d", got, sampleRate)
	}
	if got := r.Channels(); got != uint8(channels) {
		t.Fatalf("Channels = %d, want %d", got, channels)
	}

	packet, granule, err := r.ReadPacket()
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if granule != frameSize {
		t.Fatalf("granule = %d, want %d", granule, frameSize)
	}

	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	nSamples, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if nSamples != frameSize {
		t.Fatalf("decoded samples = %d, want %d", nSamples, frameSize)
	}

	if _, _, err := r.ReadPacket(); err != io.EOF {
		t.Fatalf("second ReadPacket error = %v, want %v", err, io.EOF)
	}
}
