// transient_debug_test.go - Debug transient short block synthesis
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTransientSynthesisDebug traces through packet 61 (first transient) decoding
func TestTransientSynthesisDebug(t *testing.T) {
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

	channels := 2

	// Create decoders
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	// Decode up to packet 60 (non-transient) to get state aligned
	t.Log("Decoding packets 0-60 (non-transient)...")
	for i := 0; i <= 60; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 61 (first transient)
	pkt61 := packets[61]
	t.Log("\n=== Decoding packet 61 (first transient) ===")
	t.Logf("Packet length: %d bytes", len(pkt61))

	goPcm61, goErr := goDec.DecodeFloat32(pkt61)
	if goErr != nil {
		t.Fatalf("gopus error: %v", goErr)
	}
	libPcm61, libSamples := libDec.DecodeFloat(pkt61, 5760)
	if libSamples < 0 {
		t.Fatalf("libopus error: %d", libSamples)
	}

	t.Logf("gopus samples: %d, libopus samples: %d", len(goPcm61)/channels, libSamples)

	// Compare at short block boundaries (every 120 samples for 8 blocks)
	t.Log("\nSample comparison at short block boundaries:")
	for block := 0; block < 8; block++ {
		startSample := block * 120
		endSample := startSample + 4 // First few samples of each block

		t.Logf("\n--- Short block %d (samples %d-%d) ---", block, startSample, startSample+119)
		for pos := startSample; pos < endSample && pos < 960; pos++ {
			idx := pos * 2 // stereo interleaved
			if idx+1 < len(goPcm61) && idx+1 < len(libPcm61)*2 {
				goL := goPcm61[idx]
				libL := libPcm61[idx]
				diff := goL - libL
				marker := ""
				if math.Abs(float64(diff)) > 0.0001 {
					marker = " ***"
				}
				t.Logf("  [%3d] goL=%.8f libL=%.8f diff=%.8f%s", pos, goL, libL, diff, marker)
			}
		}
	}

	// Calculate SNR per short block
	t.Log("\nSNR per short block:")
	for block := 0; block < 8; block++ {
		startSample := block * 120
		endSample := startSample + 120

		var sig, noise float64
		for pos := startSample; pos < endSample && pos < 960; pos++ {
			idx := pos * 2
			if idx+1 < len(goPcm61) && idx+1 < len(libPcm61)*2 {
				for ch := 0; ch < 2; ch++ {
					s := float64(libPcm61[idx+ch])
					n := float64(goPcm61[idx+ch]) - s
					sig += s * s
					noise += n * n
				}
			}
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}
		t.Logf("  Block %d: SNR=%.1f dB", block, snr)
	}
}

// TestDecodePacket61WithFreshDecoder decodes packet 61 with a fresh decoder
// to eliminate accumulated state issues
func TestDecodePacket61WithFreshDecoder(t *testing.T) {
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

	channels := 2
	pkt61 := packets[61]

	t.Log("=== Decode packet 61 with FRESH decoder ===")

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	goPcm, _ := goDec.DecodeFloat32(pkt61)
	libPcm, libSamples := libDec.DecodeFloat(pkt61, 5760)

	t.Logf("gopus samples: %d, libopus samples: %d", len(goPcm)/channels, libSamples)

	// SNR per short block
	t.Log("\nSNR per short block (fresh decoder):")
	for block := 0; block < 8; block++ {
		startSample := block * 120
		endSample := startSample + 120

		var sig, noise float64
		for pos := startSample; pos < endSample && pos < 960; pos++ {
			idx := pos * 2
			if idx+1 < len(goPcm) && idx+1 < len(libPcm)*2 {
				for ch := 0; ch < 2; ch++ {
					s := float64(libPcm[idx+ch])
					n := float64(goPcm[idx+ch]) - s
					sig += s * s
					noise += n * n
				}
			}
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}
		t.Logf("  Block %d: SNR=%.1f dB", block, snr)
	}

	// Overall SNR
	var totalSig, totalNoise float64
	n := minInt(len(goPcm), libSamples*channels)
	for i := 0; i < n; i++ {
		s := float64(libPcm[i])
		d := float64(goPcm[i]) - s
		totalSig += s * s
		totalNoise += d * d
	}
	overallSNR := 10 * math.Log10(totalSig/totalNoise)
	t.Logf("\nOverall SNR: %.1f dB", overallSNR)
}
