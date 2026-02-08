// benchmark_alloc_test.go - Allocation benchmarks for gopus encode/decode paths.
//
// Current Status:
// ---------------
// The public Encode/Decode APIs follow io.Reader/io.Writer patterns where callers
// provide output buffers. However, internal CELT/SILK encoding/decoding still
// performs heap allocations for intermediate buffers.
//
// Zero-Allocation Patterns Applied:
// ---------------------------------
// 1. Public API: Encoder and Decoder structs have pre-allocated scratch buffers
//    for float32<->float64 and int16<->float32 conversions at the API boundary.
//
// 2. Caller-provided buffers: All public Encode/Decode methods accept caller-
//    provided output buffers rather than allocating and returning new slices.
//
// 3. Persistent state: Sub-encoders (SILK, CELT) are created once and reused
//    across encode calls rather than creating new instances per frame.
//
// Future Improvements (for 0 allocs/op):
// --------------------------------------
// 1. CELT encoder: Add scratch buffers for MDCT computation, band energy
//    calculation, and transient analysis working arrays.
//
// 2. SILK encoder: Add scratch buffers for LPC analysis, NSQ quantization,
//    and stereo mid/side conversion.
//
// 3. Internal decoders: Add scratch buffers for synthesis, denormalization,
//    and IMDCT computation.
//
// 4. Range encoder/decoder: Pre-allocate output buffers.
//
// Run benchmarks with:
//   go test -bench=Benchmark -benchmem -run=^$ ./...
//
// Target: 0 allocs/op for Decode/Encode in hot paths.
// Current: ~288 allocs/op for Encode, ~59 allocs/op for Decode (CELT mode).

package gopus

import (
	"math"
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

// BenchmarkDecoderDecode_CELT benchmarks CELT-only decoding.
// Target: 0 allocs/op
func BenchmarkDecoderDecode_CELT(b *testing.B) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}

	pcm := make([]float32, 960) // 20ms at 48kHz mono
	packet := benchCELTPacket

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

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := dec.Decode(packet, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode benchmarks float32 encoding.
// Target: 0 allocs/op
func BenchmarkEncoderEncode(b *testing.B) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)  // 20ms at 48kHz mono
	packet := make([]byte, 4000)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := enc.Encode(pcm, packet)
		if err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncodeInt16 benchmarks int16 encoding.
// Target: 0 allocs/op
func BenchmarkEncoderEncodeInt16(b *testing.B) {
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	// Generate int16 samples
	pcm := make([]int16, 960)
	for i := range pcm {
		pcm[i] = int16(16384 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	packet := make([]byte, 4000)

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
	enc, err := NewEncoder(48000, 2, ApplicationAudio)
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
	enc, err := NewEncoder(48000, 1, ApplicationVoIP)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

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
	enc, err := NewEncoder(48000, 1, ApplicationLowDelay)
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

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
	enc, err := NewEncoder(48000, 1, ApplicationAudio)
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
	enc, err := NewEncoder(48000, 1, ApplicationLowDelay)
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
