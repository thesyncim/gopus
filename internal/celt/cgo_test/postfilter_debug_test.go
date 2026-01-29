// Package cgo debugs postfilter behavior for packet 1000
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestPostfilterHistoryCompare compares postfilter history state
func TestPostfilterHistoryCompare(t *testing.T) {
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

	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	// Track postfilter parameters
	t.Log("Postfilter state evolution (packets 995-1005):")
	t.Log("")

	// Sync to packet 994
	for i := 0; i < 995; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	for i := 995; i < 1005 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Get postfilter state BEFORE decode
		goPfPeriod := goDec.GetCELTDecoder().PostfilterPeriod()
		goPfGain := goDec.GetCELTDecoder().PostfilterGain()
		goPfTapset := goDec.GetCELTDecoder().PostfilterTapset()

		goPcm, _ := goDec.DecodeFloat32(pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		// Get postfilter state AFTER decode
		goPfPeriodAfter := goDec.GetCELTDecoder().PostfilterPeriod()
		goPfGainAfter := goDec.GetCELTDecoder().PostfilterGain()
		goPfTapsetAfter := goDec.GetCELTDecoder().PostfilterTapset()

		n := minInt(len(goPcm), libN)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		t.Logf("Pkt %d (fs=%d): SNR=%.1f dB", i, toc.FrameSize, snr)
		t.Logf("  Before: period=%d gain=%.4f tapset=%d", goPfPeriod, goPfGain, goPfTapset)
		t.Logf("  After:  period=%d gain=%.4f tapset=%d", goPfPeriodAfter, goPfGainAfter, goPfTapsetAfter)
	}
}

// TestSkipPostfilterPackets tests if problem persists without postfilter packets
func TestSkipPostfilterPackets(t *testing.T) {
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

	// Count how many packets have postfilter enabled around packet 1000
	t.Log("Checking postfilter enabled status for packets 990-1010:")

	for i := 990; i < 1010 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) <= 1 {
			continue
		}

		// Parse postfilter flag
		// First byte is TOC, CELT data starts at byte 1
		celtData := pkt[1:]
		if len(celtData) < 2 {
			continue
		}

		// Range coding: need at least 16 bits to have postfilter
		totalBits := len(celtData) * 8
		if totalBits < 16 {
			t.Logf("  Pkt %d: too short for postfilter", i)
			continue
		}

		// The postfilter flag is at bit 2 (after silence bit)
		// This is a simplified check - actual decoding is more complex
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("  Pkt %d (fs=%d): CELT data len=%d bits", i, toc.FrameSize, totalBits)
	}
}

// TestFreshVsSyncedPostfilter tests postfilter behavior with fresh vs synced state
func TestFreshVsSyncedPostfilter(t *testing.T) {
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

	pkt := packets[1000]

	// Fresh decoder
	t.Log("Test: Fresh decoder for packet 1000")
	goDec1, _ := gopus.NewDecoder(48000, 1)
	libDec1, _ := NewLibopusDecoder(48000, 1)

	t.Logf("  Initial postfilter: period=%d gain=%.4f tapset=%d",
		goDec1.GetCELTDecoder().PostfilterPeriod(),
		goDec1.GetCELTDecoder().PostfilterGain(),
		goDec1.GetCELTDecoder().PostfilterTapset())

	goPcm1, _ := goDec1.DecodeFloat32(pkt)
	libPcm1, libN1 := libDec1.DecodeFloat(pkt, 5760)
	libDec1.Destroy()

	n1 := minInt(len(goPcm1), libN1)
	var sig1, noise1 float64
	for j := 0; j < n1; j++ {
		s := float64(libPcm1[j])
		d := float64(goPcm1[j]) - s
		sig1 += s * s
		noise1 += d * d
	}
	snr1 := 10 * math.Log10(sig1/noise1)
	t.Logf("  SNR: %.1f dB", snr1)

	t.Logf("  After decode postfilter: period=%d gain=%.4f tapset=%d",
		goDec1.GetCELTDecoder().PostfilterPeriod(),
		goDec1.GetCELTDecoder().PostfilterGain(),
		goDec1.GetCELTDecoder().PostfilterTapset())

	// Synced decoder
	t.Log("\nTest: Synced decoder for packet 1000 (after 999 packets)")
	goDec2, _ := gopus.NewDecoder(48000, 1)
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	for i := 0; i < 1000; i++ {
		goDec2.DecodeFloat32(packets[i])
		libDec2.DecodeFloat(packets[i], 5760)
	}

	t.Logf("  Before decode postfilter: period=%d gain=%.4f tapset=%d",
		goDec2.GetCELTDecoder().PostfilterPeriod(),
		goDec2.GetCELTDecoder().PostfilterGain(),
		goDec2.GetCELTDecoder().PostfilterTapset())

	goPcm2, _ := goDec2.DecodeFloat32(pkt)
	libPcm2, libN2 := libDec2.DecodeFloat(pkt, 5760)

	n2 := minInt(len(goPcm2), libN2)
	var sig2, noise2 float64
	for j := 0; j < n2; j++ {
		s := float64(libPcm2[j])
		d := float64(goPcm2[j]) - s
		sig2 += s * s
		noise2 += d * d
	}
	snr2 := 10 * math.Log10(sig2/noise2)
	t.Logf("  SNR: %.1f dB", snr2)

	t.Logf("  After decode postfilter: period=%d gain=%.4f tapset=%d",
		goDec2.GetCELTDecoder().PostfilterPeriod(),
		goDec2.GetCELTDecoder().PostfilterGain(),
		goDec2.GetCELTDecoder().PostfilterTapset())
}
