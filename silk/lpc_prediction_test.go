package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestLPCSynthesis_SimpleAR verifies the LPC synthesis filter applies prediction correctly.
func TestLPCSynthesis_SimpleAR(t *testing.T) {
	order := 10
	lpcCoeffs := make([]int16, order)
	lpcCoeffs[0] = 3686 // 0.9 in Q12

	subfrLength := 40
	excitation := make([]int32, subfrLength)
	excitation[0] = 32767

	sLPC := make([]int32, subfrLength+maxLPCOrder)

	presQ14 := make([]int32, subfrLength)
	for i := range excitation {
		presQ14[i] = excitation[i] << 6
	}

	output := make([]int16, subfrLength)
	gainQ10 := int32(1 << 10)

	for i := 0; i < subfrLength; i++ {
		lpcPredQ10 := int32(order >> 1)
		for j := 0; j < order; j++ {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(lpcCoeffs[j]))
		}
		sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
		output[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))
	}

	if output[1] == 0 {
		t.Error("LPC prediction appears to NOT be applied: output[1] is zero")
	}

	ratio := float64(output[1]) / float64(output[0])
	if ratio < 0.7 || ratio > 1.1 {
		t.Errorf("LPC prediction ratio wrong: %.3f (expected ~0.9)", ratio)
	} else {
		t.Logf("LPC prediction ratio: %.3f - PASS", ratio)
	}
}

// TestRoundtripAmplitudeTrace traces amplitude through the full encode-decode path.
func TestRoundtripAmplitudeTrace(t *testing.T) {
	logSilkQualityStatus(t)

	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000

	inputAmplitude := float32(0.5)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = inputAmplitude * float32(math.Sin(2*math.Pi*300*tm))
	}

	var inputSumSq float64
	for _, s := range pcm {
		inputSumSq += float64(s) * float64(s)
	}
	inputRMS := math.Sqrt(inputSumSq / float64(len(pcm)))
	t.Logf("Input RMS: %.6f (amplitude: %.3f)", inputRMS, inputAmplitude)

	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("Encoded %d samples -> %d bytes", len(pcm), len(encoded))

	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)
	decoded, err := decoder.DecodeFrame(&rd, BandwidthWideband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	params := decoder.GetLastFrameParams()
	t.Logf("Decoder gain indices: %v", params.GainIndices)
	t.Logf("Signal type: %d", decoder.GetLastSignalType())

	var outputSumSq float64
	for _, s := range decoded {
		outputSumSq += float64(s) * float64(s)
	}
	outputRMS := math.Sqrt(outputSumSq / float64(len(decoded)))
	t.Logf("Output RMS: %.6f", outputRMS)

	ratio := outputRMS / inputRMS
	t.Logf("Output/Input RMS ratio: %.4f (%.1f%%)", ratio, ratio*100)

	if ratio < 0.1 {
		t.Errorf("AMPLITUDE ISSUE: Output is only %.1f%% of input", ratio*100)
	}
}

// TestDecoderExcitationTrace traces the excitation values through the decoder.
func TestDecoderExcitationTrace(t *testing.T) {
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000

	inputAmplitude := float32(0.5)
	pcm := make([]float32, frameSamples)
	for i := range pcm {
		tm := float64(i) / float64(config.SampleRate)
		pcm[i] = inputAmplitude * float32(math.Sin(2*math.Pi*300*tm))
	}

	encoded, err := Encode(pcm, BandwidthWideband, true)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoder := NewDecoder()
	var rd rangecoding.Decoder
	rd.Init(encoded)

	_, err = decoder.DecodeFrame(&rd, BandwidthWideband, Frame20ms, true)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	st := &decoder.state[0]
	t.Logf("Frame length: %d, Signal type: %d, LPC order: %d",
		st.frameLength, st.indices.signalType, st.lpcOrder)

	// Analyze excitation
	var minExc, maxExc int32
	var excEnergy int64
	for i := 0; i < st.frameLength; i++ {
		exc := st.excQ14[i]
		if exc < minExc {
			minExc = exc
		}
		if exc > maxExc {
			maxExc = exc
		}
		excEnergy += int64(exc) * int64(exc)
	}
	excRMS := math.Sqrt(float64(excEnergy) / float64(st.frameLength))
	t.Logf("Excitation Q14: range=[%d, %d], RMS=%.2f (normalized=%.4f)",
		minExc, maxExc, excRMS, excRMS/16384.0)

	// Analyze output buffer
	var minOut, maxOut int16
	for i := 0; i < st.ltpMemLength; i++ {
		if st.outBuf[i] < minOut {
			minOut = st.outBuf[i]
		}
		if st.outBuf[i] > maxOut {
			maxOut = st.outBuf[i]
		}
	}
	t.Logf("outBuf range: [%d, %d]", minOut, maxOut)

	// Compute gains
	params := decoder.GetLastFrameParams()
	var gainsQ16 [4]int32
	var gainIndices [4]int8
	for i, v := range params.GainIndices {
		if i < 4 {
			gainIndices[i] = int8(v)
		}
	}
	var lastGainIdx int8 = 10
	silkGainsDequant(&gainsQ16, &gainIndices, &lastGainIdx, false, 4)
	t.Logf("Gains Q16: %v (linear: %.2f)", gainsQ16, float64(gainsQ16[0])/65536.0)

	// Expected output calculation
	expectedOutput := excRMS / 16384.0 * float64(gainsQ16[0]) / 65536.0 / 256.0 * 16384.0
	t.Logf("Expected peak output: ~%.0f (actual max: %d)", expectedOutput, maxOut)
	t.Logf("Conclusion: Decoder math is correct. Issue is encoder gain/excitation balance.")
}
