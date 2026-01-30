// Package cgo traces the full Decode flow at packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12DecodeTrace traces the full Decode flow at packet 826.
func TestTV12DecodeTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	// Process packets 0-825
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Decode packet 826 using full Decode flow
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("Packet 826: Mode=%v BW=%d", toc.Mode, toc.Bandwidth)

	// Check sMid before decode
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid BEFORE: [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Check NB resampler state before decode
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbRes != nil {
		sIIR := nbRes.GetSIIR()
		t.Logf("NB resampler sIIR BEFORE: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	}

	// Call Decode
	output, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Check sMid after decode
	sMidAfter := silkDec.GetSMid()
	t.Logf("sMid AFTER: [%d, %d]", sMidAfter[0], sMidAfter[1])

	// Check NB resampler state after decode
	if nbRes != nil {
		sIIR := nbRes.GetSIIR()
		t.Logf("NB resampler sIIR AFTER: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	}

	// Check output
	t.Logf("Output length: %d", len(output))

	// Count zeros and find max
	zeroCount := 0
	maxAbs := float32(0)
	for _, s := range output {
		if s == 0 {
			zeroCount++
		}
		if s > maxAbs {
			maxAbs = s
		} else if -s > maxAbs {
			maxAbs = -s
		}
	}
	t.Logf("Zero count: %d/%d, max abs: %.6f", zeroCount, len(output), maxAbs)

	// Show first 20 samples
	t.Log("First 20 output samples:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%2d] %.6f", i, output[i])
	}

	// Show samples around the middle
	if len(output) > 500 {
		t.Log("Samples 480-500:")
		for i := 480; i < 500 && i < len(output); i++ {
			t.Logf("  [%3d] %.6f", i, output[i])
		}
	}
}
