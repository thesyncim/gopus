// Package cgo provides tests for SILK sample offset detection.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkTimingOffset checks if there's a timing offset between gopus and libopus
func TestSilkTimingOffset(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 1)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 1
	pkt := packets[0]

	// Decode with libopus
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Decode with gopus
	goDec, _ := gopus.NewDecoder(48000, channels)
	goPcm, decErr := goDec.DecodeFloat32(pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}

	n := minInt(len(goPcm), libSamples)
	t.Logf("Comparing %d samples", n)

	// Try different offsets to find best correlation
	bestOffset := 0
	bestSNR := -1000.0

	for offset := -20; offset <= 20; offset++ {
		var sigPow, noisePow float64
		count := 0

		for i := 0; i < n; i++ {
			goIdx := i
			libIdx := i + offset

			if goIdx < 0 || goIdx >= len(goPcm) || libIdx < 0 || libIdx >= libSamples {
				continue
			}

			sig := float64(libPcm[libIdx])
			noise := float64(goPcm[goIdx]) - sig
			sigPow += sig * sig
			noisePow += noise * noise
			count++
		}

		if count > 0 && sigPow > 0 {
			snr := 10 * math.Log10(sigPow/noisePow)
			if snr > bestSNR {
				bestSNR = snr
				bestOffset = offset
			}
		}
	}

	t.Logf("Best offset: %d samples, SNR: %.2f dB", bestOffset, bestSNR)

	// Show samples around the best offset
	t.Logf("\nSample comparison with offset %d:", bestOffset)
	t.Log("Index\tgopus\t\tlibopus[i+offset]\tdiff")
	for i := 0; i < minInt(30, n); i++ {
		libIdx := i + bestOffset
		if libIdx >= 0 && libIdx < libSamples {
			diff := goPcm[i] - libPcm[libIdx]
			t.Logf("%d\t%.6f\t%.6f\t\t%.6f", i, goPcm[i], libPcm[libIdx], diff)
		}
	}
}

// TestSilkCrossCorrelation finds the lag with maximum cross-correlation
func TestSilkCrossCorrelation(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 5)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	channels := 1

	// Decode multiple packets to get more signal
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	goDec, _ := gopus.NewDecoder(48000, channels)

	var goPcm, libPcmAll []float32

	for _, pkt := range packets {
		goSamples, decErr := goDec.DecodeFloat32(pkt)
		if decErr != nil {
			continue
		}
		goPcm = append(goPcm, goSamples...)

		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
		if libSamples > 0 {
			libPcmAll = append(libPcmAll, libPcm[:libSamples]...)
		}
	}

	n := minInt(len(goPcm), len(libPcmAll))
	t.Logf("Total samples: gopus=%d, libopus=%d", len(goPcm), len(libPcmAll))

	// Compute cross-correlation at different lags
	maxLag := 50
	bestLag := 0
	bestCorr := -math.MaxFloat64

	for lag := -maxLag; lag <= maxLag; lag++ {
		var sum, normGo, normLib float64
		count := 0

		for i := 0; i < n; i++ {
			goIdx := i
			libIdx := i + lag

			if goIdx < 0 || goIdx >= len(goPcm) || libIdx < 0 || libIdx >= len(libPcmAll) {
				continue
			}

			goVal := float64(goPcm[goIdx])
			libVal := float64(libPcmAll[libIdx])

			sum += goVal * libVal
			normGo += goVal * goVal
			normLib += libVal * libVal
			count++
		}

		if count > 0 && normGo > 0 && normLib > 0 {
			corr := sum / math.Sqrt(normGo*normLib)
			if corr > bestCorr {
				bestCorr = corr
				bestLag = lag
			}
		}
	}

	t.Logf("Best lag: %d samples, correlation: %.6f", bestLag, bestCorr)

	// Calculate SNR at best lag
	var sigPow, noisePow float64
	for i := 0; i < n; i++ {
		goIdx := i
		libIdx := i + bestLag

		if goIdx < 0 || goIdx >= len(goPcm) || libIdx < 0 || libIdx >= len(libPcmAll) {
			continue
		}

		sig := float64(libPcmAll[libIdx])
		noise := float64(goPcm[goIdx]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}

	snr := 10 * math.Log10(sigPow/noisePow)
	t.Logf("SNR at lag %d: %.2f dB", bestLag, snr)
}
