// transient_error_trace_test.go - Trace error growth in transient frame
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTransientErrorGrowth traces where the error grows in a transient frame
func TestTransientErrorGrowth(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Build up state
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Decode frame 61 (transient)
	goPcm, _ := goDec.DecodeFloat32(packets[61])
	libPcm, libN := libDec.DecodeFloat(packets[61], 5760)

	t.Log("Error growth in transient frame 61:")
	t.Log("Segment (120 samples) | Max Diff | Avg Diff | Segment SNR")
	t.Log("---------------------+----------+----------+------------")

	channels := 2
	segmentSize := 120 // One short block size

	for seg := 0; seg < 8; seg++ {
		startSample := seg * segmentSize
		endSample := (seg + 1) * segmentSize

		var maxDiff, sumDiff, sig, noise float64
		count := 0

		for i := startSample; i < endSample && i < libN; i++ {
			for ch := 0; ch < channels; ch++ {
				idx := i*channels + ch
				if idx < len(goPcm) && idx < libN*channels {
					s := float64(libPcm[idx])
					g := float64(goPcm[idx])
					d := g - s

					sig += s * s
					noise += d * d

					if math.Abs(d) > maxDiff {
						maxDiff = math.Abs(d)
					}
					sumDiff += math.Abs(d)
					count++
				}
			}
		}

		avgDiff := sumDiff / float64(count)
		snr := 10 * math.Log10(sig/noise)

		t.Logf("Block %d [%4d-%4d] | %.2e | %.2e | %6.1f dB",
			seg, startSample, endSample-1, maxDiff, avgDiff, snr)
	}

	// Also check what happens IMMEDIATELY AFTER the frame
	t.Log("\nChecking first 120 samples of frame 62:")

	goPcm62, _ := goDec.DecodeFloat32(packets[62])
	libPcm62, libN62 := libDec.DecodeFloat(packets[62], 5760)

	var maxDiff62, sumDiff62, sig62, noise62 float64
	for i := 0; i < 120 && i < libN62; i++ {
		for ch := 0; ch < channels; ch++ {
			idx := i*channels + ch
			if idx < len(goPcm62) && idx < libN62*channels {
				s := float64(libPcm62[idx])
				g := float64(goPcm62[idx])
				d := g - s
				sig62 += s * s
				noise62 += d * d
				if math.Abs(d) > maxDiff62 {
					maxDiff62 = math.Abs(d)
				}
				sumDiff62 += math.Abs(d)
			}
		}
	}
	avgDiff62 := sumDiff62 / 240.0
	snr62 := 10 * math.Log10(sig62/noise62)
	t.Logf("Frame 62 [0-119]: max=%.2e, avg=%.2e, SNR=%.1f dB", maxDiff62, avgDiff62, snr62)

	// And the LAST 120 samples of frame 62 (where the zeros would affect things)
	t.Log("\nChecking last 120 samples of frame 62:")
	startLast := libN62 - 120
	var maxDiffLast, sumDiffLast, sigLast, noiseLast float64
	for i := startLast; i < libN62; i++ {
		for ch := 0; ch < channels; ch++ {
			idx := i*channels + ch
			if idx < len(goPcm62) && idx < libN62*channels {
				s := float64(libPcm62[idx])
				g := float64(goPcm62[idx])
				d := g - s
				sigLast += s * s
				noiseLast += d * d
				if math.Abs(d) > maxDiffLast {
					maxDiffLast = math.Abs(d)
				}
				sumDiffLast += math.Abs(d)
			}
		}
	}
	avgDiffLast := sumDiffLast / 240.0
	snrLast := 10 * math.Log10(sigLast/noiseLast)
	t.Logf("Frame 62 [%d-%d]: max=%.2e, avg=%.2e, SNR=%.1f dB", startLast, libN62-1, maxDiffLast, avgDiffLast, snrLast)
}
