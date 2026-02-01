//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestSilkVoicedEnergy checks if divergent packets have different energy characteristics.
func TestSilkVoicedEnergy(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	voicedPackets := []int{4, 5, 11, 12, 13, 14, 15, 19}
	divergentPackets := map[int]bool{4: true, 5: true, 15: true}

	t.Log("Energy analysis of voiced packets:")
	t.Log("Packet | Status    | RMS dB | Max Abs | Samples > 1000")

	for _, pktIdx := range voicedPackets {
		if pktIdx >= len(packets) {
			continue
		}
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		config := silk.GetBandwidthConfig(silkBW)

		// Decode with libopus (ground truth)
		libDec, _ := NewLibopusDecoder(config.SampleRate, 1)
		if libDec == nil {
			continue
		}
		libPcm, libSamples := libDec.DecodeFloat(pkt, 960)
		libDec.Destroy()
		if libSamples < 0 {
			continue
		}

		// Calculate energy metrics
		var sumSq float64
		maxAbs := float32(0)
		samplesOver1000 := 0
		for i := 0; i < libSamples; i++ {
			v := libPcm[i]
			sumSq += float64(v * v)
			absV := v
			if absV < 0 {
				absV = -absV
			}
			if absV > maxAbs {
				maxAbs = absV
			}
			// Check if sample > 1000/32768 ≈ 0.03
			if absV*32768 > 1000 {
				samplesOver1000++
			}
		}
		rms := math.Sqrt(sumSq / float64(libSamples))
		rmsDb := 20 * math.Log10(rms+1e-10)

		status := "bit-exact"
		if divergentPackets[pktIdx] {
			status = "DIVERGENT"
		}

		t.Logf("  %2d   | %-9s | %6.1f | %7.0f | %d",
			pktIdx, status, rmsDb, maxAbs*32768, samplesOver1000)

		// Also decode with gopus and compare energies
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		goDec := silk.NewDecoder()
		goNative, _ := goDec.DecodeFrame(&rd, silkBW, duration, true)

		var goSumSq float64
		for _, v := range goNative {
			goSumSq += float64(v * v)
		}
		goRms := math.Sqrt(goSumSq / float64(len(goNative)))
		goRmsDb := 20 * math.Log10(goRms+1e-10)

		t.Logf("        gopus RMS: %.1f dB, libopus RMS: %.1f dB, diff: %.2f dB",
			goRmsDb, rmsDb, goRmsDb-rmsDb)
	}
}
