// Package cgo traces dual-stereo decoding to identify divergence
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestDualStereoTracePacket14 traces dual-stereo decoding for packet 14
func TestDualStereoTracePacket14(t *testing.T) {
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

	// Enable dual-stereo debug tracing
	celt.DebugDualStereo = true
	celt.DebugDualStereoAlloc = true
	defer func() {
		celt.DebugDualStereo = false
		celt.DebugDualStereoAlloc = false
	}()

	// Create fresh decoders
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	if libDec == nil {
		t.Skip("Cannot create libopus decoder")
	}
	defer libDec.Destroy()

	// Decode packets 0-13 to sync state (with debug on to see all)
	t.Log("Decoding packets 0-13 to sync state...")
	for i := 0; i < 14; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Now decode packet 14
	t.Log("\n=== Decoding packet 14 with dual-stereo tracing ===")

	goSamples, _ := goDec.DecodeFloat32(packets[14])
	libPcm, _ := libDec.DecodeFloat(packets[14], 5760)

	// Compare first few samples
	t.Log("\nFirst 10 samples comparison:")
	for i := 0; i < 10; i++ {
		goL := goSamples[i*2]
		goR := goSamples[i*2+1]
		libL := libPcm[i*2]
		libR := libPcm[i*2+1]
		t.Logf("  [%d] L: go=%.6f lib=%.6f diff=%.2e | R: go=%.6f lib=%.6f diff=%.2e",
			i, goL, libL, goL-libL, goR, libR, goR-libR)
	}

	// Find max error position
	maxRErr := float32(0)
	maxRIdx := 0
	for i := 0; i < 960 && i*2+1 < len(goSamples); i++ {
		rErr := goSamples[i*2+1] - libPcm[i*2+1]
		if rErr < 0 {
			rErr = -rErr
		}
		if rErr > maxRErr {
			maxRErr = rErr
			maxRIdx = i
		}
	}
	t.Logf("\nMax R error at sample %d: %.6f", maxRIdx, maxRErr)
}
