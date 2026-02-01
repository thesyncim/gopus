//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestEnergyCompareDetailed(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Detailed Energy Comparison ===")

	// GOPUS encoding
	celtEnc := celt.NewEncoder(1)
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Pre-emphasis
	preemph := celtEnc.ApplyPreemphasis(pcm)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)

	// Band energies
	gopusEnergies := celtEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("Gopus band energies (dB):")
	for i := 0; i < nbBands && i < 21; i++ {
		t.Logf("  Band %2d: %8.4f dB", i, gopusEnergies[i])
	}

	// LIBOPUS - encode and decode energies from packet
	t.Log("")
	t.Log("Now comparing with libopus...")

	libEnc, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Check what TOC libopus uses
	toc := libPacket[0]
	config := (toc >> 3) & 0x1F
	t.Logf("Libopus TOC: 0x%02X (config=%d)", toc, config)

	// Energy comparison: decode both and compare levels
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	libDecoded, _ := libDec1.DecodeFloat(libPacket, frameSize)

	// Gopus packet
	gopusPacket, _ := celtEnc.EncodeFrame(pcm, frameSize)
	tocGopus := byte(0xF8) // CELT FB 20ms
	gopusWithTOC := append([]byte{tocGopus}, gopusPacket...)
	gopusDecoded, _ := libDec2.DecodeFloat(gopusWithTOC, frameSize)

	// Compute band-by-band energy from decoded signals
	t.Log("")
	t.Log("Comparing decoded signal band energies:")
	t.Log("(Computed from decoded signals using same band structure)")

	for band := 0; band < 10 && band < nbBands; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)

		var gopusBandPower, libBandPower float64
		for k := start; k < end; k++ {
			if k < frameSize {
				gopusBandPower += float64(gopusDecoded[k] * gopusDecoded[k])
				libBandPower += float64(libDecoded[k] * libDecoded[k])
			}
		}

		gopusBandDB := 10 * math.Log10(gopusBandPower+1e-10)
		libBandDB := 10 * math.Log10(libBandPower+1e-10)
		diff := gopusBandDB - libBandDB

		t.Logf("  Band %2d: gopus=%8.2f dB, libopus=%8.2f dB, diff=%+6.2f dB",
			band, gopusBandDB, libBandDB, diff)
	}

	// Compare first 10 bytes of payload
	t.Log("")
	t.Log("Payload comparison:")
	gopusPayload := gopusPacket
	libPayload := libPacket[1:] // Skip TOC

	maxBytes := 15
	if len(gopusPayload) < maxBytes {
		maxBytes = len(gopusPayload)
	}
	if len(libPayload) < maxBytes {
		maxBytes = len(libPayload)
	}

	t.Log("       gopus    libopus")
	for i := 0; i < maxBytes; i++ {
		match := ""
		if gopusPayload[i] == libPayload[i] {
			match = " <match>"
		}
		t.Logf("  %2d:  0x%02X     0x%02X%s", i, gopusPayload[i], libPayload[i], match)
	}
}
