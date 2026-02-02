package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestLPCMathTrace traces the exact math in silkDecodeCore.
func TestLPCMathTrace(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000

	// Generate test signal
	amplitude := float32(0.3)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = amplitude * float32(math.Sin(2*math.Pi*300*tm))
	}

	// Encode
	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode
	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)
	decoded, err := decoder.DecodeFrame(&rd, BandwidthWideband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]

	// Get gains
	var ctrl decoderControl
	var lastGain int8 = 10
	silkGainsDequant(&ctrl.GainsQ16, &st.indices.GainsIndices, &lastGain, false, 4)

	gainQ16 := ctrl.GainsQ16[0]
	gainQ10 := gainQ16 >> 6

	t.Logf("=== Detailed Math Trace ===")
	t.Logf("GainsQ16[0] = %d", gainQ16)
	t.Logf("GainQ10 = GainsQ16 >> 6 = %d", gainQ10)
	t.Logf("Signal type: %d (0=inactive, 1=unvoiced, 2=voiced)", st.indices.signalType)

	// Look at first few excitation values
	t.Logf("\nExcitation trace:")
	for i := 0; i < 5; i++ {
		excQ14 := st.excQ14[i]
		t.Logf("  excQ14[%d] = %d (%.4f linear)", i, excQ14, float64(excQ14)/16384.0)
	}

	// Now manually compute what the output SHOULD be
	// From silkDecodeCore:
	//   sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
	//   pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))

	t.Logf("\nManual output computation (assuming zero LPC history):")
	for i := 0; i < 5; i++ {
		excQ14 := st.excQ14[i]

		// For unvoiced or voiced with zero LTP history, presQ14 = excQ14
		presQ14 := excQ14

		// LPC prediction with zero history
		lpcPredQ10 := int32(st.lpcOrder >> 1) // Initial bias

		// sLPC = presQ14 + (lpcPredQ10 << 4)
		sLPC := silkAddSat32(presQ14, silkLShiftSAT32(lpcPredQ10, 4))

		// output = (sLPC * gainQ10) >> 8, saturated to int16
		product := silkSMULWW(sLPC, gainQ10)
		shifted := silkRSHIFT_ROUND(product, 8)
		output := silkSAT16(shifted)

		t.Logf("  [%d] presQ14=%d, lpcPredQ10=%d, sLPC=%d, product=%d, shifted=%d, output=%d",
			i, presQ14, lpcPredQ10, sLPC, product, shifted, output)
		t.Logf("       Actual decoded[%d] = %.4f (int16: %d)", i, decoded[i], int16(decoded[i]*32768))
	}

	// The key question: why is lpcPredQ10 only 8 (the rounding bias)?
	// Let's check if the LPC coefficients are being decoded correctly

	t.Logf("\nLPC coefficient check:")
	t.Logf("LPC order: %d", st.lpcOrder)
	t.Logf("NLSF interp coef Q2: %d", st.indices.NLSFInterpCoefQ2)

	// Check prevNLSFQ15 - these should be non-trivial
	t.Logf("Previous NLSF (first 5):")
	for i := 0; i < 5 && i < st.lpcOrder; i++ {
		t.Logf("  prevNLSFQ15[%d] = %d", i, st.prevNLSFQ15[i])
	}

	// The issue might be that we're checking the DECODER state after decode
	// but the LPC coefficients used during decode are stored elsewhere
}

