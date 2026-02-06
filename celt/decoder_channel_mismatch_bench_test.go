package celt

import (
	"math"
	"testing"
)

func benchmarkEncodedPacket(b *testing.B, channels, frameSize int) []byte {
	b.Helper()

	enc := NewEncoder(channels)
	pcm := make([]float64, frameSize*channels)

	const sampleRate = 48000.0
	const amp = 0.45
	for i := 0; i < frameSize; i++ {
		l := amp * math.Sin(2*math.Pi*440*float64(i)/sampleRate)
		if channels == 1 {
			pcm[i] = l
			continue
		}
		r := amp * math.Sin(2*math.Pi*554*float64(i)/sampleRate)
		pcm[2*i] = l
		pcm[2*i+1] = r
	}

	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		b.Fatalf("EncodeFrame failed: %v", err)
	}

	// Copy to decouple from encoder packet scratch storage.
	packetCopy := make([]byte, len(packet))
	copy(packetCopy, packet)
	return packetCopy
}

func BenchmarkDecodeFrameWithPacketStereo_MonoToStereo(b *testing.B) {
	const frameSize = 960
	decoder := NewDecoder(2)
	packet := benchmarkEncodedPacket(b, 1, frameSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := decoder.DecodeFrameWithPacketStereo(packet, frameSize, false)
		if err != nil {
			b.Fatalf("DecodeFrameWithPacketStereo failed: %v", err)
		}
		if len(out) != frameSize*2 {
			b.Fatalf("unexpected output length: got=%d want=%d", len(out), frameSize*2)
		}
	}
}

func BenchmarkDecodeFramePacket_Mono(b *testing.B) {
	const frameSize = 960
	decoder := NewDecoder(1)
	packet := benchmarkEncodedPacket(b, 1, frameSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := decoder.DecodeFrame(packet, frameSize)
		if err != nil {
			b.Fatalf("DecodeFrame failed: %v", err)
		}
		if len(out) != frameSize {
			b.Fatalf("unexpected output length: got=%d want=%d", len(out), frameSize)
		}
	}
}

func BenchmarkDecodeFramePacket_Stereo(b *testing.B) {
	const frameSize = 960
	decoder := NewDecoder(2)
	packet := benchmarkEncodedPacket(b, 2, frameSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := decoder.DecodeFrame(packet, frameSize)
		if err != nil {
			b.Fatalf("DecodeFrame failed: %v", err)
		}
		if len(out) != frameSize*2 {
			b.Fatalf("unexpected output length: got=%d want=%d", len(out), frameSize*2)
		}
	}
}

func BenchmarkDecodeFrameWithPacketStereo_StereoToMono(b *testing.B) {
	const frameSize = 960
	decoder := NewDecoder(1)
	packet := benchmarkEncodedPacket(b, 2, frameSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := decoder.DecodeFrameWithPacketStereo(packet, frameSize, true)
		if err != nil {
			b.Fatalf("DecodeFrameWithPacketStereo failed: %v", err)
		}
		if len(out) != frameSize {
			b.Fatalf("unexpected output length: got=%d want=%d", len(out), frameSize)
		}
	}
}
