package testvectors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestDiagnosticFindDrift07(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	name := "testvector07"
	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	var allDecoded []int16
	for _, pkt := range packets {
		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			fs := getFrameSizeFromConfig(pkt.Data[0] >> 3)
			zeros := make([]int16, fs*2)
			allDecoded = append(allDecoded, zeros...)
			continue
		}
		allDecoded = append(allDecoded, pcm...)
	}

	refData, _ := os.ReadFile(decFile)
	refSamples := make([]int16, len(refData)/2)
	for i := range refSamples {
		refSamples[i] = int16(binary.LittleEndian.Uint16(refData[i*2:]))
	}

	minLen := len(allDecoded)
	if len(refSamples) < minLen {
		minLen = len(refSamples)
	}

	// Scan per-packet quality and correlate with frame size
	sampleOff := 0
	prevFS := 0
	for i, pkt := range packets {
		toc := pkt.Data[0]
		config := toc >> 3
		fs := getFrameSizeFromConfig(config)
		pktSamples := fs * 2 // stereo

		if sampleOff+pktSamples > minLen {
			break
		}

		q := ComputeQuality(allDecoded[sampleOff:sampleOff+pktSamples], refSamples[sampleOff:sampleOff+pktSamples], 48000)

		if q < -50 {
			t.Logf("BAD pkt %4d: config=%2d fs=%4d (%.1fms) Q=%7.1f offset=%d  prevFS=%d",
				i, config, fs, float64(fs)/48.0, q, sampleOff, prevFS)
			// Show a few samples
			end := sampleOff + 10
			if end > minLen {
				end = minLen
			}
			for j := sampleOff; j < end; j++ {
				t.Logf("    [%d] dec=%6d ref=%6d diff=%6d",
					j, allDecoded[j], refSamples[j], int(allDecoded[j])-int(refSamples[j]))
			}
		}

		prevFS = fs
		sampleOff += pktSamples
	}
}

func TestDiagnosticVector07PerPacketDiff(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	name := "testvector07"
	bitFile := filepath.Join(testVectorDir, name+".bit")
	decFile := filepath.Join(testVectorDir, name+".dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("Failed to parse %s: %v", bitFile, err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("Failed to create decoder: %v", err)
	}

	var allDecoded []int16
	for _, pkt := range packets {
		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			fs := getFrameSizeFromConfig(pkt.Data[0] >> 3)
			zeros := make([]int16, fs*2)
			allDecoded = append(allDecoded, zeros...)
			continue
		}
		allDecoded = append(allDecoded, pcm...)
	}

	refData, _ := os.ReadFile(decFile)
	refSamples := make([]int16, len(refData)/2)
	for i := range refSamples {
		refSamples[i] = int16(binary.LittleEndian.Uint16(refData[i*2:]))
	}

	minLen := len(allDecoded)
	if len(refSamples) < minLen {
		minLen = len(refSamples)
	}

	// Count bad packets by frame size
	type stat struct {
		count    int
		badCount int
	}
	stats := make(map[int]*stat)
	sampleOff := 0

	for _, pkt := range packets {
		fs := getFrameSizeFromConfig(pkt.Data[0] >> 3)
		pktSamples := fs * 2

		if sampleOff+pktSamples > minLen {
			break
		}

		s := stats[fs]
		if s == nil {
			s = &stat{}
			stats[fs] = s
		}
		s.count++

		// Compute max sample diff for this packet
		maxDiff := 0
		for j := sampleOff; j < sampleOff+pktSamples; j++ {
			d := int(allDecoded[j]) - int(refSamples[j])
			if d < 0 {
				d = -d
			}
			if d > maxDiff {
				maxDiff = d
			}
		}
		if maxDiff > 100 {
			s.badCount++
		}

		sampleOff += pktSamples
	}

	t.Log("Per-frame-size statistics:")
	for fs, s := range stats {
		t.Logf("  fs=%4d (%5.1fms): %4d total, %4d bad (maxDiff>100), %.1f%%",
			fs, float64(fs)/48.0, s.count, s.badCount, 100*float64(s.badCount)/float64(s.count))
	}

	// Now check: on the working commit cffc8c5, correlation was 0.9957 for first 10k samples.
	// Something subtly wrong in the PVQ decode is causing wrong output for some bands/packets.
	// Let me check the first bad packet more carefully.
	sampleOff = 0
	for i, pkt := range packets {
		fs := getFrameSizeFromConfig(pkt.Data[0] >> 3)
		pktSamples := fs * 2

		if sampleOff+pktSamples > minLen {
			break
		}

		maxDiff := 0
		for j := sampleOff; j < sampleOff+pktSamples; j++ {
			d := int(allDecoded[j]) - int(refSamples[j])
			if d < 0 {
				d = -d
			}
			if d > maxDiff {
				maxDiff = d
			}
		}

		if maxDiff > 100 {
			toc := pkt.Data[0]
			config := toc >> 3
			t.Logf("\nFirst bad packet: index=%d config=%d fs=%d maxDiff=%d",
				i, config, fs, maxDiff)

			// Compute per-sample correlation
			var corr, p1, p2 float64
			for j := sampleOff; j < sampleOff+pktSamples; j++ {
				d := float64(allDecoded[j])
				r := float64(refSamples[j])
				corr += d * r
				p1 += d * d
				p2 += r * r
			}
			if p1 > 0 && p2 > 0 {
				nc := corr / math.Sqrt(p1*p2)
				t.Logf("  Correlation: %.6f", nc)
			}
			break
		}

		sampleOff += pktSamples
		_ = i
	}
}
