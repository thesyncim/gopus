// benchmark_alloc_test.go benchmarks the public encode/decode paths with
// allocation reporting enabled.
//
// The caller-buffer encode/decode APIs are expected to stay at 0 allocs/op in
// hot paths. Convenience helpers such as EncodeFloat32 allocate by design and
// are benchmarked separately so the two paths do not get conflated.

package gopus

import (
	"math"
	"runtime"
	"testing"
)

// Test packet data for benchmarks
var (
	// CELT fullband 20ms mono packet (config 31)
	benchCELTPacket = func() []byte {
		toc := byte(0xF8) // config=31 (CELT FB 20ms), mono, code 0
		data := make([]byte, 50)
		data[0] = toc
		for i := 1; i < len(data); i++ {
			data[i] = byte(i * 7)
		}
		return data
	}()

	// Hybrid fullband 20ms mono packet (config 15)
	benchHybridPacket = func() []byte {
		toc := byte(0x78) // config=15 (Hybrid FB 20ms), mono, code 0
		data := make([]byte, 50)
		data[0] = toc
		for i := 1; i < len(data); i++ {
			data[i] = 0xFF
		}
		return data
	}()

	// SILK wideband 20ms mono packet (config 9)
	benchSILKPacket = func() []byte {
		toc := byte(0x48) // config=9 (SILK WB 20ms), mono, code 0
		data := make([]byte, 50)
		data[0] = toc
		for i := 1; i < len(data); i++ {
			data[i] = byte(i)
		}
		return data
	}()
)

// generateBenchSineWave generates a sine wave for encoder benchmarks.
func generateBenchSineWave(samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return pcm
}

func warmDecoderFloat32Benchmark(b *testing.B, dec *Decoder, packet []byte, pcm []float32, label string) {
	b.Helper()
	if _, err := dec.Decode(packet, pcm); err != nil {
		b.Fatalf("%s warmup: %v", label, err)
	}
	runtime.GC()
	runtime.Gosched()
}

func warmDecoderInt16Benchmark(b *testing.B, dec *Decoder, packet []byte, pcm []int16, label string) {
	b.Helper()
	if _, err := dec.DecodeInt16(packet, pcm); err != nil {
		b.Fatalf("%s warmup: %v", label, err)
	}
	runtime.GC()
	runtime.Gosched()
}

// BenchmarkDecoderDecode_CELT benchmarks CELT-only decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_CELT(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]float32, 960) // 20ms at 48kHz mono
	packet := benchCELTPacket

	warmDecoderFloat32Benchmark(b, dec, packet, pcm, "Decode")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkDecoderDecode_Hybrid benchmarks Hybrid mode decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_Hybrid(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]float32, 960) // 20ms at 48kHz mono
	packet := benchHybridPacket

	warmDecoderFloat32Benchmark(b, dec, packet, pcm, "Decode")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkDecoderDecode_SILK benchmarks SILK-only decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_SILK(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]float32, 960) // 20ms at 48kHz mono
	packet := benchSILKPacket

	warmDecoderFloat32Benchmark(b, dec, packet, pcm, "Decode")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkDecoderDecodeInt16 benchmarks int16 decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecodeInt16(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]int16, 960) // 20ms at 48kHz mono
	packet := benchCELTPacket

	warmDecoderInt16Benchmark(b, dec, packet, pcm, "DecodeInt16")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.DecodeInt16(packet, pcm)
		if err != nil {
			b.Fatalf("DecodeInt16: %v", err)
		}
	}
}

// BenchmarkDecoderDecode_PLC benchmarks Packet Loss Concealment.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_PLC(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]float32, 960)
	// Decode one frame first to set up state
	_, _ = dec.Decode(benchCELTPacket, pcm)
	runtime.GC()
	runtime.Gosched()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(nil, pcm)
		if err != nil {
			b.Fatalf("Decode PLC: %v", err)
		}
	}
}

