// Package cgo compares SILK decoder state at packet 826.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12StateCompare826 compares gopus vs libopus SILK decoder state.
func TestTV12StateCompare826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Create libopus decoder
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	t.Log("=== Processing packets 0-825 ===")

	// Track bandwidth changes
	bwNames := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}
	var lastBW silk.Bandwidth

	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// libopus
		libDec.DecodeFloat(pkt, 1920)

		// gopus SILK (skip non-SILK)
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				// Use DecodeFrame to test just the SILK core
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				silkDec.DecodeFrame(&rd, silkBW, duration, true)

				if silkBW != lastBW {
					t.Logf("Packet %d: %s → %s", i, bwNames[lastBW], bwNames[silkBW])
					lastBW = silkBW
				}
			}
		}
	}

	t.Log("\n=== Comparing at packet 826 (NB) ===")

	// Export gopus state BEFORE decoding packet 826
	goState := silkDec.ExportState()
	t.Logf("Gopus state before 826:")
	t.Logf("  prevGainQ16: %d", goState.PrevGainQ16)
	t.Logf("  sLPCQ14Buf[0:4]: [%d, %d, %d, %d]",
		goState.SLPCQ14Buf[0], goState.SLPCQ14Buf[1], goState.SLPCQ14Buf[2], goState.SLPCQ14Buf[3])
	t.Logf("  fsKHz: %d, lpcOrder: %d", goState.FsKHz, goState.LpcOrder)

	// Decode packet 826
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	// Gopus native decode
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goNative, _ := silkDec.DecodeFrame(&rd, silkBW, duration, true)

	// Libopus 48kHz decode
	libOut, libN := libDec.DecodeFloat(pkt, 1920)

	// Check gopus state AFTER decoding
	goStateAfter := silkDec.ExportState()
	t.Logf("\nGopus state after 826:")
	t.Logf("  prevGainQ16: %d", goStateAfter.PrevGainQ16)
	t.Logf("  sLPCQ14Buf[0:4]: [%d, %d, %d, %d]",
		goStateAfter.SLPCQ14Buf[0], goStateAfter.SLPCQ14Buf[1], goStateAfter.SLPCQ14Buf[2], goStateAfter.SLPCQ14Buf[3])

	// Native output comparison
	goMax := float32(0)
	goSum := float32(0)
	for _, s := range goNative {
		if abs := float32(math.Abs(float64(s))); abs > goMax {
			goMax = abs
		}
		goSum += s
	}
	goMean := goSum / float32(len(goNative))

	libMax := float32(0)
	libSum := float32(0)
	for i := 0; i < libN; i++ {
		if abs := float32(math.Abs(float64(libOut[i]))); abs > libMax {
			libMax = abs
		}
		libSum += libOut[i]
	}
	libMean := libSum / float32(libN)

	t.Logf("\nOutput comparison:")
	t.Logf("  Gopus native (8kHz): %d samples, max=%.6f, mean=%.6f", len(goNative), goMax, goMean)
	t.Logf("  Libopus 48kHz: %d samples, max=%.6f, mean=%.6f", libN, libMax, libMean)

	// Show first 10 native samples
	t.Log("\nGopus native (first 10):")
	for i := 0; i < 10 && i < len(goNative); i++ {
		t.Logf("  [%d] %.6f", i, goNative[i])
	}

	// Check if native samples are reasonable
	if goMax < 0.001 {
		t.Log("\n*** WARNING: Gopus native output is very quiet (max < 0.001)")
		t.Log("*** This suggests SILK decoder state is wrong")
	}
}
