package testvectors

import (
	"flag"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

const (
	benchmarkDecodeSampleRate = 48000
	benchmarkDecodeChannels   = 2
)

type benchmarkVector struct {
	name           string
	packets        []Packet
	packetBytes    int64
	decodedSamples int64
}

func BenchmarkDecodeOfficialTestVectors(b *testing.B) {
	vectors := loadBenchmarkVectors(b)

	b.Run("Float32", func(b *testing.B) {
		b.Run("all", func(b *testing.B) {
			benchmarkDecodeFloat32Vectors(b, vectors)
		})
		for _, vector := range vectors {
			vector := vector
			b.Run(vector.name, func(b *testing.B) {
				benchmarkDecodeFloat32Vectors(b, []benchmarkVector{vector})
			})
		}
	})

	b.Run("Int16", func(b *testing.B) {
		b.Run("all", func(b *testing.B) {
			benchmarkDecodeInt16Vectors(b, vectors)
		})
		for _, vector := range vectors {
			vector := vector
			b.Run(vector.name, func(b *testing.B) {
				benchmarkDecodeInt16Vectors(b, []benchmarkVector{vector})
			})
		}
	})
}

func loadBenchmarkVectors(b *testing.B) []benchmarkVector {
	b.Helper()
	if err := ensureTestVectors(b); err != nil {
		b.Skipf("skipping official test-vector benchmarks: %v", err)
	}

	vectors := make([]benchmarkVector, 0, len(testVectorNames))
	for _, name := range testVectorNames {
		bitFile := filepath.Join(testVectorDir, name+".bit")
		packets, err := ReadBitstreamFile(bitFile)
		if err != nil {
			b.Fatalf("read %s: %v", bitFile, err)
		}
		if len(packets) == 0 {
			b.Fatalf("%s has no packets", bitFile)
		}

		vector := benchmarkVector{name: name, packets: packets}
		for _, packet := range packets {
			vector.packetBytes += int64(len(packet.Data))
		}
		vector.decodedSamples = benchmarkDecodedSamples(b, vector)
		vectors = append(vectors, vector)
	}

	return vectors
}

func benchmarkDecodedSamples(b *testing.B, vector benchmarkVector) int64 {
	b.Helper()
	dec, pcm := newBenchmarkDecoder(b)
	decoded, err := decodeBenchmarkVectorFloat32(dec, vector.packets, pcm)
	if err != nil {
		b.Fatalf("%s setup decode: %v", vector.name, err)
	}
	if decoded <= 0 {
		b.Fatalf("%s setup decode produced no samples", vector.name)
	}
	return decoded
}

func newBenchmarkDecoder(b *testing.B) (*gopus.Decoder, []float32) {
	b.Helper()
	cfg := gopus.DefaultDecoderConfig(benchmarkDecodeSampleRate, benchmarkDecodeChannels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}
	return dec, make([]float32, cfg.MaxPacketSamples*cfg.Channels)
}

func benchmarkDecodeFloat32Vectors(b *testing.B, vectors []benchmarkVector) {
	dec, pcm := newBenchmarkDecoder(b)
	expectedSamples, packetCount, packetBytes := summarizeBenchmarkVectors(vectors)

	if decoded, err := decodeBenchmarkVectorsFloat32(dec, vectors, pcm); err != nil {
		b.Fatalf("warmup decode: %v", err)
	} else if decoded != expectedSamples {
		b.Fatalf("warmup decoded samples=%d want=%d", decoded, expectedSamples)
	}

	assertBenchmarkZeroAllocs(b, "Decode", expectedSamples, func() (int64, error) {
		dec.Reset()
		return decodeBenchmarkVectorsFloat32(dec, vectors, pcm)
	})
	dec.Reset()
	b.SetBytes(packetBytes)
	b.ReportAllocs()
	b.ResetTimer()

	var decodedTotal int64
	for i := 0; i < b.N; i++ {
		decoded, err := decodeBenchmarkVectorsFloat32(dec, vectors, pcm)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
		decodedTotal += decoded
	}

	b.StopTimer()
	if decodedTotal != expectedSamples*int64(b.N) {
		b.Fatalf("decoded samples=%d want=%d", decodedTotal, expectedSamples*int64(b.N))
	}
	reportDecodeBenchmarkMetrics(b, expectedSamples, packetCount)
}

func benchmarkDecodeInt16Vectors(b *testing.B, vectors []benchmarkVector) {
	cfg := gopus.DefaultDecoderConfig(benchmarkDecodeSampleRate, benchmarkDecodeChannels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		b.Fatalf("NewDecoder: %v", err)
	}
	pcm := make([]int16, cfg.MaxPacketSamples*cfg.Channels)
	expectedSamples, packetCount, packetBytes := summarizeBenchmarkVectors(vectors)

	if decoded, err := decodeBenchmarkVectorsInt16(dec, vectors, pcm); err != nil {
		b.Fatalf("warmup DecodeInt16: %v", err)
	} else if decoded != expectedSamples {
		b.Fatalf("warmup decoded samples=%d want=%d", decoded, expectedSamples)
	}

	assertBenchmarkZeroAllocs(b, "DecodeInt16", expectedSamples, func() (int64, error) {
		dec.Reset()
		return decodeBenchmarkVectorsInt16(dec, vectors, pcm)
	})
	dec.Reset()
	b.SetBytes(packetBytes)
	b.ReportAllocs()
	b.ResetTimer()

	var decodedTotal int64
	for i := 0; i < b.N; i++ {
		decoded, err := decodeBenchmarkVectorsInt16(dec, vectors, pcm)
		if err != nil {
			b.Fatalf("DecodeInt16: %v", err)
		}
		decodedTotal += decoded
	}

	b.StopTimer()
	if decodedTotal != expectedSamples*int64(b.N) {
		b.Fatalf("decoded samples=%d want=%d", decodedTotal, expectedSamples*int64(b.N))
	}
	reportDecodeBenchmarkMetrics(b, expectedSamples, packetCount)
}

func summarizeBenchmarkVectors(vectors []benchmarkVector) (samples int64, packets int64, packetBytes int64) {
	for _, vector := range vectors {
		samples += vector.decodedSamples
		packets += int64(len(vector.packets))
		packetBytes += vector.packetBytes
	}
	return samples, packets, packetBytes
}

func decodeBenchmarkVectorsFloat32(dec *gopus.Decoder, vectors []benchmarkVector, pcm []float32) (int64, error) {
	var decoded int64
	for _, vector := range vectors {
		dec.Reset()
		n, err := decodeBenchmarkVectorFloat32(dec, vector.packets, pcm)
		if err != nil {
			return decoded, fmt.Errorf("%s: %w", vector.name, err)
		}
		decoded += n
	}
	return decoded, nil
}

func decodeBenchmarkVectorFloat32(dec *gopus.Decoder, packets []Packet, pcm []float32) (int64, error) {
	var decoded int64
	for i, packet := range packets {
		n, err := dec.Decode(packet.Data, pcm)
		if err != nil {
			return decoded, fmt.Errorf("packet %d: %w", i, err)
		}
		decoded += int64(n)
	}
	return decoded, nil
}

func decodeBenchmarkVectorsInt16(dec *gopus.Decoder, vectors []benchmarkVector, pcm []int16) (int64, error) {
	var decoded int64
	for _, vector := range vectors {
		dec.Reset()
		n, err := decodeBenchmarkVectorInt16(dec, vector.packets, pcm)
		if err != nil {
			return decoded, fmt.Errorf("%s: %w", vector.name, err)
		}
		decoded += n
	}
	return decoded, nil
}

func decodeBenchmarkVectorInt16(dec *gopus.Decoder, packets []Packet, pcm []int16) (int64, error) {
	var decoded int64
	for i, packet := range packets {
		n, err := dec.DecodeInt16(packet.Data, pcm)
		if err != nil {
			return decoded, fmt.Errorf("packet %d: %w", i, err)
		}
		decoded += int64(n)
	}
	return decoded, nil
}

func assertBenchmarkZeroAllocs(b *testing.B, name string, expectedSamples int64, decode func() (int64, error)) {
	b.Helper()
	if benchmarkCPUProfileEnabled() {
		return
	}
	var decodeErr error
	allocs := testing.AllocsPerRun(3, func() {
		if decodeErr != nil {
			return
		}
		decoded, err := decode()
		if err != nil {
			decodeErr = err
			return
		}
		if decoded != expectedSamples {
			decodeErr = fmt.Errorf("decoded samples=%d want=%d", decoded, expectedSamples)
		}
	})
	if decodeErr != nil {
		b.Fatalf("%s allocation guard: %v", name, decodeErr)
	}
	if allocs != 0 {
		b.Fatalf("%s allocation guard: got %.2f allocs/op, want 0", name, allocs)
	}
}

func benchmarkCPUProfileEnabled() bool {
	profile := flag.Lookup("test.cpuprofile")
	return profile != nil && profile.Value.String() != ""
}

func reportDecodeBenchmarkMetrics(b *testing.B, samples, packets int64) {
	if b.N <= 0 || samples <= 0 || packets <= 0 {
		return
	}
	elapsed := b.Elapsed()
	if elapsed <= 0 {
		return
	}

	totalSamples := float64(samples) * float64(b.N)
	totalPackets := float64(packets) * float64(b.N)
	elapsedNS := float64(elapsed.Nanoseconds())
	audioSeconds := totalSamples / benchmarkDecodeSampleRate

	b.ReportMetric(float64(samples), "samples/op")
	b.ReportMetric(float64(packets), "packets/op")
	b.ReportMetric(float64(samples)*1000/benchmarkDecodeSampleRate, "audio_ms/op")
	b.ReportMetric(elapsedNS/totalSamples, "ns/sample")
	b.ReportMetric(elapsedNS/totalPackets, "ns/packet")
	b.ReportMetric(audioSeconds/elapsed.Seconds(), "x_realtime")
}
