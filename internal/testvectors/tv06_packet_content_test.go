package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV06PacketContentAnalysis looks at the actual signal content at problem packets.
func TestTV06PacketContentAnalysis(t *testing.T) {
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

	dec, _ := gopus.NewDecoderDefault(48000, 2)

	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// Show packets 1494-1505
		if i >= 1494 && i <= 1505 {
			// Compute various signal characteristics
			var refMax, refMin, decMax, decMin int16
			var refEnergy, decEnergy float64

			refMax, refMin = -32768, 32767
			decMax, decMin = -32768, 32767

			for j := 0; j < len(pcm); j++ {
				if refSlice[j] > refMax {
					refMax = refSlice[j]
				}
				if refSlice[j] < refMin {
					refMin = refSlice[j]
				}
				if pcm[j] > decMax {
					decMax = pcm[j]
				}
				if pcm[j] < decMin {
					decMin = pcm[j]
				}
				refEnergy += float64(refSlice[j]) * float64(refSlice[j])
				decEnergy += float64(pcm[j]) * float64(pcm[j])
			}

			refRMS := math.Sqrt(refEnergy / float64(len(pcm)))
			decRMS := math.Sqrt(decEnergy / float64(len(pcm)))

			// Dynamic range
			refDynRange := float64(refMax) - float64(refMin)
			decDynRange := float64(decMax) - float64(decMin)

			t.Logf("Packet %4d:", i)
			t.Logf("  Ref: RMS=%8.1f, range=[%6d,%6d], dyn=%8.1f", refRMS, refMin, refMax, refDynRange)
			t.Logf("  Dec: RMS=%8.1f, range=[%6d,%6d], dyn=%8.1f", decRMS, decMin, decMax, decDynRange)
			t.Logf("  Ratio: RMS=%.4f, dyn=%.4f", decRMS/refRMS, decDynRange/refDynRange)
		}

		refOffset += len(pcm)
	}
}

// TestTV06ErrorDistribution looks at where errors occur within bad packets.
func TestTV06ErrorDistribution(t *testing.T) {
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

	dec, _ := gopus.NewDecoderDefault(48000, 2)

	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// For packets 1497-1500, show error distribution
		if i >= 1497 && i <= 1500 {
			t.Logf("Packet %d error distribution:", i)

			// Divide frame into 8 sections (for 10ms stereo: 960 samples -> 120 per section)
			sectionSize := len(pcm) / 8
			for s := 0; s < 8; s++ {
				start := s * sectionSize
				end := start + sectionSize
				if end > len(pcm) {
					end = len(pcm)
				}

				var sigPow, noisePow float64
				var maxDiff float64
				for j := start; j < end; j++ {
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

				t.Logf("  Section %d [%4d-%4d]: Q=%8.2f, maxDiff=%6.0f",
					s, start, end-1, q, maxDiff)
			}
		}

		refOffset += len(pcm)
	}
}

// TestTV06LRChannelErrorCorrelation checks if L and R errors are correlated.
func TestTV06LRChannelErrorCorrelation(t *testing.T) {
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

	dec, _ := gopus.NewDecoderDefault(48000, 2)

	refOffset := 0
	for i, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}

		pcm, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			continue
		}

		if refOffset+len(pcm) > len(reference) {
			break
		}

		refSlice := reference[refOffset : refOffset+len(pcm)]

		// For packets 1497-1500
		if i >= 1497 && i <= 1500 {
			// Calculate L and R errors
			var errorsL, errorsR []float64
			for j := 0; j < len(pcm)/2; j++ {
				errL := float64(pcm[j*2]) - float64(refSlice[j*2])
				errR := float64(pcm[j*2+1]) - float64(refSlice[j*2+1])
				errorsL = append(errorsL, errL)
				errorsR = append(errorsR, errR)
			}

			// Correlation between L and R errors
			var sumL, sumR, sumL2, sumR2, sumLR float64
			n := float64(len(errorsL))
			for j := range errorsL {
				sumL += errorsL[j]
				sumR += errorsR[j]
				sumL2 += errorsL[j] * errorsL[j]
				sumR2 += errorsR[j] * errorsR[j]
				sumLR += errorsL[j] * errorsR[j]
			}

			meanL := sumL / n
			meanR := sumR / n
			varL := sumL2/n - meanL*meanL
			varR := sumR2/n - meanR*meanR
			cov := sumLR/n - meanL*meanR

			corr := 0.0
			if varL > 0 && varR > 0 {
				corr = cov / (math.Sqrt(varL) * math.Sqrt(varR))
			}

			// Check if errors have same sign pattern
			sameSign := 0
			oppositeSign := 0
			for j := range errorsL {
				if (errorsL[j] >= 0 && errorsR[j] >= 0) || (errorsL[j] < 0 && errorsR[j] < 0) {
					sameSign++
				} else {
					oppositeSign++
				}
			}

			t.Logf("Packet %d L/R error analysis:", i)
			t.Logf("  Correlation: %.4f", corr)
			t.Logf("  Same sign: %d (%.1f%%), Opposite sign: %d (%.1f%%)",
				sameSign, float64(sameSign)*100/n, oppositeSign, float64(oppositeSign)*100/n)
			t.Logf("  Mean L: %.2f, Mean R: %.2f", meanL, meanR)
		}

		refOffset += len(pcm)
	}
}
