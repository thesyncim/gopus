package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/types"
)

func decodeSilkHeaderBits(t *testing.T, packet []byte) (int, int) {
	t.Helper()

	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parse packet failed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	rd := &rangecoding.Decoder{}
	rd.Init(frames[0])
	vad := rd.DecodeBit(1)
	lbrr := rd.DecodeBit(1)
	return vad, lbrr
}

func decodeHybridStereoHeaderBits(t *testing.T, packet []byte) (int, int, int, int) {
	t.Helper()

	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parse packet failed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(frames))
	}

	rd := &rangecoding.Decoder{}
	rd.Init(frames[0])
	vadMid := rd.DecodeBit(1)
	lbrrMid := rd.DecodeBit(1)
	vadSide := rd.DecodeBit(1)
	lbrrSide := rd.DecodeBit(1)
	return vadMid, lbrrMid, vadSide, lbrrSide
}

func TestSILKHeaderVADFlags(t *testing.T) {
	const (
		frameSize  = 960
		sampleRate = 48000
		freqHz     = 440.0
		amp        = 0.6
	)

	silence := make([]float64, frameSize)
	enc := NewEncoder(sampleRate, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	packet, err := enc.Encode(silence, frameSize)
	if err != nil {
		t.Fatalf("encode silence failed: %v", err)
	}
	vad, lbrr := decodeSilkHeaderBits(t, packet)
	if vad != 0 {
		t.Fatalf("silence VAD flag=%d, want 0", vad)
	}
	if lbrr != 0 {
		t.Fatalf("silence LBRR flag=%d, want 0", lbrr)
	}

	enc = NewEncoder(sampleRate, 1)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	tone := make([]float64, frameSize)
	for i := range tone {
		ti := float64(i) / float64(sampleRate)
		tone[i] = amp * math.Sin(2*math.Pi*freqHz*ti)
	}
	packet, err = enc.Encode(tone, frameSize)
	if err != nil {
		t.Fatalf("encode tone failed: %v", err)
	}
	vad, _ = decodeSilkHeaderBits(t, packet)
	if vad == 0 {
		t.Fatalf("tone VAD flag=%d, want 1", vad)
	}
}

func TestHybridStereoLBRRFlags(t *testing.T) {
	const (
		frameSize  = 960
		sampleRate = 48000
		freqL      = 440.0
		freqR      = 660.0
		amp        = 0.6
	)

	enc := NewEncoder(sampleRate, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetFEC(true)
	enc.SetPacketLoss(10)

	pcm := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[2*i] = amp * math.Sin(2*math.Pi*freqL*ti)
		pcm[2*i+1] = amp * math.Sin(2*math.Pi*freqR*ti)
	}

	packet1, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("encode frame 1 failed: %v", err)
	}
	vadMid1, lbrrMid1, vadSide1, lbrrSide1 := decodeHybridStereoHeaderBits(t, packet1)
	if vadMid1 == 0 || vadSide1 == 0 {
		t.Fatalf("expected VAD flags set on frame 1, got mid=%d side=%d", vadMid1, vadSide1)
	}
	if lbrrMid1 != 0 || lbrrSide1 != 0 {
		t.Fatalf("expected no LBRR on first frame, got mid=%d side=%d", lbrrMid1, lbrrSide1)
	}

	packet2, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("encode frame 2 failed: %v", err)
	}
	vadMid2, lbrrMid2, vadSide2, lbrrSide2 := decodeHybridStereoHeaderBits(t, packet2)
	if vadMid2 == 0 || vadSide2 == 0 {
		t.Fatalf("expected VAD flags set on frame 2, got mid=%d side=%d", vadMid2, vadSide2)
	}
	if lbrrMid2 == 0 || lbrrSide2 == 0 {
		t.Fatalf("expected LBRR flags set on frame 2, got mid=%d side=%d", lbrrMid2, lbrrSide2)
	}
}
