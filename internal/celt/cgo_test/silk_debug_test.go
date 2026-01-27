package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func TestSilkNativeAfterPacket0(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 2 {
		t.Skip("Could not load enough packets")
	}

	// First decode packet 0 with both decoders to initialize state
	pkt0 := packets[0]
	toc0 := gopus.ParseTOC(pkt0[0])

	// gopus - decode packet 0
	goDec := silk.NewDecoder()
	silkBW0, _ := silk.BandwidthFromOpus(int(toc0.Bandwidth))
	duration0 := silk.FrameDurationFromTOC(toc0.FrameSize)
	var rd0 rangecoding.Decoder
	rd0.Init(pkt0[1:])
	goNative0, _ := goDec.DecodeFrame(&rd0, silkBW0, duration0, true)
	t.Logf("Packet 0: gopus native samples: %d", len(goNative0))

	// libopus - decode packet 0 at 8k
	libDec, err := NewLibopusDecoder(8000, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder at 8k")
	}
	defer libDec.Destroy()
	libPcm0, libSamples0 := libDec.DecodeFloat(pkt0, 960)
	t.Logf("Packet 0: libopus samples: %d", libSamples0)

	// Compare packet 0 native output
	snr0 := calcSNR(goNative0, libPcm0[:libSamples0])
	t.Logf("Packet 0 native SNR: %.1f dB", snr0)

	// Now decode packet 1 with SAME decoders (state carried over)
	pkt1 := packets[1]
	toc1 := gopus.ParseTOC(pkt1[0])

	silkBW1, _ := silk.BandwidthFromOpus(int(toc1.Bandwidth))
	duration1 := silk.FrameDurationFromTOC(toc1.FrameSize)
	var rd1 rangecoding.Decoder
	rd1.Init(pkt1[1:])
	goNative1, err := goDec.DecodeFrame(&rd1, silkBW1, duration1, true)
	if err != nil {
		t.Fatalf("gopus native decode packet 1 failed: %v", err)
	}

	libPcm1, libSamples1 := libDec.DecodeFloat(pkt1, 960)
	if libSamples1 < 0 {
		t.Fatalf("libopus decode packet 1 failed: %d", libSamples1)
	}

	t.Logf("Packet 1: gopus native samples: %d, libopus: %d", len(goNative1), libSamples1)

	// Find first difference
	minN := len(goNative1)
	if libSamples1 < minN {
		minN = libSamples1
	}
	firstDiff := -1
	var sigPow, noisePow float64
	for i := 0; i < minN; i++ {
		sig := float64(libPcm1[i])
		noise := float64(goNative1[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
		if firstDiff < 0 && math.Abs(noise) > 1e-5 {
			firstDiff = i
		}
	}
	snr1 := 10 * math.Log10(sigPow/noisePow)
	if math.IsNaN(snr1) || math.IsInf(snr1, 1) {
		snr1 = 999
	}

	t.Logf("Packet 1 native SNR: %.1f dB, first diff at sample %d", snr1, firstDiff)

	// Show samples around first difference
	if firstDiff >= 0 {
		start := firstDiff - 5
		if start < 0 {
			start = 0
		}
		end := firstDiff + 20
		if end > minN {
			end = minN
		}
		for i := start; i < end; i++ {
			marker := ""
			if math.Abs(float64(goNative1[i]-libPcm1[i])) > 1e-4 {
				marker = " <-- DIFF"
			}
			t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f%s", i, goNative1[i], libPcm1[i], goNative1[i]-libPcm1[i], marker)
		}
	}

	// Also show SNR per 20ms frame (160 samples at 8kHz)
	t.Log("\nSNR per native 20ms frame:")
	for frame := 0; frame*160 < minN; frame++ {
		start := frame * 160
		end := start + 160
		if end > minN {
			end = minN
		}
		var fSig, fNoise float64
		for i := start; i < end; i++ {
			sig := float64(libPcm1[i])
			noise := float64(goNative1[i]) - sig
			fSig += sig * sig
			fNoise += noise * noise
		}
		fSnr := 10 * math.Log10(fSig/fNoise)
		if math.IsNaN(fSnr) || math.IsInf(fSnr, 1) {
			fSnr = 999
		}
		t.Logf("  Frame %d [%d-%d]: SNR=%.1f dB", frame, start, end-1, fSnr)
	}
}

func calcSNR(go_pcm []float32, lib_pcm []float32) float64 {
	n := len(go_pcm)
	if len(lib_pcm) < n {
		n = len(lib_pcm)
	}
	var sigPow, noisePow float64
	for i := 0; i < n; i++ {
		sig := float64(lib_pcm[i])
		noise := float64(go_pcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		return 999
	}
	return snr
}
