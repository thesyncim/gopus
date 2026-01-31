// Package cgo tests if state reset fixes R channel error
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestStateResetPerPacket tests if resetting decoder state before each packet fixes the error
func TestStateResetPerPacket(t *testing.T) {
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

	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Cannot create libopus decoder")
	}
	defer libDec.Destroy()

	// Compare two approaches:
	// 1. Continuous decoding (with state)
	// 2. Fresh decoder per packet (no state)

	t.Log("=== Continuous decoding (with state) ===")
	goContinuous, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	for pktIdx := 0; pktIdx < 20 && pktIdx < len(packets); pktIdx++ {
		goCont, _ := decodeFloat32(goContinuous, packets[pktIdx])
		libPcm, libSamples := libDec.DecodeFloat(packets[pktIdx], 5760)

		maxRErr := float64(0)
		for i := 0; i < libSamples && i*2+1 < len(goCont); i++ {
			rErr := math.Abs(float64(goCont[i*2+1]) - float64(libPcm[i*2+1]))
			if rErr > maxRErr {
				maxRErr = rErr
			}
		}
		if maxRErr > 1e-6 {
			t.Logf("Pkt %2d: continuous max R err = %.6e", pktIdx, maxRErr)
		}
	}

	// Reset libopus
	libDec.Destroy()
	libDec, _ = NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	t.Log("\n=== Fresh decoder per packet (no state) ===")
	for pktIdx := 0; pktIdx < 20 && pktIdx < len(packets); pktIdx++ {
		goFresh, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
		// Also create fresh libopus decoder
		libFresh, _ := NewLibopusDecoder(48000, 2)

		goSamples, _ := decodeFloat32(goFresh, packets[pktIdx])
		libPcm, libSamples := libFresh.DecodeFloat(packets[pktIdx], 5760)
		libFresh.Destroy()

		maxRErr := float64(0)
		for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
			rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
			if rErr > maxRErr {
				maxRErr = rErr
			}
		}
		if maxRErr > 1e-6 {
			t.Logf("Pkt %2d: fresh max R err = %.6e", pktIdx, maxRErr)
		}
	}

	t.Log("\n=== Test if error appears after specific packet ===")
	// Decode packets 0-13, then reset gopus but not libopus, then compare packet 14
	libDec.Destroy()
	libDec, _ = NewLibopusDecoder(48000, 2)
	goPartial, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))

	// Sync both decoders through packets 0-13
	for i := 0; i < 14; i++ {
		decodeFloat32(goPartial, packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 14 with both
	goSamples, _ := decodeFloat32(goPartial, packets[14])
	libPcm, libSamples := libDec.DecodeFloat(packets[14], 5760)

	maxRErr := float64(0)
	maxLErr := float64(0)
	for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
		lErr := math.Abs(float64(goSamples[i*2]) - float64(libPcm[i*2]))
		rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
		if lErr > maxLErr {
			maxLErr = lErr
		}
		if rErr > maxRErr {
			maxRErr = rErr
		}
	}
	t.Logf("Packet 14 after syncing 0-13: max L err = %.6e, max R err = %.6e", maxLErr, maxRErr)
}

// TestIsolatePacket14Error tries to isolate if the error is in the packet itself or state
func TestIsolatePacket14Error(t *testing.T) {
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

	// Decode packet 14 with fresh decoders (no prior state)
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 2))
	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Cannot create libopus decoder")
	}
	defer libDec.Destroy()

	goSamples, err := decodeFloat32(goDec, packets[14])
	if err != nil {
		t.Fatalf("gopus decode error: %v", err)
	}
	libPcm, libSamples := libDec.DecodeFloat(packets[14], 5760)

	t.Logf("Packet 14 with fresh decoders (no prior packets):")
	t.Logf("  gopus samples: %d, libopus samples: %d", len(goSamples)/2, libSamples)

	maxLErr := float64(0)
	maxRErr := float64(0)
	maxLIdx := 0
	maxRIdx := 0

	for i := 0; i < libSamples && i*2+1 < len(goSamples); i++ {
		lErr := math.Abs(float64(goSamples[i*2]) - float64(libPcm[i*2]))
		rErr := math.Abs(float64(goSamples[i*2+1]) - float64(libPcm[i*2+1]))
		if lErr > maxLErr {
			maxLErr = lErr
			maxLIdx = i
		}
		if rErr > maxRErr {
			maxRErr = rErr
			maxRIdx = i
		}
	}
	t.Logf("  Max L error: %.6e at sample %d", maxLErr, maxLIdx)
	t.Logf("  Max R error: %.6e at sample %d", maxRErr, maxRIdx)
}
