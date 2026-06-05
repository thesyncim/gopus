package smoke

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/container/red"
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

// TestExternalConsumerRED verifies a downstream module can build and parse RFC
// 2198 RED packets through the public container/red API with no replace beyond
// the module root.
func TestExternalConsumerRED(t *testing.T) {
	const primaryPT = 111
	enc := red.NewEncoder(primaryPT, 960, 1)
	dec := red.NewDecoder(primaryPT)

	// The first frame seeds the history; the second carries it as a redundant
	// block. The Encoder owns the history and output buffer.
	prev := []byte{0x01, 0x02, 0x03}
	_, _ = enc.Encode(prev, 0)
	primary := []byte{0x10, 0x11, 0x12, 0x13}
	payload, n := enc.Encode(primary, 960)
	if n == 0 || len(payload) == 0 {
		t.Fatalf("Encode produced empty RED payload (n=%d)", n)
	}

	gotPrimary, blocks, err := dec.Parse(payload)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary = %x, want %x", gotPrimary, primary)
	}
	if len(blocks) != 1 {
		t.Fatalf("redundant blocks = %d, want 1", len(blocks))
	}
	if !bytes.Equal(blocks[0].Payload, prev) {
		t.Fatalf("redundant payload = %x, want %x", blocks[0].Payload, prev)
	}
}

// TestExternalConsumerPacketLossConcealment verifies a downstream module can run
// packet loss concealment via Decode(nil) after decoding a real packet, using
// only the public top-level API.
func TestExternalConsumerPacketLossConcealment(t *testing.T) {
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
		pcmIn[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}

	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("EncodeFloat32: %v", err)
	}

	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)
	if _, err := dec.Decode(packet, pcmOut); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Conceal one lost frame: nil packet data drives PLC, sized to one frame.
	concealBuf := make([]float32, frameSize*channels)
	n, err := dec.Decode(nil, concealBuf)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("PLC samples = %d, want %d", n, frameSize)
	}
}
