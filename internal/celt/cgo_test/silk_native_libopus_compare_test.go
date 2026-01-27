package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func loadPacketsSimple(bitFile string, maxPackets int) ([][]byte, error) {
	data, err := os.ReadFile(bitFile)
	if err != nil {
		return nil, err
	}
	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		if maxPackets > 0 && len(packets) >= maxPackets {
			break
		}
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		_ = binary.BigEndian.Uint32(data[offset:])
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}
	return packets, nil
}

func TestSilkNativeLibopus8000Packet1(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 5)
	if err != nil || len(packets) < 2 {
		t.Skip("Could not load enough packets")
	}
	pkt := packets[1]
	toc := gopus.ParseTOC(pkt[0])
	if toc.Mode != gopus.ModeSILK {
		t.Skip("Not SILK mode")
	}
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	// gopus native decode (8k for NB)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()
	goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus native decode failed: %v", err)
	}

	// libopus decode at 8k
	libDec, err := NewLibopusDecoder(8000, 1)
	if err != nil || libDec == nil {
		t.Fatalf("Failed to create libopus decoder at 8k")
	}
	defer libDec.Destroy()

	libPcm, libSamples := libDec.DecodeFloat(pkt, 960) // 60ms@8k=480, buffer generous
	if libSamples < 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}
	if libSamples != len(goNative) {
		t.Logf("Sample count mismatch: go=%d lib=%d", len(goNative), libSamples)
	}

	// Compare
	minN := len(goNative)
	if libSamples < minN {
		minN = libSamples
	}
	firstDiff := -1
	var sigPow, noisePow float64
	for i := 0; i < minN; i++ {
		sig := float64(libPcm[i])
		noise := float64(goNative[i]) - sig
		sigPow += sig * sig
		noisePow += noise * noise
		if firstDiff < 0 && math.Abs(noise) > 1e-6 {
			firstDiff = i
		}
	}
	snr := 10 * math.Log10(sigPow/noisePow)
	if math.IsNaN(snr) || math.IsInf(snr, 1) {
		snr = 999
	}
	if firstDiff < 0 {
		firstDiff = minN
	}
	// Log around first diff
	start := firstDiff - 5
	if start < 0 {
		start = 0
	}
	end := firstDiff + 10
	if end > minN {
		end = minN
	}
	for i := start; i < end; i++ {
		t.Logf("[%d] go=%.6f lib=%.6f diff=%.6f", i, goNative[i], libPcm[i], goNative[i]-libPcm[i])
	}
	t.Logf("First diff at %d, SNR=%.1f dB", firstDiff, snr)
}
