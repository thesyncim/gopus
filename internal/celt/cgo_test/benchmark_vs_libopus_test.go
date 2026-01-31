// Package cgo provides benchmarks comparing gopus vs libopus performance.
package cgo

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

func init() {
	// Disable CELT tracing for benchmarks - this is critical for accurate timing
	celt.SetTracer(&celt.NoopTracer{})
}

// loadAllPackets loads all packets from a .bit file
func loadAllPackets(bitFile string) ([][]byte, error) {
	data, err := os.ReadFile(bitFile)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:]) // enc_final_range
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets, nil
}

// BenchmarkDecodeGopus benchmarks gopus decoder throughput
func BenchmarkDecodeGopus(b *testing.B) {
	// Ensure tracing is disabled
	celt.SetTracer(&celt.NoopTracer{})

	testVectors := []struct {
		name     string
		channels int
	}{
		{"testvector01", 2}, // CELT stereo - passes
		{"testvector11", 2}, // CELT stereo - passes
	}

	for _, tv := range testVectors {
		b.Run(tv.name, func(b *testing.B) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tv.name + ".bit"
			packets, err := loadAllPackets(bitFile)
			if err != nil {
				b.Skipf("Could not load packets: %v", err)
			}
			if len(packets) == 0 {
				b.Skip("No packets")
			}

			// Determine channels from first packet
			toc := gopus.ParseTOC(packets[0][0])
			channels := 1
			if toc.Stereo {
				channels = 2
			}

			dec, _ := gopus.NewDecoderDefault(48000, channels)

			b.ResetTimer()
			b.ReportAllocs()

			var totalSamples int
			for i := 0; i < b.N; i++ {
				for _, pkt := range packets {
					out, err := dec.DecodeFloat32(pkt)
					if err == nil {
						totalSamples += len(out) / channels
					}
				}
			}

			b.ReportMetric(float64(totalSamples)/float64(b.N), "samples/op")
		})
	}
}

// BenchmarkDecodeLibopus benchmarks libopus decoder throughput
func BenchmarkDecodeLibopus(b *testing.B) {
	testVectors := []struct {
		name     string
		channels int
	}{
		{"testvector01", 2}, // CELT stereo - passes
		{"testvector11", 2}, // CELT stereo - passes
	}

	for _, tv := range testVectors {
		b.Run(tv.name, func(b *testing.B) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tv.name + ".bit"
			packets, err := loadAllPackets(bitFile)
			if err != nil {
				b.Skipf("Could not load packets: %v", err)
			}
			if len(packets) == 0 {
				b.Skip("No packets")
			}

			// Determine channels from first packet
			toc := gopus.ParseTOC(packets[0][0])
			channels := 1
			if toc.Stereo {
				channels = 2
			}

			libDec, _ := NewLibopusDecoder(48000, channels)
			if libDec == nil {
				b.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			b.ResetTimer()
			b.ReportAllocs()

			var totalSamples int
			for i := 0; i < b.N; i++ {
				for _, pkt := range packets {
					_, n := libDec.DecodeFloat(pkt, 5760)
					if n > 0 {
						totalSamples += n
					}
				}
			}

			b.ReportMetric(float64(totalSamples)/float64(b.N), "samples/op")
		})
	}
}

// BenchmarkDecodeSinglePacketGopus benchmarks single packet decoding with gopus
func BenchmarkDecodeSinglePacketGopus(b *testing.B) {
	// Ensure tracing is disabled
	celt.SetTracer(&celt.NoopTracer{})

	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"
	packets, err := loadAllPackets(bitFile)
	if err != nil || len(packets) == 0 {
		b.Skip("Could not load packets")
	}

	// Use first packet
	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	dec, _ := gopus.NewDecoderDefault(48000, channels)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dec.DecodeFloat32(pkt)
	}

	b.ReportMetric(float64(toc.FrameSize), "samples/op")
}

// BenchmarkDecodeSinglePacketLibopus benchmarks single packet decoding with libopus
func BenchmarkDecodeSinglePacketLibopus(b *testing.B) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"
	packets, err := loadAllPackets(bitFile)
	if err != nil || len(packets) == 0 {
		b.Skip("Could not load packets")
	}

	// Use first packet
	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	libDec, _ := NewLibopusDecoder(48000, channels)
	if libDec == nil {
		b.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		libDec.DecodeFloat(pkt, 5760)
	}

	b.ReportMetric(float64(toc.FrameSize), "samples/op")
}

