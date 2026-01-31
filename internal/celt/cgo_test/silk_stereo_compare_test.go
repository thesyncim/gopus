package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestSilkStereoFirstPacket compares gopus vs libopus for the first stereo SILK packet.
// This is a focused diagnostic to validate stereo decoding.
func TestSilkStereoFirstPacket(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets := loadPackets(t, bitFile, 0)
	if len(packets) == 0 {
		t.Skip("Could not load test packets")
	}

	// Find first stereo packet
	var pkt []byte
	for _, p := range packets {
		if len(p) == 0 {
			continue
		}
		if gopus.ParseTOC(p[0]).Stereo {
			pkt = p
			break
		}
	}
	if pkt == nil {
		t.Skip("No stereo packets in test vector")
	}

	channels := 2
	goDec, err := gopus.NewDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create gopus decoder: %v", err)
	}
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder")
	}
	defer libDec.Destroy()

	goPcm, decErr := goDec.DecodeFloat32(pkt)
	if decErr != nil {
		t.Fatalf("gopus decode failed: %v", decErr)
	}
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	if len(goPcm) != libSamples*channels {
		t.Fatalf("sample count mismatch gopus=%d libopus=%d", len(goPcm)/channels, libSamples)
	}

	var sigPow, noisePow float64
	for i := 0; i < libSamples*channels; i++ {
		sig := float64(libPcm[i])
		noise := float64(goPcm[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
	}

	snr := 10 * math.Log10(sigPow/noisePow)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999
	}
	t.Logf("Stereo packet SNR (gopus vs libopus): %.2f dB", snr)

	// Compare libopus mono output to different downmixes of the stereo output.
	libDecMono, err := NewLibopusDecoder(48000, 1)
	if err != nil || libDecMono == nil {
		t.Fatalf("Failed to create libopus mono decoder")
	}
	defer libDecMono.Destroy()
	libMono, monoSamples := libDecMono.DecodeFloat(pkt, 5760)
	if monoSamples < 0 {
		t.Fatalf("libopus mono decode failed: %d", monoSamples)
	}
	if monoSamples != libSamples {
		t.Fatalf("mono/stereo sample mismatch: mono=%d stereo=%d", monoSamples, libSamples)
	}

	best := ""
	bestSNR := -1e9
	check := func(name string, get func(i int) float64) {
		var sigP, noiseP float64
		for i := 0; i < monoSamples; i++ {
			sig := float64(libMono[i])
			noise := get(i) - sig
			sigP += sig * sig
			noiseP += noise * noise
		}
		s := 10 * math.Log10(sigP/noiseP)
		if math.IsNaN(s) || math.IsInf(s, 1) {
			s = 999
		}
		t.Logf("Mono mapping SNR (%s vs libopus mono): %.2f dB", name, s)
		if s > bestSNR {
			bestSNR = s
			best = name
		}
	}

	check("left", func(i int) float64 { return float64(goPcm[i*2]) })
	check("right", func(i int) float64 { return float64(goPcm[i*2+1]) })
	check("mid_avg", func(i int) float64 { return 0.5 * (float64(goPcm[i*2]) + float64(goPcm[i*2+1])) })

	t.Logf("Best mono mapping: %s (SNR %.2f dB)", best, bestSNR)
}
