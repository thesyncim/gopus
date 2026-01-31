// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestReferenceFileComparison compares gopus, libopus, and reference .dec files.
// This diagnoses whether the compliance test failure is due to:
// 1. gopus vs libopus divergence (should be bit-exact per other tests)
// 2. libopus vs reference file divergence (indicates reference file issue)
func TestReferenceFileComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	decFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.dec"

	// Load packets
	packets, err := loadPacketsSimple(bitFile, -1) // Load all packets
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	// Load reference PCM
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skip("Could not load reference:", err)
	}

	t.Logf("Loaded %d packets, reference has %d samples", len(packets), len(reference))

	// Create decoders - always stereo to match opus_demo reference output
	goDec, err := gopus.NewDecoderDefault(48000, 2)
	if err != nil {
		t.Fatal(err)
	}

	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode all packets with both decoders
	var goSamples, libSamples []int16

	for pktIdx, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])

		// Decode with Go
		goOut, err := decodeInt16(goDec, pkt)
		if err != nil {
			t.Logf("Packet %d: Go decode error: %v", pktIdx, err)
			// Use zeros
			zeros := make([]int16, toc.FrameSize*2)
			goSamples = append(goSamples, zeros...)
		} else {
			goSamples = append(goSamples, goOut...)
		}

		// Decode with libopus (float32, then convert to int16).
		// Use 5760 as max samples per channel (120ms at 48kHz).
		libOutFloat, libN := libDec.DecodeFloat(pkt, 5760)
		if libN < 0 {
			t.Logf("Packet %d: libopus decode error", pktIdx)
			zeros := make([]int16, toc.FrameSize*2)
			libSamples = append(libSamples, zeros...)
		} else {
			total := libN * 2
			for i := 0; i < total; i++ {
				// Convert float32 [-1, 1] to int16
				v := libOutFloat[i] * 32768.0
				if v > 32767 {
					v = 32767
				} else if v < -32768 {
					v = -32768
				}
				libSamples = append(libSamples, int16(v))
			}
		}
	}

	t.Logf("Decoded: Go=%d samples, libopus=%d samples", len(goSamples), len(libSamples))

	t.Logf("Reference: %d samples", len(reference))

	// Compare all three
	minLen := len(goSamples)
	if len(libSamples) < minLen {
		minLen = len(libSamples)
	}
	if len(reference) < minLen {
		minLen = len(reference)
	}

	// Calculate SNR for each pair
	var goVsLibNoise, goVsRefNoise, libVsRefNoise float64
	var sigPower float64

	for i := 0; i < minLen; i++ {
		gv := float64(goSamples[i])
		lv := float64(libSamples[i])
		rv := float64(reference[i])

		sigPower += rv * rv

		goVsLibDiff := gv - lv
		goVsRefDiff := gv - rv
		libVsRefDiff := lv - rv

		goVsLibNoise += goVsLibDiff * goVsLibDiff
		goVsRefNoise += goVsRefDiff * goVsRefDiff
		libVsRefNoise += libVsRefDiff * libVsRefDiff
	}

	goVsLibSNR := 10 * math.Log10(sigPower/goVsLibNoise)
	goVsRefSNR := 10 * math.Log10(sigPower/goVsRefNoise)
	libVsRefSNR := 10 * math.Log10(sigPower/libVsRefNoise)

	if math.IsNaN(goVsLibSNR) || math.IsInf(goVsLibSNR, 1) {
		goVsLibSNR = 999.0
	}
	if math.IsNaN(goVsRefSNR) || math.IsInf(goVsRefSNR, 1) {
		goVsRefSNR = 999.0
	}
	if math.IsNaN(libVsRefSNR) || math.IsInf(libVsRefSNR, 1) {
		libVsRefSNR = 999.0
	}

	t.Log("\n=== SNR Comparison ===")
	t.Logf("gopus vs libopus: %.2f dB", goVsLibSNR)
	t.Logf("gopus vs reference: %.2f dB", goVsRefSNR)
	t.Logf("libopus vs reference: %.2f dB", libVsRefSNR)

	// Diagnosis
	t.Log("\n=== Diagnosis ===")
	if goVsLibSNR > 100 {
		t.Log("gopus matches libopus (bit-exact or near-exact)")
	} else {
		t.Logf("gopus diverges from libopus (SNR %.2f dB)", goVsLibSNR)
	}

	if libVsRefSNR > 100 {
		t.Log("libopus matches reference file (bit-exact or near-exact)")
	} else {
		t.Logf("libopus diverges from reference file (SNR %.2f dB) - reference file issue", libVsRefSNR)
	}

	if goVsRefSNR > 48 {
		t.Logf("gopus would PASS compliance test (SNR %.2f dB >= 48 dB)", goVsRefSNR)
	} else {
		t.Logf("gopus would FAIL compliance test (SNR %.2f dB < 48 dB)", goVsRefSNR)
	}

	// Show first few divergent samples
	t.Log("\n=== Sample Comparison (first 20) ===")
	for i := 0; i < 20 && i < minLen; i++ {
		t.Logf("Sample %3d: go=%7d, lib=%7d, ref=%7d, go-lib=%6d, go-ref=%6d, lib-ref=%6d",
			i, goSamples[i], libSamples[i], reference[i],
			int(goSamples[i])-int(libSamples[i]),
			int(goSamples[i])-int(reference[i]),
			int(libSamples[i])-int(reference[i]))
	}
}