// TestBenchmarkComparison runs a simple timing comparison and prints results
func TestBenchmarkComparison(t *testing.T) {
	testVectors := []struct {
		name string
		mode string
	}{
		{"testvector01", "CELT"},
		{"testvector11", "CELT"},
	}

	type result struct {
		name       string
		mode       string
		packets    int
		goTimeUs   float64
		libTimeUs  float64
		goSamples  int
		libSamples int
		snrDB      float64
		speedRatio float64
	}

	var results []result

	iterations := 10

	for _, tv := range testVectors {
		bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + tv.name + ".bit"
		packets, err := loadAllPackets(bitFile)
		if err != nil {
			t.Logf("Skipping %s: %v", tv.name, err)
			continue
		}
		if len(packets) == 0 {
			continue
		}

		// Determine channels from first packet
		toc := gopus.ParseTOC(packets[0][0])
		channels := 1
		if toc.Stereo {
			channels = 2
		}

		// Warm up and collect samples for SNR
		goDec, _ := gopus.NewDecoderDefault(48000, channels)
		libDec, _ := NewLibopusDecoder(48000, channels)
		if libDec == nil {
			t.Logf("Skipping %s: could not create libopus decoder", tv.name)
			continue
		}

		var goSamples, libSamples []float32
		for _, pkt := range packets {
			goOut, _ := goDec.DecodeFloat32(pkt)
			goSamples = append(goSamples, goOut...)

			libOut, n := libDec.DecodeFloat(pkt, 5760)
			if n > 0 {
				libSamples = append(libSamples, libOut[:n*channels]...)
			}
		}

		// Calculate SNR
		minLen := len(goSamples)
		if len(libSamples) < minLen {
			minLen = len(libSamples)
		}
		var noise, signal float64
		for i := 0; i < minLen; i++ {
			diff := float64(goSamples[i]) - float64(libSamples[i])
			noise += diff * diff
			signal += float64(libSamples[i]) * float64(libSamples[i])
		}
		snr := 10 * math.Log10(signal/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999.0
		}

		libDec.Destroy()

		// Benchmark gopus
		goDec2, _ := gopus.NewDecoderDefault(48000, channels)
		goStart := time.Now()
		for iter := 0; iter < iterations; iter++ {
			for _, pkt := range packets {
				goDec2.DecodeFloat32(pkt)
			}
		}
		goElapsed := time.Since(goStart)

		// Benchmark libopus
		libDec2, _ := NewLibopusDecoder(48000, channels)
		libStart := time.Now()
		for iter := 0; iter < iterations; iter++ {
			for _, pkt := range packets {
				libDec2.DecodeFloat(pkt, 5760)
			}
		}
		libElapsed := time.Since(libStart)
		libDec2.Destroy()

		goTimeUs := float64(goElapsed.Microseconds()) / float64(iterations)
		libTimeUs := float64(libElapsed.Microseconds()) / float64(iterations)

		results = append(results, result{
			name:       tv.name,
			mode:       tv.mode,
			packets:    len(packets),
			goTimeUs:   goTimeUs,
			libTimeUs:  libTimeUs,
			goSamples:  len(goSamples),
			libSamples: len(libSamples),
			snrDB:      snr,
			speedRatio: libTimeUs / goTimeUs,
		})
	}

	// Print results
	t.Log("")
	t.Log("=== gopus vs libopus Benchmark Comparison ===")
	t.Log("")
	t.Logf("%-14s | %-6s | %7s | %12s | %12s | %10s | %s",
		"Vector", "Mode", "Packets", "gopus (µs)", "libopus (µs)", "SNR (dB)", "Speed Ratio")
	t.Log("---------------|--------|---------|--------------|--------------|------------|------------")

	for _, r := range results {
		status := "SLOWER"
		if r.speedRatio > 1.0 {
			status = fmt.Sprintf("%.2fx FASTER", r.speedRatio)
		} else if r.speedRatio > 0.9 {
			status = "~SAME"
		} else {
			status = fmt.Sprintf("%.2fx slower", 1.0/r.speedRatio)
		}

		snrStr := fmt.Sprintf("%.1f", r.snrDB)
		if r.snrDB > 100 {
			snrStr = ">100 (exact)"
		}

		t.Logf("%-14s | %-6s | %7d | %12.1f | %12.1f | %10s | %s",
			r.name, r.mode, r.packets, r.goTimeUs, r.libTimeUs, snrStr, status)
	}
}

// BenchmarkDecodeStreamGopus benchmarks continuous stream decoding with gopus
func BenchmarkDecodeStreamGopus(b *testing.B) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"
	packets, err := loadAllPackets(bitFile)
	if err != nil || len(packets) == 0 {
		b.Skip("Could not load packets")
	}

	toc := gopus.ParseTOC(packets[0][0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	// Pre-allocate output buffer
	outBuf := make([]float32, 5760*channels)
	dec, _ := gopus.NewDecoderDefault(48000, channels)

	b.ResetTimer()
	b.ReportAllocs()

	var totalBytes int
	for i := 0; i < b.N; i++ {
		for _, pkt := range packets {
			n, _ := dec.Decode(pkt, outBuf)
			totalBytes += n * 4 // float32 = 4 bytes
		}
	}

	b.SetBytes(int64(totalBytes / b.N))
}

// BenchmarkDecodeStreamLibopus benchmarks continuous stream decoding with libopus
func BenchmarkDecodeStreamLibopus(b *testing.B) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector01.bit"
	packets, err := loadAllPackets(bitFile)
	if err != nil || len(packets) == 0 {
		b.Skip("Could not load packets")
	}

	toc := gopus.ParseTOC(packets[0][0])
	channels := 1
	if toc.Stereo {
		channels = 2
	}

	libDec, _ := NewLibopusDecoder(48000, channels)
	if libDec == nil {
		b.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	b.ResetTimer()
	b.ReportAllocs()

	var totalBytes int
	for i := 0; i < b.N; i++ {
		for _, pkt := range packets {
			_, n := libDec.DecodeFloat(pkt, 5760)
			if n > 0 {
				totalBytes += n * channels * 4
			}
		}
	}

	b.SetBytes(int64(totalBytes / b.N))
}