// BenchmarkDecoderDecode_Stereo benchmarks stereo decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_Stereo(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	// Stereo CELT packet (config 31, stereo flag set)
	packet := make([]byte, 50)
	packet[0] = 0xFC // config=31, stereo=1, code=0
	for i := 1; i < len(packet); i++ {
		packet[i] = byte(i * 7)
	}

	pcm := make([]float32, 960*2) // 20ms at 48kHz stereo

	warmDecoderFloat32Benchmark(b, dec, packet, pcm, "Decode")
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_CallerBuffer benchmarks float32 encoding with caller-owned output.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_CallerBuffer(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationAudio))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960) // 20ms at 48kHz mono
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for i := 0; i < 5; i++ {
		enc.Encode(pcm, packet)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncodeFloat32_Allocating benchmarks the allocating convenience path.
func BenchmarkEncoderEncodeFloat32_Allocating(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationAudio))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)

	for i := 0; i < 5; i++ {
		if _, err := enc.EncodeFloat32(pcm); err != nil {
			b.Fatalf("EncodeFloat32 warmup: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := enc.EncodeFloat32(pcm); err != nil {
			b.Fatalf("EncodeFloat32: %v", err)
		}
	}
}

// BenchmarkEncoderEncodeInt16 benchmarks int16 encoding.
// Target: 0 allocs/op
func BenchmarkEncoderEncodeInt16(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationAudio))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	// Generate int16 samples
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for i := 0; i < 5; i++ {
		enc.EncodeInt16(pcm, packet)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.EncodeInt16(pcm, packet)
		if err != nil {
			b.Fatalf("EncodeInt16: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_Stereo benchmarks stereo encoding.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_Stereo(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 2, ApplicationAudio))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	// Generate stereo samples (interleaved)
	pcm := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for i := 0; i < 5; i++ {
		enc.Encode(pcm, packet)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_VoIP benchmarks VoIP mode encoding (SILK).
// Target: 0 allocs/op
func BenchmarkEncoderEncode_VoIP(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationVoIP))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for i := 0; i < 5; i++ {
		enc.Encode(pcm, packet)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_LowDelay benchmarks low-delay mode encoding (CELT).
// Target: 0 allocs/op
func BenchmarkEncoderEncode_LowDelay(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationLowDelay))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for i := 0; i < 5; i++ {
		enc.Encode(pcm, packet)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkRoundTrip benchmarks encode + decode round trip.
// Target: 0 allocs/op
func BenchmarkRoundTrip(b *testing.B) {
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationAudio))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcmIn := generateBenchSineWave(960)
	packet := make([]byte, 4000)
	pcmOut := make([]float32, 960)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		n, err := enc.Encode(pcmIn, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}

		_, err = dec.Decode(packet[:n], pcmOut)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkDecoderDecode_MultiFrame benchmarks multi-frame packet decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_MultiFrame(b *testing.B) {
	// First encode two frames, then decode the multi-frame packet
	enc, err := NewEncoder(DefaultEncoderConfig(48000, 1, ApplicationLowDelay))
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	// Generate and encode a packet
	pcmIn := generateBenchSineWave(960)
	packet := make([]byte, 4000)
	n, err := enc.Encode(pcmIn, packet)
	if err != nil {
		b.Fatalf("Encode: %v", err)
	}
	packet = packet[:n]

	pcm := make([]float32, 960) // Single frame

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkStreamReader benchmarks streaming decode via io.Reader.
// Note: Some allocations expected from io.Reader interface overhead.
func BenchmarkStreamReader(b *testing.B) {
	// Create a mock packet reader
	packets := make([][]byte, 100)
	for i := range packets {
		p := make([]byte, 50)
		p[0] = 0xF8 // CELT FB 20ms mono
		for j := 1; j < len(p); j++ {
			p[j] = byte(j * i)
		}
		packets[i] = p
	}

	source := &mockPacketReader{packets: packets}
	reader, err := NewReader(DefaultDecoderConfig(48000, 1), source, FormatFloat32LE)
	if err != nil {
		b.Fatalf("NewReader: %v", err)
	}

	buf := make([]byte, 960*4) // 20ms float32

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		source.index = 0
		reader.Reset()

		_, err := reader.Read(buf)
		if err != nil {
			b.Fatalf("Read: %v", err)
		}
	}
}

// mockPacketReader is a test packet source.
type mockPacketReader struct {
	packets [][]byte
	index   int
}

func (m *mockPacketReader) ReadPacketInto(dst []byte) (int, uint64, error) {
	if m.index >= len(m.packets) {
		return 0, 0, nil
	}
	n := copy(dst, m.packets[m.index])
	m.index++
	return n, uint64(m.index), nil
}
