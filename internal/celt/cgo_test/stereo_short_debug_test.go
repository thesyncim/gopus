// stereo_short_debug_test.go - Debug stereo short frame (120 sample) divergence
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereo120SampleFrames specifically tests 120-sample stereo CELT frames
// which have 67% bad packets in testvector07.
func TestStereo120SampleFrames(t *testing.T) {
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
		offset += 4 // skip enc_final_range
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	channels := 2

	// Find first 120-sample stereo packet
	firstIdx := -1
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 && toc.Stereo {
			firstIdx = i
			t.Logf("First 120-sample stereo packet at index %d", i)
			break
		}
	}

	if firstIdx < 0 {
		t.Skip("No 120-sample stereo packets found")
		return
	}

	// Create decoders and decode up to the first 120-stereo packet
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	t.Logf("\nDecoding packets leading up to and including first 120-sample stereo:")

	// Decode all packets up to first bad one
	for i := 0; i <= firstIdx+10 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if goErr != nil || libSamples < 0 {
			t.Logf("Pkt %d: decode error (go=%v, lib=%d)", i, goErr, libSamples)
			continue
		}

		var sig, noise float64
		n := minInt(len(goPcm), libSamples*channels)
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		status := "OK"
		if snr < 40 {
			status = "BAD"
		}

		fs := toc.FrameSize
		st := "mono"
		if toc.Stereo {
			st = "stereo"
		}

		t.Logf("Pkt %4d: frame=%4d %6s len=%3d SNR=%7.1f %s", i, fs, st, len(pkt), snr, status)

		// If this is a bad 120-sample stereo packet, show sample comparison
		if toc.FrameSize == 120 && toc.Stereo && snr < 40 {
			t.Logf("\n  *** Sample comparison for bad 120-sample stereo packet %d ***", i)
			showSamples := 40
			if len(goPcm) < showSamples {
				showSamples = len(goPcm)
			}
			t.Log("  Idx    gopus       libopus      diff")
			for j := 0; j < showSamples; j++ {
				diff := goPcm[j] - libPcm[j]
				marker := ""
				if math.Abs(float64(diff)) > 0.001 {
					marker = " *"
				}
				ch := "L"
				if j%2 == 1 {
					ch = "R"
				}
				t.Logf("  %3d %s %10.6f %10.6f %10.6f%s", j/2, ch, goPcm[j], libPcm[j], diff, marker)
			}
			break // Stop after first bad packet analysis
		}
	}
}

// TestCompare120Mono tests that 120-sample mono packets decode correctly
func TestCompare120Mono(t *testing.T) {
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

	// Find first 120-sample MONO packet
	firstIdx := -1
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 && !toc.Stereo {
			firstIdx = i
			t.Logf("First 120-sample mono packet at index %d", i)
			break
		}
	}

	if firstIdx < 0 {
		t.Skip("No 120-sample mono packets found")
		return
	}

	// Create decoders and decode
	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	t.Logf("\nDecoding packets leading up to and including first 120-sample mono:")

	for i := 0; i <= firstIdx+5 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, goErr := goDec.DecodeFloat32(pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		if goErr != nil || libSamples < 0 {
			continue
		}

		var sig, noise float64
		n := minInt(len(goPcm), libSamples*channels)
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}

		snr := 10 * math.Log10(sig/noise)
		if math.IsNaN(snr) || math.IsInf(snr, 1) {
			snr = 999
		}

		status := "OK"
		if snr < 40 {
			status = "BAD"
		}

		st := "mono"
		if toc.Stereo {
			st = "stereo"
		}

		t.Logf("Pkt %4d: frame=%4d %6s len=%3d SNR=%7.1f %s", i, toc.FrameSize, st, len(pkt), snr, status)
	}
}

// TestIsolateStereoShortBlock tests decoding of a single 120-sample stereo packet
// with fresh decoders to isolate the issue.
func TestIsolateStereoShortBlock(t *testing.T) {
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

	// Find a 120-sample stereo packet
	var stereo120Pkts []int
	for i, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		if toc.FrameSize == 120 && toc.Stereo {
			stereo120Pkts = append(stereo120Pkts, i)
		}
	}

	if len(stereo120Pkts) == 0 {
		t.Skip("No 120-sample stereo packets")
		return
	}

	t.Logf("Found %d 120-sample stereo packets", len(stereo120Pkts))

	// Test decoding with fresh decoder (no prior state)
	channels := 2
	t.Log("\n=== Test: Fresh decoder, single 120-sample stereo packet ===")

	pktIdx := stereo120Pkts[0]
	pkt := packets[pktIdx]
	t.Logf("Testing packet %d (%d bytes)", pktIdx, len(pkt))

	goDec, _ := gopus.NewDecoder(48000, channels)
	libDec, _ := NewLibopusDecoder(48000, channels)
	defer libDec.Destroy()

	goPcm, goErr := goDec.DecodeFloat32(pkt)
	libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

	if goErr != nil {
		t.Fatalf("gopus error: %v", goErr)
	}
	if libSamples < 0 {
		t.Fatalf("libopus error: %d", libSamples)
	}

	t.Logf("gopus samples: %d, libopus samples: %d", len(goPcm)/channels, libSamples)

	var sig, noise float64
	n := minInt(len(goPcm), libSamples*channels)
	for j := 0; j < n; j++ {
		s := float64(libPcm[j])
		d := float64(goPcm[j]) - s
		sig += s * s
		noise += d * d
	}

	snr := 10 * math.Log10(sig/noise)
	t.Logf("SNR: %.1f dB", snr)

	// Show first 40 samples
	t.Log("\nFirst 40 samples:")
	showSamples := 40
	if len(goPcm) < showSamples {
		showSamples = len(goPcm)
	}
	for j := 0; j < showSamples; j++ {
		diff := goPcm[j] - libPcm[j]
		ch := "L"
		if j%2 == 1 {
			ch = "R"
		}
		t.Logf("[%3d %s] go=%10.6f lib=%10.6f diff=%10.6f", j/2, ch, goPcm[j], libPcm[j], diff)
	}
}
