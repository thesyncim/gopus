package testvectors

import (
	"math"
	"path/filepath"
	"sort"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDecoderDivergenceVectors reports per-packet SNR diagnostics for failing vectors.
// This helps localize where decoder output diverges from the RFC 8251 reference.
func TestDecoderDivergenceVectors(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	vectors := []string{
		"testvector02",
		"testvector03",
		"testvector04",
		"testvector05",
		"testvector06",
		"testvector12",
	}

	for _, vector := range vectors {
		t.Run(vector, func(t *testing.T) {
			runDecoderDivergenceVector(t, vector)
		})
	}
}

type packetDivergence struct {
	index            int
	mode             string
	frameSize        int
	snr              float64
	maxDiff          int16
	avgDiff          float64
	isModeTransition bool
	prevMode         string
}

func runDecoderDivergenceVector(t *testing.T, vector string) {
	bitFile := filepath.Join(testVectorDir, vector+".bit")
	decFile := filepath.Join(testVectorDir, vector+".dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Fatalf("read bitstream: %v", err)
	}
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Fatalf("read reference: %v", err)
	}

	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("new decoder: %v", err)
	}

	var decoded []int16
	var stats []packetDivergence
	prevMode := ""
	lowSNRCount := 0
	const lowSNRThreshold = 40.0
	const rmsActiveThreshold = 5.0 // int16 RMS threshold to treat as "active"

	var totalSignalPower, totalNoisePower float64
	var activeSignalPower, activeNoisePower float64
	activePackets := 0

	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		tocByte := pkt.Data[0]
		cfg := tocByte >> 3
		fs := getFrameSizeFromConfig(cfg)
		mode := getModeFromConfig(cfg)

		isModeTransition := prevMode != "" && prevMode != mode

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			t.Logf("packet %d decode error: %v", i, err)
			zeros := make([]int16, fs*2)
			pcm = zeros
		}
		decoded = append(decoded, pcm...)

		refStart := len(decoded) - len(pcm)
		refEnd := refStart + len(pcm)
		if refStart < 0 {
			refStart = 0
		}
		if refEnd > len(reference) {
			refEnd = len(reference)
		}
		if refStart >= refEnd {
			prevMode = mode
			continue
		}

		segDecoded := decoded[len(decoded)-len(pcm):]
		segRef := reference[refStart:refEnd]

		snr, maxDiff, avgDiff, sigPower, noisePower := packetSNRStats(segDecoded, segRef)
		if snr < lowSNRThreshold {
			lowSNRCount++
		}

		totalSignalPower += sigPower
		totalNoisePower += noisePower
		if sigPower > 0 {
			rms := math.Sqrt(sigPower)
			if rms >= rmsActiveThreshold {
				activeSignalPower += sigPower
				activeNoisePower += noisePower
				activePackets++
			}
		}

		stats = append(stats, packetDivergence{
			index:            i,
			mode:             mode,
			frameSize:        fs,
			snr:              snr,
			maxDiff:          maxDiff,
			avgDiff:          avgDiff,
			isModeTransition: isModeTransition,
			prevMode:         prevMode,
		})

		prevMode = mode
	}

	t.Logf("packets: %d, lowSNR(<%.1f dB): %d", len(stats), lowSNRThreshold, lowSNRCount)
	if totalSignalPower > 0 && totalNoisePower > 0 {
		totalSNR := 10 * math.Log10(totalSignalPower/totalNoisePower)
		t.Logf("overall SNR: %.2f dB", totalSNR)
	}
	if activeSignalPower > 0 && activeNoisePower > 0 {
		activeSNR := 10 * math.Log10(activeSignalPower/activeNoisePower)
		t.Logf("active-only SNR (RMS>=%.1f): %.2f dB, packets=%d", rmsActiveThreshold, activeSNR, activePackets)
	}

	// Report worst 5 packets by SNR
	sort.Slice(stats, func(i, j int) bool { return stats[i].snr < stats[j].snr })
	worst := stats
	if len(worst) > 5 {
		worst = worst[:5]
	}
	t.Log("worst 5 packets:")
	for _, ps := range worst {
		t.Logf("  pkt %d mode=%s fs=%d SNR=%.2f dB maxDiff=%d avgDiff=%.2f",
			ps.index, ps.mode, ps.frameSize, ps.snr, ps.maxDiff, ps.avgDiff)
	}

	// Report mode transitions with low SNR
	t.Log("mode transitions with low SNR:")
	found := false
	for _, ps := range stats {
		if ps.isModeTransition && ps.snr < lowSNRThreshold {
			found = true
			t.Logf("  pkt %d: %s -> %s, fs=%d, SNR=%.2f dB, maxDiff=%d",
				ps.index, ps.prevMode, ps.mode, ps.frameSize, ps.snr, ps.maxDiff)
		}
	}
	if !found {
		t.Log("  none")
	}
}

func packetSNRStats(decoded, reference []int16) (snr float64, maxDiff int16, avgDiff float64, signalPower float64, noisePower float64) {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return math.Inf(-1), 0, 0, 0, 0
	}

	var sumDiff float64
	for i := 0; i < n; i++ {
		d := decoded[i]
		r := reference[i]
		diff := int32(d) - int32(r)
		if diff < 0 {
			diff = -diff
		}
		if int16(diff) > maxDiff {
			maxDiff = int16(diff)
		}
		sumDiff += float64(diff)
		signalPower += float64(r) * float64(r)
		noisePower += float64(d-r) * float64(d-r)
	}
	if noisePower == 0 {
		return 200.0, maxDiff, sumDiff / float64(n), signalPower, noisePower
	}
	if signalPower == 0 {
		return math.Inf(-1), maxDiff, sumDiff / float64(n), signalPower, noisePower
	}
	snr = 10 * math.Log10(signalPower/noisePower)
	avgDiff = sumDiff / float64(n)
	return snr, maxDiff, avgDiff, signalPower, noisePower
}
