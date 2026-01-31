package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV06FrameSizeQuality compares quality of 10ms vs 20ms frames.
func TestTV06FrameSizeQuality(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(48000, 2)

	var q10ms, q20ms []float64
	var mono10ms, mono20ms, stereo10ms, stereo20ms []float64

	refOffset := 0
	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		toc := pkt.Data[0]
		config := toc >> 3
		stereo := (toc & 0x04) != 0
		frameSize := getFrameSizeFromConfig(config)

		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		var sigPow, noisePow float64
		for j := 0; j < len(pcm); j++ {
			sig := float64(refSlice[j])
			noise := float64(pcm[j]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
		}

		snr := 10 * math.Log10(sigPow/noisePow)
		q := (snr - 48.0) * (100.0 / 48.0)

		if frameSize == 480 { // 10ms
			q10ms = append(q10ms, q)
			if stereo {
				stereo10ms = append(stereo10ms, q)
			} else {
				mono10ms = append(mono10ms, q)
			}
		} else if frameSize == 960 { // 20ms
			q20ms = append(q20ms, q)
			if stereo {
				stereo20ms = append(stereo20ms, q)
			} else {
				mono20ms = append(mono20ms, q)
			}
		}

		refOffset += len(pcm)
	}

	// Calculate averages
	avgQ10ms := average(q10ms)
	avgQ20ms := average(q20ms)
	avgMono10ms := average(mono10ms)
	avgMono20ms := average(mono20ms)
	avgStereo10ms := average(stereo10ms)
	avgStereo20ms := average(stereo20ms)

	t.Logf("Overall:")
	t.Logf("  10ms frames: %d packets, avg Q=%.2f", len(q10ms), avgQ10ms)
	t.Logf("  20ms frames: %d packets, avg Q=%.2f", len(q20ms), avgQ20ms)
	t.Logf("")
	t.Logf("Mono:")
	t.Logf("  10ms frames: %d packets, avg Q=%.2f", len(mono10ms), avgMono10ms)
	t.Logf("  20ms frames: %d packets, avg Q=%.2f", len(mono20ms), avgMono20ms)
	t.Logf("")
	t.Logf("Stereo:")
	t.Logf("  10ms frames: %d packets, avg Q=%.2f", len(stereo10ms), avgStereo10ms)
	t.Logf("  20ms frames: %d packets, avg Q=%.2f", len(stereo20ms), avgStereo20ms)
}

// TestTV06FrameSizeTransitionDetail looks at packets around frame size transition.
func TestTV06FrameSizeTransitionDetail(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(48000, 2)

	// Transitions at packets 313, 939, 1252
	transitions := []int{313, 939, 1252}

	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Check if we're near a transition
		nearTransition := false
		for _, tr := range transitions {
			if i >= tr-5 && i <= tr+5 {
				nearTransition = true
				break
			}
		}

		if nearTransition {
			var sigPow, noisePow float64
			var maxDiff float64
			for j := 0; j < len(pcm); j++ {
				sig := float64(refSlice[j])
				noise := float64(pcm[j]) - sig
				sigPow += sig * sig
				noisePow += noise * noise
				if math.Abs(noise) > maxDiff {
					maxDiff = math.Abs(noise)
				}
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			q := (snr - 48.0) * (100.0 / 48.0)

			toc := pkt.Data[0]
			config := toc >> 3
			stereo := (toc & 0x04) != 0
			frameSize := getFrameSizeFromConfig(config)

			t.Logf("Packet %4d: Q=%8.2f, maxDiff=%6.0f, fs=%3d, stereo=%v, config=%d",
				i, q, maxDiff, frameSize/48, stereo, config)
		}

		refOffset += len(pcm)
	}
}

// TestTV06AfterStereoTransition shows quality just after stereo transition.
func TestTV06AfterStereoTransition(t *testing.T) {
	bitFile := filepath.Join(testVectorDir, "testvector06.bit")
	decFile := filepath.Join(testVectorDir, "testvector06.dec")

	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("Could not read test vector: %v", err)
	}

	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skipf("Could not read reference: %v", err)
	}

	dec, _ := gopus.NewDecoder(48000, 2)

	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := dec.DecodeInt16Slice(pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Show packets 935-950 (around stereo transition at 939)
		if i >= 935 && i <= 950 {
			var sigPow, noisePow float64
			var maxDiff float64
			for j := 0; j < len(pcm); j++ {
				sig := float64(refSlice[j])
				noise := float64(pcm[j]) - sig
				sigPow += sig * sig
				noisePow += noise * noise
				if math.Abs(noise) > maxDiff {
					maxDiff = math.Abs(noise)
				}
			}

			snr := 10 * math.Log10(sigPow/noisePow)
			q := (snr - 48.0) * (100.0 / 48.0)

			toc := pkt.Data[0]
			stereo := (toc & 0x04) != 0
			config := toc >> 3
			frameSize := getFrameSizeFromConfig(config)

			marker := ""
			if i == 939 {
				marker = " <-- STEREO TRANSITION"
			}
			t.Logf("Packet %4d: Q=%8.2f, maxDiff=%6.0f, fs=%3d, stereo=%v%s",
				i, q, maxDiff, frameSize/48, stereo, marker)
		}

		refOffset += len(pcm)
	}
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}
