// Package cgo compares resampler input between gopus and silk decoder paths
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerInputCompare traces what input the resampler receives
// in both gopus.Decoder and standalone silk.Decoder paths
func TestTV12ResamplerInputCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("=== Testing what resampler receives in each path ===")

	// Path 1: gopus.Decoder
	var gopusOutput []float32
	var gopusDelayBufBefore [12]int16
	var gopusDelayBufAfter [12]int16
	{
		goDec, _ := gopus.NewDecoderDefault(48000, 1)
		silkDec := goDec.GetSILKDecoder()

		// Process packets 0-136
		for i := 0; i < 137; i++ {
			goDec.DecodeFloat32(packets[i])
		}

		// Get MB resampler (creates it if not exists)
		mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
		mbRes.EnableDebug(true)
		delayBuf := mbRes.GetDelayBuf()
		copy(gopusDelayBufBefore[:], delayBuf[:12])

		// Decode packet 137
		gopusOutput, _ = goDec.DecodeFloat32(packets[137])

		delayBuf = mbRes.GetDelayBuf()
		copy(gopusDelayBufAfter[:], delayBuf[:12])

		t.Logf("\n=== gopus.Decoder path ===")
		t.Logf("MB delayBuf BEFORE: [%d, %d, %d, %d | %d, %d, %d, %d, %d, %d, %d, %d]",
			gopusDelayBufBefore[0], gopusDelayBufBefore[1], gopusDelayBufBefore[2], gopusDelayBufBefore[3],
			gopusDelayBufBefore[4], gopusDelayBufBefore[5], gopusDelayBufBefore[6], gopusDelayBufBefore[7],
			gopusDelayBufBefore[8], gopusDelayBufBefore[9], gopusDelayBufBefore[10], gopusDelayBufBefore[11])
		t.Logf("MB delayBuf AFTER:  [%d, %d, %d, %d | %d, %d, %d, %d, %d, %d, %d, %d]",
			gopusDelayBufAfter[0], gopusDelayBufAfter[1], gopusDelayBufAfter[2], gopusDelayBufAfter[3],
			gopusDelayBufAfter[4], gopusDelayBufAfter[5], gopusDelayBufAfter[6], gopusDelayBufAfter[7],
			gopusDelayBufAfter[8], gopusDelayBufAfter[9], gopusDelayBufAfter[10], gopusDelayBufAfter[11])
		t.Logf("Debug input first 10: %v", mbRes.GetDebugInputFirst10())
		t.Logf("Debug delayBuf at Process start: %v", mbRes.GetDebugDelayBufFirst8())
		t.Logf("Debug Process call count: %d", mbRes.GetDebugProcessCallCount())
		t.Logf("Debug resampler ID: %d, last process ID: %d", mbRes.GetDebugID(), mbRes.GetDebugLastProcessID())
		debugOut := mbRes.GetDebugOutputFirst10()
		t.Logf("Debug output (inside Process): %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
			debugOut[0], debugOut[1], debugOut[2], debugOut[3], debugOut[4],
			debugOut[5], debugOut[6], debugOut[7], debugOut[8], debugOut[9])
		t.Logf("Return output first 10: %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
			gopusOutput[0], gopusOutput[1], gopusOutput[2], gopusOutput[3], gopusOutput[4],
			gopusOutput[5], gopusOutput[6], gopusOutput[7], gopusOutput[8], gopusOutput[9])
	}

	// Path 2: standalone silk.Decoder
	var silkOutput []float32
	var silkDelayBufBefore [12]int16
	var silkDelayBufAfter [12]int16
	{
		silkDec := silk.NewDecoder()

		// Process packets 0-136
		for i := 0; i <= 136; i++ {
			pkt := packets[i]
			toc := gopus.ParseTOC(pkt[0])
			if toc.Mode == gopus.ModeSILK {
				silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
				if ok {
					silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
				}
			}
		}

		// Get MB resampler (creates it if not exists)
		mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
		mbRes.EnableDebug(true)
		delayBuf := mbRes.GetDelayBuf()
		copy(silkDelayBufBefore[:], delayBuf[:12])

		// Decode packet 137
		pkt := packets[137]
		toc := gopus.ParseTOC(pkt[0])
		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		silkOutput, _ = silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)

		delayBuf = mbRes.GetDelayBuf()
		copy(silkDelayBufAfter[:], delayBuf[:12])

		t.Logf("\n=== silk.Decoder path ===")
		t.Logf("MB delayBuf BEFORE: [%d, %d, %d, %d | %d, %d, %d, %d, %d, %d, %d, %d]",
			silkDelayBufBefore[0], silkDelayBufBefore[1], silkDelayBufBefore[2], silkDelayBufBefore[3],
			silkDelayBufBefore[4], silkDelayBufBefore[5], silkDelayBufBefore[6], silkDelayBufBefore[7],
			silkDelayBufBefore[8], silkDelayBufBefore[9], silkDelayBufBefore[10], silkDelayBufBefore[11])
		t.Logf("MB delayBuf AFTER:  [%d, %d, %d, %d | %d, %d, %d, %d, %d, %d, %d, %d]",
			silkDelayBufAfter[0], silkDelayBufAfter[1], silkDelayBufAfter[2], silkDelayBufAfter[3],
			silkDelayBufAfter[4], silkDelayBufAfter[5], silkDelayBufAfter[6], silkDelayBufAfter[7],
			silkDelayBufAfter[8], silkDelayBufAfter[9], silkDelayBufAfter[10], silkDelayBufAfter[11])
		t.Logf("Debug input first 10: %v", mbRes.GetDebugInputFirst10())
		t.Logf("Debug delayBuf at Process start: %v", mbRes.GetDebugDelayBufFirst8())
		t.Logf("Debug Process call count: %d", mbRes.GetDebugProcessCallCount())
		t.Logf("Debug resampler ID: %d, last process ID: %d", mbRes.GetDebugID(), mbRes.GetDebugLastProcessID())
		debugOut := mbRes.GetDebugOutputFirst10()
		t.Logf("Debug output (inside Process): %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
			debugOut[0], debugOut[1], debugOut[2], debugOut[3], debugOut[4],
			debugOut[5], debugOut[6], debugOut[7], debugOut[8], debugOut[9])
		t.Logf("Return output first 10: %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
			silkOutput[0], silkOutput[1], silkOutput[2], silkOutput[3], silkOutput[4],
			silkOutput[5], silkOutput[6], silkOutput[7], silkOutput[8], silkOutput[9])
	}

	// Compare
	t.Log("\n=== Comparison ===")
	if gopusDelayBufBefore == silkDelayBufBefore {
		t.Log("delayBuf BEFORE: SAME")
	} else {
		t.Log("delayBuf BEFORE: DIFFERENT!")
	}
}
