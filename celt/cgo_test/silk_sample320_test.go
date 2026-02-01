//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestSilkSample320Trace traces the exact computation of sample 320
// to find the root cause of the 1 LSB difference.
func TestSilkSample320Trace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 10)
	if err != nil || len(packets) < 5 {
		t.Skip("Could not load packets")
	}

	// First, decode packets 0-3 to build state (important for accurate comparison!)
	goDec := silk.NewDecoder()
	for pktIdx := 0; pktIdx < 4; pktIdx++ {
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			t.Fatalf("Invalid SILK bandwidth for packet %d", pktIdx)
		}
		duration := silk.FrameDurationFromTOC(toc.FrameSize)
		var rd rangecoding.Decoder
		rd.Init(pkt[1:])
		_, err := goDec.DecodeFrame(&rd, silkBW, duration, pktIdx == 0)
		if err != nil {
			t.Fatalf("Failed to decode packet %d: %v", pktIdx, err)
		}
	}
	t.Log("Decoded packets 0-3 for state buildup")

	pkt4 := packets[4]
	toc := gopus.ParseTOC(pkt4[0])
	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	// Decode packet 4 with trace, using accumulated state
	var rd rangecoding.Decoder
	rd.Init(pkt4[1:])

	var frame2K0Info silk.TraceInfo
	goOutput, err := goDec.DecodeFrameWithTrace(&rd, silkBW, duration, true, func(frame, k int, info silk.TraceInfo) {
		if frame == 2 && k == 0 {
			frame2K0Info = info
			t.Logf("\nFrame 2, k=0 intermediate values:")
			t.Logf("  SignalType: %d (2=voiced)", info.SignalType)
			t.Logf("  PitchLag: %d", info.PitchLag)
			t.Logf("  LtpMemLength: %d", info.LtpMemLength)
			t.Logf("  LpcOrder: %d", info.LpcOrder)
			t.Logf("  InvGainQ31: %d", info.InvGainQ31)
			t.Logf("  GainQ10: %d", info.GainQ10)
			t.Logf("  LTPCoefQ14: %v", info.LTPCoefQ14)
			t.Logf("  FirstPresQ14: %d", info.FirstPresQ14)
			t.Logf("  FirstLpcPredQ10: %d", info.FirstLpcPredQ10)
			t.Logf("  FirstSLPC: %d", info.FirstSLPC)
			t.Logf("  FirstOutputQ0: %d", info.FirstOutputQ0)
			t.Logf("  SLPCHistory[0:16]: %v", info.SLPCHistory)
			t.Logf("  A_Q12[0:10]: %v", info.A_Q12[0:10])
			t.Logf("  FirstExcQ14: %d", info.FirstExcQ14)
			t.Logf("  SLTPQ15Used: %v", info.SLTPQ15Used)
			t.Logf("  FirstLTPPredQ13: %d", info.FirstLTPPredQ13)

			// Manually compute LTP prediction
			t.Logf("\n  Manual LTP prediction for sample 0:")
			ltpPredManual := int32(2)
			for j := 0; j < 5; j++ {
				sltpVal := info.SLTPQ15Used[j]
				coef := int32(info.LTPCoefQ14[j])
				// silkSMLAWB(a, b, c) = a + ((b >> 16) * c) + (((b & 0xFFFF) * c) >> 16)
				contrib := int32((int64(sltpVal>>16)*int64(coef) + (int64(sltpVal&0xFFFF)*int64(coef))>>16))
				ltpPredManual += contrib
				t.Logf("  j=%d: sLTP_Q15=%d * B_Q14=%d => contrib=%d, total=%d", j, sltpVal, coef, contrib, ltpPredManual)
			}
			t.Logf("  ltpPredQ13 manual=%d, traced=%d", ltpPredManual, info.FirstLTPPredQ13)
			t.Logf("  presQ14 = excQ14 + (ltpPredQ13 << 1) = %d + %d = %d (traced: %d)",
				info.FirstExcQ14, ltpPredManual<<1, info.FirstExcQ14+ltpPredManual<<1, info.FirstPresQ14)

			// Manually compute LPC prediction for sample 0
			lpcOrder := 10
			lpcPredManual := int32(lpcOrder >> 1) // = 5
			t.Logf("\n  Manual LPC prediction for sample 0:")
			t.Logf("  Initial lpcPredQ10 = %d", lpcPredManual)
			for j := 0; j < lpcOrder; j++ {
				histIdx := 15 - j // sLPC[maxLPCOrder + 0 - j - 1] = sLPC[15 - j]
				histVal := info.SLPCHistory[histIdx]
				coef := int32(info.A_Q12[j])
				contrib := int32((int64(histVal) * int64(int16(coef))) >> 16)
				lpcPredManual += contrib
				if j < 5 {
					t.Logf("  j=%d: sLPC[%d]=%d * A_Q12[%d]=%d >> 16 = %d, total=%d",
						j, histIdx, histVal, j, coef, contrib, lpcPredManual)
				}
			}
			t.Logf("  Final lpcPredQ10 (manual) = %d, (traced) = %d", lpcPredManual, info.FirstLpcPredQ10)
		}
	})
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Get libopus native output for comparison using stateful decoder
	libState := NewSilkDecoderState()
	if libState == nil {
		t.Fatal("Could not create libopus SILK state")
	}
	defer libState.Free()

	// Build libopus state from packets 0-3 (same as gopus)
	framesPerPacket, nbSubfr := frameParamsForDuration(duration)
	fsKHz := config.SampleRate / 1000
	for pktIdx := 0; pktIdx < 4; pktIdx++ {
		pkt := packets[pktIdx]
		_, _ = libState.DecodePacketNativeCore(pkt[1:], fsKHz, nbSubfr, framesPerPacket)
	}

	// Now decode packet 4 with accumulated state
	libNative, err := libState.DecodePacketNativeCore(pkt4[1:], fsKHz, nbSubfr, framesPerPacket)
	if err != nil || len(libNative) < 321 {
		t.Fatal("libopus native core decode failed")
	}

	goSample320 := int(goOutput[320] * 32768)
	libSample320 := int(libNative[320])

	t.Logf("\nSample 320 comparison:")
	t.Logf("  gopus:   %d", goSample320)
	t.Logf("  libopus: %d", libSample320)
	t.Logf("  diff:    %d", goSample320-libSample320)

	// Reverse-engineer: what sLPC would produce libopus output?
	gainQ10 := frame2K0Info.GainQ10
	t.Logf("\nReverse engineering:")
	t.Logf("  GainQ10: %d", gainQ10)

	// output = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC, gainQ10), 8))
	// So: output << 8 ≈ (sLPC * gainQ10) >> 16
	// Therefore: sLPC ≈ (output << 8) << 16 / gainQ10
	// But with rounding: sLPC * gainQ10 >> 16 >> 8 = output (with rounding)

	// Let's compute what sLPC values would produce these outputs
	// silkSMULWW(sLPC, gainQ10) = (sLPC * gainQ10) >> 16
	// silkRSHIFT_ROUND(x, 8) = (x >> 7 + 1) >> 1

	// For gopus output = 1454
	// For libopus output = 1455

	// Working backwards:
	// output = ((sLPC * gainQ10) >> 16 + 128) >> 8  (simplified rounding)

	// The difference of 1 in output means a difference of ~256 in (sLPC * gainQ10) >> 16
	// Which means a difference of ~256 << 16 / gainQ10 = 4177920 / 962560 ≈ 4.3 in sLPC

	t.Logf("  For output diff of 1, sLPC diff ≈ %.1f (Q14)", float64(256<<16)/float64(gainQ10))

	// Now let's see if the difference is in presQ14 or lpcPredQ10
	// sLPC = presQ14 + (lpcPredQ10 << 4)
	// A difference of 4.3 in sLPC could come from:
	// - 4.3 difference in presQ14 (Q14)
	// - 4.3 / 16 = 0.27 difference in lpcPredQ10 (Q10)

	t.Logf("  FirstPresQ14 from gopus: %d", frame2K0Info.FirstPresQ14)

	// Manual verification of the computation
	t.Log("\nManual computation verification:")
	presQ14 := frame2K0Info.FirstPresQ14
	lpcPredQ10 := frame2K0Info.FirstLpcPredQ10
	sLPC := presQ14 + (lpcPredQ10 << 4)
	t.Logf("  sLPC = presQ14 + (lpcPredQ10 << 4) = %d + (%d << 4) = %d + %d = %d",
		presQ14, lpcPredQ10, presQ14, lpcPredQ10<<4, sLPC)
	t.Logf("  Actual FirstSLPC from trace: %d", frame2K0Info.FirstSLPC)

	// Compute output
	gainQ10Val := frame2K0Info.GainQ10
	smulwwResult := int32((int64(sLPC) * int64(gainQ10Val)) >> 16)
	t.Logf("  silkSMULWW(sLPC=%d, gainQ10=%d) = %d", sLPC, gainQ10Val, smulwwResult)

	// Rounding
	roundedShift7 := smulwwResult >> 7
	roundedFinal := (roundedShift7 + 1) >> 1
	t.Logf("  silkRSHIFT_ROUND(%d, 8) = ((%d >> 7) + 1) >> 1 = (%d + 1) >> 1 = %d",
		smulwwResult, smulwwResult, roundedShift7, roundedFinal)

	// Verify bit-exact match
	if roundedFinal == int32(libSample320) {
		t.Log("\n✓ Sample 320 is BIT-EXACT between gopus and libopus native core")
	} else {
		t.Logf("\n✗ Sample 320 mismatch: computed=%d, libopus=%d", roundedFinal, libSample320)
	}
}
