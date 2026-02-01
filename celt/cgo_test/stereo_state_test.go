//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests stereo decoder state accumulation
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStereoStateAccumulation checks how L-R difference develops through packets
func TestStereoStateAccumulation(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
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
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	// Decode packets and track the L-R difference
	t.Log("Tracking L-R RMS difference through packets:")
	t.Log("Pkt | Mode  | lib L-R rms | go L-R rms | L err     | R err")
	t.Log("----|-------|-------------|------------|-----------|----------")

	for pktIdx := 0; pktIdx <= 20 && pktIdx < len(packets); pktIdx++ {
		pkt := packets[pktIdx]
		toc := pkt[0]
		config := toc >> 3

		var mode string
		switch {
		case config <= 11:
			mode = "SILK"
		case config <= 15:
			mode = "Hyb "
		default:
			mode = "CELT"
		}

		goSamplesF32, _ := decodeFloat32(goDec, pkt)
		libPcm, libSamples := libDec.DecodeFloat(pkt, 5760)

		// Compute L-R RMS for both decoders
		var libLRDiffSq, goLRDiffSq float64
		var lDiffSq, rDiffSq float64
		for i := 0; i < libSamples && i*2+1 < len(goSamplesF32); i++ {
			libL := float64(libPcm[i*2])
			libR := float64(libPcm[i*2+1])
			goL := float64(goSamplesF32[i*2])
			goR := float64(goSamplesF32[i*2+1])

			libLRDiffSq += (libL - libR) * (libL - libR)
			goLRDiffSq += (goL - goR) * (goL - goR)
			lDiffSq += (goL - libL) * (goL - libL)
			rDiffSq += (goR - libR) * (goR - libR)
		}

		libLRRms := math.Sqrt(libLRDiffSq / float64(libSamples))
		goLRRms := math.Sqrt(goLRDiffSq / float64(libSamples))
		lErrRms := math.Sqrt(lDiffSq / float64(libSamples))
		rErrRms := math.Sqrt(rDiffSq / float64(libSamples))

		t.Logf("%3d | %s  | %.6f    | %.6f   | %.6f  | %.6f", pktIdx, mode, libLRRms, goLRRms, lErrRms, rErrRms)
	}
}

// TestFreshDecoderVsStateful compares fresh decoder output vs stateful decoder
func TestFreshDecoderVsStateful(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector08.bit"
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

	pkt := packets[14]
	t.Logf("Testing packet 14: len=%d", len(pkt))

	// Fresh decoder - should produce L=R since no previous state
	freshGo, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	freshSamples, _ := decodeFloat32(freshGo, pkt)

	freshLib, _ := NewLibopusDecoder(48000, 2)
	freshLibPcm, freshLibSamples := freshLib.DecodeFloat(pkt, 5760)
	freshLib.Destroy()

	// Compute L-R for fresh decoders
	var freshGoLRSq, freshLibLRSq float64
	for i := 0; i < freshLibSamples && i*2+1 < len(freshSamples); i++ {
		goL := float64(freshSamples[i*2])
		goR := float64(freshSamples[i*2+1])
		libL := float64(freshLibPcm[i*2])
		libR := float64(freshLibPcm[i*2+1])
		freshGoLRSq += (goL - goR) * (goL - goR)
		freshLibLRSq += (libL - libR) * (libL - libR)
	}

	t.Logf("Fresh gopus  L-R RMS: %.6f", math.Sqrt(freshGoLRSq/float64(freshLibSamples)))
	t.Logf("Fresh libopus L-R RMS: %.6f", math.Sqrt(freshLibLRSq/float64(freshLibSamples)))

	// Now stateful decoder - decode packets 0-13 first
	statefulGo, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	statefulLib, _ := NewLibopusDecoder(48000, 2)
	defer statefulLib.Destroy()

	for i := 0; i < 14; i++ {
		decodeFloat32(statefulGo, packets[i])
		statefulLib.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 14
	statefulSamples, _ := decodeFloat32(statefulGo, pkt)
	statefulLibPcm, statefulLibSamples := statefulLib.DecodeFloat(pkt, 5760)

	var statefulGoLRSq, statefulLibLRSq float64
	for i := 0; i < statefulLibSamples && i*2+1 < len(statefulSamples); i++ {
		goL := float64(statefulSamples[i*2])
		goR := float64(statefulSamples[i*2+1])
		libL := float64(statefulLibPcm[i*2])
		libR := float64(statefulLibPcm[i*2+1])
		statefulGoLRSq += (goL - goR) * (goL - goR)
		statefulLibLRSq += (libL - libR) * (libL - libR)
	}

	t.Logf("Stateful gopus  L-R RMS: %.6f", math.Sqrt(statefulGoLRSq/float64(statefulLibSamples)))
	t.Logf("Stateful libopus L-R RMS: %.6f", math.Sqrt(statefulLibLRSq/float64(statefulLibSamples)))

	// The difference between fresh and stateful shows how much state matters
	t.Log("If fresh produces L=R but stateful doesn't, the L-R difference comes from state")
}
