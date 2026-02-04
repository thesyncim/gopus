package testvectors

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestDecoderAlignmentVectors checks if decoder output is time-shifted vs RFC 8251 reference.
// A consistent non-zero bestShift suggests delay compensation mismatch (not an audio quality bug).
func TestDecoderAlignmentVectors(t *testing.T) {
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	const maxShift = 200 // samples (stereo interleaved)
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
			bitFile := filepath.Join(testVectorDir, vector+".bit")
			decFile := filepath.Join(testVectorDir, vector+".dec")

			packets, err := ReadBitstreamFile(bitFile)
			if err != nil {
				t.Fatalf("read bitstream: %v", err)
			}
			ref, err := readPCMFile(decFile)
			if err != nil {
				t.Fatalf("read reference: %v", err)
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}

			var decoded []int16
			for _, pkt := range packets {
				pcm, err := decodeInt16(dec, pkt.Data)
				if err != nil {
					t.Fatalf("decode failed: %v", err)
				}
				decoded = append(decoded, pcm...)
			}

			baseQ := ComputeQuality(decoded, ref, 48000)
			scale := bestScale(decoded, ref)
			offset := bestOffset(decoded, ref, scale)
			scaledQ := qualityWithScale(decoded, ref, scale)
			affineQ := qualityWithScaleOffset(decoded, ref, scale, offset)
			bestShift, bestQ := bestShiftQuality(decoded, ref, maxShift)
			bestSNR := SNRFromQuality(bestQ)
			baseSNR := SNRFromQuality(baseQ)
			scaledSNR := SNRFromQuality(scaledQ)
			affineSNR := SNRFromQuality(affineQ)

			t.Logf("base: Q=%.2f, SNR=%.2f dB", baseQ, baseSNR)
			t.Logf("scaled: scale=%.6f, Q=%.2f, SNR=%.2f dB", scale, scaledQ, scaledSNR)
			t.Logf("affine: scale=%.6f offset=%.3f, Q=%.2f, SNR=%.2f dB", scale, offset, affineQ, affineSNR)
			t.Logf("bestShift=%d samples (interleaved), Q=%.2f, SNR=%.2f dB", bestShift, bestQ, bestSNR)
		})
	}
}

func bestShiftQuality(decoded, reference []int16, maxShift int) (bestShift int, bestQ float64) {
	bestShift = 0
	bestQ = ComputeQuality(decoded, reference, 48000)
	for shift := -maxShift; shift <= maxShift; shift++ {
		d, r := alignForShift(decoded, reference, shift)
		q := ComputeQuality(d, r, 48000)
		if q > bestQ {
			bestQ = q
			bestShift = shift
		}
	}
	return bestShift, bestQ
}

// alignForShift aligns decoded and reference slices for a given shift.
// shift > 0: decoded is delayed vs reference (drop decoded[0:shift])
// shift < 0: decoded is advanced vs reference (drop reference[0:-shift])
func alignForShift(decoded, reference []int16, shift int) ([]int16, []int16) {
	d := decoded
	r := reference
	if shift > 0 {
		if shift < len(d) {
			d = d[shift:]
		} else {
			d = d[:0]
		}
	} else if shift < 0 {
		s := -shift
		if s < len(r) {
			r = r[s:]
		} else {
			r = r[:0]
		}
	}
	return d, r
}

// bestScale computes least-squares scale factor to align decoded to reference.
func bestScale(decoded, reference []int16) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return 1.0
	}
	var num, den float64
	for i := 0; i < n; i++ {
		d := float64(decoded[i])
		r := float64(reference[i])
		num += r * d
		den += d * d
	}
	if den == 0 {
		return 1.0
	}
	return num / den
}

// qualityWithScale computes Q after scaling decoded by scale (diagnostic only).
func qualityWithScale(decoded, reference []int16, scale float64) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return -1e9
	}
	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		r := float64(reference[i])
		d := float64(decoded[i]) * scale
		signalPower += r * r
		noise := d - r
		noisePower += noise * noise
	}
	signalPower /= float64(n)
	noisePower /= float64(n)
	if signalPower == 0 {
		return -1e9
	}
	if noisePower == 0 {
		return 100.0
	}
	snr := 10.0 * math.Log10(signalPower/noisePower)
	return QualityFromSNR(snr)
}

// bestOffset computes mean error after applying scale.
func bestOffset(decoded, reference []int16, scale float64) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += float64(reference[i]) - float64(decoded[i])*scale
	}
	return sum / float64(n)
}

// qualityWithScaleOffset computes Q after applying scale and offset to decoded.
func qualityWithScaleOffset(decoded, reference []int16, scale, offset float64) float64 {
	n := len(decoded)
	if len(reference) < n {
		n = len(reference)
	}
	if n == 0 {
		return -1e9
	}
	var signalPower, noisePower float64
	for i := 0; i < n; i++ {
		r := float64(reference[i])
		d := float64(decoded[i])*scale + offset
		signalPower += r * r
		noise := d - r
		noisePower += noise * noise
	}
	signalPower /= float64(n)
	noisePower /= float64(n)
	if signalPower == 0 {
		return -1e9
	}
	if noisePower == 0 {
		return 100.0
	}
	snr := 10.0 * math.Log10(signalPower/noisePower)
	return QualityFromSNR(snr)
}
