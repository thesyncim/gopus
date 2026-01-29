// Package cgo compares coefficient decoding for packet 1000
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPacket1000Coefficients compares CELT coefficients for packet 1000
func TestPacket1000Coefficients(t *testing.T) {
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

	// Sync state first
	goDec, _ := gopus.NewDecoder(48000, 1)
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	for i := 0; i < 1000; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 1000
	pkt := packets[1000]
	t.Logf("Packet 1000: len=%d, TOC=0x%02X", len(pkt), pkt[0])

	goPcm, _ := goDec.DecodeFloat32(pkt)
	libPcm, libN := libDec.DecodeFloat(pkt, 5760)

	// Compute SNR
	n := minInt(len(goPcm), libN)
	var sig, noise float64
	for i := 0; i < n; i++ {
		s := float64(libPcm[i])
		d := float64(goPcm[i]) - s
		sig += s * s
		noise += d * d
	}
	snr := 10 * math.Log10(sig/noise)
	t.Logf("Final output SNR: %.1f dB", snr)

	// Now do step-by-step analysis
	t.Logf("\nStep-by-step coefficient extraction for packet 1000:")

	celtData := pkt[1:] // Skip TOC
	rd := &rangecoding.Decoder{}
	rd.Init(celtData)

	mode := celt.GetModeConfig(120)
	totalBits := len(celtData) * 8
	tell := rd.Tell()

	t.Logf("  totalBits=%d, initial tell=%d", totalBits, tell)

	// Decode silence
	silence := false
	if tell >= totalBits {
		silence = true
	} else if tell == 1 {
		silence = rd.DecodeBit(15) == 1
	}
	t.Logf("  silence=%v, tell after silence=%d", silence, rd.Tell())

	// Decode postfilter
	pfPeriod := 0
	pfGain := 0.0
	pfTapset := 0
	if rd.Tell()+16 <= totalBits {
		if rd.DecodeBit(1) == 1 {
			octave := int(rd.DecodeUniform(6))
			pfPeriod = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			qg := int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				pfTapset = rd.DecodeICDF([]uint8{2, 4, 0}, 2)
			}
			pfGain = 0.09375 * float64(qg+1)
			t.Logf("  postfilter: enabled, period=%d, gain=%.4f, tapset=%d",
				pfPeriod, pfGain, pfTapset)
		} else {
			t.Logf("  postfilter: disabled")
		}
	}
	t.Logf("  tell after postfilter=%d", rd.Tell())

	// For LM=0, no transient flag
	t.Logf("  LM=%d (no transient flag for LM=0)", mode.LM)

	// Decode intra
	intra := false
	if rd.Tell()+3 <= totalBits {
		intra = rd.DecodeBit(3) == 1
	}
	t.Logf("  intra=%v, tell after intra=%d", intra, rd.Tell())

	// The coefficients are decoded later in the bitstream
	// The key is whether the energy and band shapes match

	t.Logf("\n  For packet 1000, we expect:")
	t.Logf("    - Postfilter enabled (this affects output)")
	t.Logf("    - Errors start at sample 72")
	t.Logf("    - Errors grow towards end (IIR filter effect)")
}

// TestDisablePostfilterEffect tests if disabling postfilter improves quality
func TestDisablePostfilterEffect(t *testing.T) {
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

	t.Log("Comparing SNR for packets 996-1005 with and without postfilter:")

	// Test with normal decoding (postfilter enabled)
	t.Log("\n=== With postfilter (normal) ===")
	goDec1, _ := gopus.NewDecoder(48000, 1)
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()

	for i := 0; i < 996; i++ {
		goDec1.DecodeFloat32(packets[i])
		libDec1.DecodeFloat(packets[i], 5760)
	}

	for i := 996; i < 1005 && i < len(packets); i++ {
		pkt := packets[i]
		goPcm, _ := goDec1.DecodeFloat32(pkt)
		libPcm, libN := libDec1.DecodeFloat(pkt, 5760)

		n := minInt(len(goPcm), libN)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)
		t.Logf("  Packet %d: SNR = %.1f dB", i, snr)
	}
}