// readPCMFile reads raw signed 16-bit little-endian PCM samples from a file.
func readPCMFile(filename string) ([]int16, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if len(data)%2 != 0 {
		return nil, err
	}

	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}

	return samples, nil
}

// TestLibopusVsReference compares libopus output directly to reference file.
// This isolates whether the reference file was generated with the same settings.
func TestLibopusVsReference(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	decFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.dec"

	// Load packets
	packets, err := loadPacketsSimple(bitFile, -1)
	if err != nil {
		t.Skip("Could not load packets:", err)
	}

	// Load reference
	reference, err := readPCMFile(decFile)
	if err != nil {
		t.Skip("Could not load reference:", err)
	}

	t.Logf("Loaded %d packets", len(packets))
	t.Logf("Reference: %d samples", len(reference))

	// Test with different decoder configurations
	configs := []struct {
		name     string
		rate     int
		channels int
	}{
		{"48kHz mono", 48000, 1},
		{"48kHz stereo", 48000, 2},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			libDec, _ := NewLibopusDecoder(cfg.rate, cfg.channels)
			if libDec == nil {
				t.Skip("Could not create libopus decoder")
			}
			defer libDec.Destroy()

			var libSamples []int16

			for _, pkt := range packets {
				// Use 5760 as max samples per channel (120ms at 48kHz).
				libOutFloat, libN := libDec.DecodeFloat(pkt, 5760)
				if libN > 0 {
					total := libN * cfg.channels
					for i := 0; i < total; i++ {
						v := libOutFloat[i] * 32768.0
						if v > 32767 {
							v = 32767
						} else if v < -32768 {
							v = -32768
						}
						libSamples = append(libSamples, int16(v))
					}
				}
			}

			t.Logf("libopus output: %d samples", len(libSamples))

			// Calculate SNR
			minLen := len(libSamples)
			if len(reference) < minLen {
				minLen = len(reference)
			}

			var noise, signal float64
			for i := 0; i < minLen; i++ {
				diff := float64(libSamples[i]) - float64(reference[i])
				noise += diff * diff
				signal += float64(reference[i]) * float64(reference[i])
			}

			snr := 10 * math.Log10(signal/noise)
			if math.IsNaN(snr) || math.IsInf(snr, 1) {
				snr = 999.0
			}

			t.Logf("libopus vs reference: SNR=%.2f dB", snr)

			if snr > 100 {
				t.Log("MATCH: libopus output matches reference file")
			} else {
				t.Logf("DIVERGE: libopus output differs from reference (SNR=%.2f)", snr)
			}
		})
	}
}
