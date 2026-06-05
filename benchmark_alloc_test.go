// benchmark_alloc_test.go benchmarks the public encode/decode paths with
// allocation reporting enabled.
//
// The caller-buffer encode/decode APIs are expected to stay at 0 allocs/op in
// hot paths. Convenience helpers such as EncodeFloat32 allocate by design and
// are benchmarked separately so the two paths do not get conflated.

package gopus_test

import (
	"github.com/thesyncim/gopus"
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

func warmDecoderFloat32Benchmark(b *testing.B, dec *gopus.Decoder, packet []byte, pcm []float32, label string) {
	b.Helper()
	if _, err := dec.Decode(packet, pcm); err != nil {
		b.Fatalf("%s warmup: %v", label, err)
	}
	runtime.GC()
	runtime.Gosched()
}

func warmDecoderInt16Benchmark(b *testing.B, dec *gopus.Decoder, packet []byte, pcm []int16, label string) {
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960) // 20ms at 48kHz mono
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for range 5 {
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)

	for range 5 {
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
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
	for range 5 {
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	// Generate stereo samples (interleaved)
	pcm := make([]float32, 960*2)
	for i := range 960 {
		pcm[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
		pcm[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/48000))
	}
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for range 5 {
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationVoIP})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for range 5 {
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationLowDelay})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	pcm := generateBenchSineWave(960)
	packet := make([]byte, 4000)

	// Warmup: initialize all scratch buffers before timing
	for range 5 {
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

// BenchmarkEncoderEncode_RestrictedCELTCBR benchmarks the CELT CBR workload used
// by the libopus-relative encoder guard.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_RestrictedCELTCBR(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationRestrictedCelt})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		b.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
		b.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		b.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalMusic); err != nil {
		b.Fatalf("SetSignal: %v", err)
	}

	pcm := generateBenchSineWave(960 * 2)
	packet := make([]byte, 4000)

	for range 5 {
		if _, err := enc.Encode(pcm, packet); err != nil {
			b.Fatalf("Encode warmup: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := enc.Encode(pcm, packet); err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_RestrictedCELTCBRAfterReset keeps the libopus-relative
// benchmark contract honest: every measured operation starts a new stream.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_RestrictedCELTCBRAfterReset(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationRestrictedCelt})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		b.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
		b.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		b.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalMusic); err != nil {
		b.Fatalf("SetSignal: %v", err)
	}

	pcm := generateBenchSineWave(960 * 2)
	packet := make([]byte, 4000)

	for range 5 {
		enc.Reset()
		if _, err := enc.Encode(pcm, packet); err != nil {
			b.Fatalf("Encode warmup: %v", err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		enc.Reset()
		if _, err := enc.Encode(pcm, packet); err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}

// BenchmarkEncoderEncode_RestrictedCELTCBRStreamAfterReset matches one
// per-case operation in tools/encoderbenchcmp.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_RestrictedCELTCBRStreamAfterReset(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 2, Application: gopus.ApplicationRestrictedCelt})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		b.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
		b.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		b.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalMusic); err != nil {
		b.Fatalf("SetSignal: %v", err)
	}

	frames := 50
	pcm := generateBenchSineWave(960 * 2 * frames)
	packet := make([]byte, 4000)

	for range 5 {
		enc.Reset()
		for frame := range frames {
			start := frame * 960 * 2
			if _, err := enc.Encode(pcm[start:start+960*2], packet); err != nil {
				b.Fatalf("Encode warmup frame %d: %v", frame, err)
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		enc.Reset()
		for frame := range frames {
			start := frame * 960 * 2
			if _, err := enc.Encode(pcm[start:start+960*2], packet); err != nil {
				b.Fatalf("Encode frame %d: %v", frame, err)
			}
		}
	}
}

// BenchmarkEncoderEncode_RestrictedCELT5msCBRStreamAfterReset matches the
// short-frame CELT per-case operation in tools/encoderbenchcmp.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_RestrictedCELT5msCBRStreamAfterReset(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationRestrictedCelt})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(240); err != nil {
		b.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthFullband); err != nil {
		b.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		b.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalMusic); err != nil {
		b.Fatalf("SetSignal: %v", err)
	}

	frames := 200
	pcm := generateBenchSineWave(240 * frames)
	packet := make([]byte, 4000)

	for range 5 {
		enc.Reset()
		for frame := range frames {
			start := frame * 240
			if _, err := enc.Encode(pcm[start:start+240], packet); err != nil {
				b.Fatalf("Encode warmup frame %d: %v", frame, err)
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		enc.Reset()
		for frame := range frames {
			start := frame * 240
			if _, err := enc.Encode(pcm[start:start+240], packet); err != nil {
				b.Fatalf("Encode frame %d: %v", frame, err)
			}
		}
	}
}

// BenchmarkEncoderEncode_RestrictedSILKCBRStreamAfterReset matches the SILK
// per-case operation in tools/encoderbenchcmp.
// Target: 0 allocs/op
func BenchmarkEncoderEncode_RestrictedSILKCBRStreamAfterReset(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationRestrictedSilk})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(960); err != nil {
		b.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(gopus.BandwidthWideband); err != nil {
		b.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(32000); err != nil {
		b.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		b.Fatalf("SetBitrateMode: %v", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		b.Fatalf("SetComplexity: %v", err)
	}
	if err := enc.SetSignal(gopus.SignalVoice); err != nil {
		b.Fatalf("SetSignal: %v", err)
	}

	frames := 50
	pcm := generateBenchSineWave(960 * frames)
	packet := make([]byte, 4000)

	for range 5 {
		enc.Reset()
		for frame := range frames {
			start := frame * 960
			if _, err := enc.Encode(pcm[start:start+960], packet); err != nil {
				b.Fatalf("Encode warmup frame %d: %v", frame, err)
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		enc.Reset()
		for frame := range frames {
			start := frame * 960
			if _, err := enc.Encode(pcm[start:start+960], packet); err != nil {
				b.Fatalf("Encode frame %d: %v", frame, err)
			}
		}
	}
}

// BenchmarkRoundTrip benchmarks encode + decode round trip.
// Target: 0 allocs/op
func BenchmarkRoundTrip(b *testing.B) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationLowDelay})
	if err != nil {
		b.Fatalf("NewEncoder: %v", err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
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
	reader, err := gopus.NewReader(gopus.DefaultDecoderConfig(48000, 1), source, gopus.FormatFloat32LE)
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

// longPacketDurations lists the multi-frame packet durations (40/60/80/100/120 ms)
// whose caller-buffer encode paths target 0 allocs/op.
var longPacketDurations = []struct {
	name      string
	frameSize int
}{
	{"40ms", 1920},
	{"60ms", 2880},
	{"80ms", 3840},
	{"100ms", 4800},
	{"120ms", 5760},
}

func benchmarkLongPacketEncode(b *testing.B, app gopus.Application, mode gopus.EncoderMode, bw gopus.Bandwidth, bitrate, channels int) {
	for _, d := range longPacketDurations {
		b.Run(d.name, func(b *testing.B) {
			enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: channels, Application: app})
			if err != nil {
				b.Fatalf("NewEncoder: %v", err)
			}
			if err := enc.SetMode(mode); err != nil {
				b.Fatalf("SetMode: %v", err)
			}
			if err := enc.SetFrameSize(d.frameSize); err != nil {
				b.Fatalf("SetFrameSize: %v", err)
			}
			if err := enc.SetBandwidth(bw); err != nil {
				b.Fatalf("SetBandwidth: %v", err)
			}
			if err := enc.SetBitrate(bitrate); err != nil {
				b.Fatalf("SetBitrate: %v", err)
			}

			pcm := generateBenchSineWave(d.frameSize * channels)
			packet := make([]byte, 4000)

			for range 5 {
				if _, err := enc.Encode(pcm, packet); err != nil {
					b.Fatalf("Encode warmup: %v", err)
				}
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				if _, err := enc.Encode(pcm, packet); err != nil {
					b.Fatalf("Encode: %v", err)
				}
			}
		})
	}
}

// BenchmarkEncoderEncode_LongPacketCELT measures the long CELT multi-frame
// caller-buffer encode path. Target: 0 allocs/op.
func BenchmarkEncoderEncode_LongPacketCELT(b *testing.B) {
	benchmarkLongPacketEncode(b, gopus.ApplicationAudio, gopus.EncoderModeCELT, gopus.BandwidthFullband, 128000, 1)
}

// BenchmarkEncoderEncode_LongPacketHybrid measures the long hybrid multi-frame
// caller-buffer encode path. Target: 0 allocs/op.
func BenchmarkEncoderEncode_LongPacketHybrid(b *testing.B) {
	benchmarkLongPacketEncode(b, gopus.ApplicationAudio, gopus.EncoderModeHybrid, gopus.BandwidthFullband, 64000, 1)
}

// BenchmarkEncoderEncode_LongPacketSILK measures the long SILK multi-frame
// caller-buffer encode path (80/100/120 ms split into 20/40/60 ms SILK frames).
// Target: 0 allocs/op.
func BenchmarkEncoderEncode_LongPacketSILK(b *testing.B) {
	benchmarkLongPacketEncode(b, gopus.ApplicationVoIP, gopus.EncoderModeSILK, gopus.BandwidthWideband, 24000, 1)
}
