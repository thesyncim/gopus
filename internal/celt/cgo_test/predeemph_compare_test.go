// Package cgo compares pre-deemphasis samples between gopus and libopus
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestPreDeemphSamplesCompare tests if the issue is in synthesis or deemphasis
func TestPreDeemphSamplesCompare(t *testing.T) {
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

	// Create decoders
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	// Decode packets 996-1005 and analyze the error pattern
	t.Log("Analyzing error pattern to determine if issue is before or after de-emphasis:")
	t.Log("")

	// First sync up to packet 995
	for i := 0; i < 996; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	for i := 996; i < 1005 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		if libN <= 0 {
			continue
		}

		// Compute error at different positions in the frame
		n := minInt(len(goPcm), libN)

		// Error at start (samples 0-9)
		var errStart float64
		for j := 0; j < 10 && j < n; j++ {
			d := float64(goPcm[j]) - float64(libPcm[j])
			errStart += d * d
		}

		// Error at middle (samples 50-59)
		var errMid float64
		for j := 50; j < 60 && j < n; j++ {
			d := float64(goPcm[j]) - float64(libPcm[j])
			errMid += d * d
		}

		// Error at end (last 10 samples)
		var errEnd float64
		for j := n - 10; j < n; j++ {
			d := float64(goPcm[j]) - float64(libPcm[j])
			errEnd += d * d
		}

		// Total error
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		// Get deemphasis state error
		libMem, _ := libDec.GetPreemphState()
		goState := goDec.GetCELTDecoder().PreemphState()
		stateErr := math.Abs(goState[0] - float64(libMem))

		t.Logf("Pkt %d (fs=%d): SNR=%.1f dB, errStart=%.2e, errMid=%.2e, errEnd=%.2e, stateErr=%.2e",
			i, toc.FrameSize, snr, errStart, errMid, errEnd, stateErr)

		// If error grows towards end, it's IIR filter issue
		// If error is uniform, it's synthesis issue
		if errEnd > errStart*10 {
			t.Logf("  -> Error pattern: grows towards end (IIR/de-emphasis issue)")
		} else if errStart > errEnd*10 {
			t.Logf("  -> Error pattern: larger at start (overlap/synthesis issue)")
		}
	}
}

// TestFreshDecodeEachPacket tests if decoding single packets with fresh state works
func TestFreshDecodeEachPacket(t *testing.T) {
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

	t.Log("Testing packets 996-1005 decoded with FRESH decoders (no history):")

	for i := 996; i < 1005 && i < len(packets); i++ {
		// Create fresh decoders for each packet
		goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
		libDec, _ := NewLibopusDecoder(48000, 1)

		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		libDec.Destroy()

		if libN <= 0 {
			continue
		}

		n := minInt(len(goPcm), libN)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		t.Logf("Pkt %d (fs=%d): SNR=%.1f dB (fresh decode)", i, toc.FrameSize, snr)
	}
}

// TestIsolateFrameSizeTransition tests the 240->120 frame size transition
func TestIsolateFrameSizeTransition(t *testing.T) {
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

	t.Log("Testing 240->120 frame size transition (packets 995-1005):")
	t.Log("")

	// Sync decoders to packet 994
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	for i := 0; i < 995; i++ {
		decodeFloat32(goDec, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode 995-1005 with detailed analysis
	for i := 995; i < 1006 && i < len(packets); i++ {
		// Get state before decode
		libMemBefore, _ := libDec.GetPreemphState()
		goStateBefore := append([]float64(nil), goDec.GetCELTDecoder().PreemphState()...)
		goOverlapBefore := append([]float64(nil), goDec.GetCELTDecoder().OverlapBuffer()...)

		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		// Get state after decode
		libMemAfter, _ := libDec.GetPreemphState()
		goStateAfter := goDec.GetCELTDecoder().PreemphState()
		goOverlapAfter := goDec.GetCELTDecoder().OverlapBuffer()

		if libN <= 0 {
			continue
		}

		n := minInt(len(goPcm), libN)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		// Compute overlap buffer energy
		var overlapEnergyBefore, overlapEnergyAfter float64
		for _, v := range goOverlapBefore {
			overlapEnergyBefore += v * v
		}
		for _, v := range goOverlapAfter {
			overlapEnergyAfter += v * v
		}

		t.Logf("Pkt %d (fs=%d):", i, toc.FrameSize)
		t.Logf("  SNR: %.1f dB", snr)
		t.Logf("  Before: preemph_state=%.6f (lib=%.6f, err=%.2e), overlap_energy=%.6f",
			goStateBefore[0], libMemBefore, math.Abs(goStateBefore[0]-float64(libMemBefore)), overlapEnergyBefore)
		t.Logf("  After:  preemph_state=%.6f (lib=%.6f, err=%.2e), overlap_energy=%.6f",
			goStateAfter[0], libMemAfter, math.Abs(goStateAfter[0]-float64(libMemAfter)), overlapEnergyAfter)
	}
}

// TestFindFirstDegradedPacket finds the first packet where quality degrades
func TestFindFirstDegradedPacket(t *testing.T) {
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

	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	t.Log("Finding first packet with SNR < 80 dB:")

	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])

		goPcm, _ := decodeFloat32(goDec, pkt)
		libPcm, libN := libDec.DecodeFloat(pkt, 5760)

		if libN <= 0 {
			continue
		}

		n := minInt(len(goPcm), libN)
		var sig, noise float64
		for j := 0; j < n; j++ {
			s := float64(libPcm[j])
			d := float64(goPcm[j]) - s
			sig += s * s
			noise += d * d
		}
		snr := 10 * math.Log10(sig/noise)

		if snr < 80 {
			t.Logf("FOUND: Packet %d is first with SNR < 80 dB", i)
			t.Logf("  SNR: %.1f dB", snr)
			t.Logf("  Frame size: %d samples", toc.FrameSize)
			t.Logf("  Packet length: %d bytes", len(pkt))

			// Show context
			if i > 0 {
				prevToc := gopus.ParseTOC(packets[i-1][0])
				t.Logf("  Previous packet: fs=%d", prevToc.FrameSize)
			}
			return
		}
	}
	t.Log("All packets have SNR >= 80 dB")
}