// TestSimulateLPCSynthesis manually simulates the LPC synthesis to find the bug.
func TestSimulateLPCSynthesis(t *testing.T) {
	// Create a simple test case
	lpcOrder := 10
	subfrLength := 40

	// Create some sample excitation (small values)
	excQ14 := make([]int32, subfrLength)
	for i := range excQ14 {
		excQ14[i] = 1000 // Small excitation
	}

	// Create some sample LPC coefficients
	// For a predictable signal, LPC coefficients should be large
	A_Q12 := make([]int16, lpcOrder)
	A_Q12[0] = -4096 * 2 // -2.0 in Q12 - strong first-order prediction
	for i := 1; i < lpcOrder; i++ {
		A_Q12[i] = 0
	}

	// Create sLPC buffer with history
	sLPC := make([]int32, subfrLength+16)
	// Initialize history to zeros (simulating first frame)
	for i := 0; i < 16; i++ {
		sLPC[i] = 0
	}

	// Simulate the decode loop
	gainQ10 := int32(65536) // Gain of 64 in Q10

	t.Logf("=== LPC Synthesis Simulation ===")
	t.Logf("LPC order: %d", lpcOrder)
	t.Logf("A_Q12[0] = %d (%.2f linear)", A_Q12[0], float64(A_Q12[0])/4096.0)
	t.Logf("gainQ10 = %d (%.2f linear)", gainQ10, float64(gainQ10)/1024.0)

	for i := 0; i < 10; i++ {
		// LPC prediction
		lpcPredQ10 := int32(lpcOrder >> 1) // Initial bias
		for j := 0; j < lpcOrder; j++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[16+i-j-1], int32(A_Q12[j]))
		}

		// presQ14 for unvoiced = excQ14
		presQ14 := excQ14[i]

		// sLPC update
		sLPC[16+i] = silkAddSat32(presQ14, silkLShiftSAT32(lpcPredQ10, 4))

		// Output
		product := silkSMULWW(sLPC[16+i], gainQ10)
		shifted := silkRSHIFT_ROUND(product, 8)
		output := silkSAT16(shifted)

		t.Logf("[%d] lpcPredQ10=%d, presQ14=%d, sLPC=%d, output=%d",
			i, lpcPredQ10, presQ14, sLPC[16+i], output)
	}

	t.Logf("\nNow simulate with non-zero history:")

	// Reset sLPC with some history
	for i := 0; i < 16; i++ {
		sLPC[i] = 10000 // Non-zero history
	}

	for i := 0; i < 10; i++ {
		// LPC prediction
		lpcPredQ10 := int32(lpcOrder >> 1)
		for j := 0; j < lpcOrder; j++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[16+i-j-1], int32(A_Q12[j]))
		}

		presQ14 := excQ14[i]
		sLPC[16+i] = silkAddSat32(presQ14, silkLShiftSAT32(lpcPredQ10, 4))

		product := silkSMULWW(sLPC[16+i], gainQ10)
		shifted := silkRSHIFT_ROUND(product, 8)
		output := silkSAT16(shifted)

		t.Logf("[%d] lpcPredQ10=%d, presQ14=%d, sLPC=%d, output=%d",
			i, lpcPredQ10, presQ14, sLPC[16+i], output)
	}
}

// TestTraceActualDecodeCore traces the actual silkDecodeCore with instrumentation.
func TestTraceActualDecodeCore(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000

	// Generate test signal
	amplitude := float32(0.3)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = amplitude * float32(math.Sin(2*math.Pi*300*tm))
	}

	// Encode
	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode with trace callback
	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)

	traceCallback := func(frame, k int, info TraceInfo) {
		t.Logf("Subframe %d: GainQ10=%d, InvGainQ31=%d, FirstLTPPredQ13=%d",
			k, info.GainQ10, info.InvGainQ31, info.FirstLTPPredQ13)
	}

	decoded, err := decoder.DecodeFrameWithTrace(&rd, BandwidthWideband, Frame20ms, true, traceCallback)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]

	t.Logf("\nDecoded %d samples", len(decoded))
	t.Logf("Signal type: %d", st.indices.signalType)

	// Look at the sLPCQ14Buf AFTER decoding - this is the history for next frame
	t.Logf("\nsLPCQ14Buf after decode (history for next frame):")
	for i := 0; i < 16; i++ {
		if st.sLPCQ14Buf[i] != 0 {
			t.Logf("  sLPCQ14Buf[%d] = %d", i, st.sLPCQ14Buf[i])
		}
	}
}
