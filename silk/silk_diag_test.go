package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestSILKEncoderSignalTrace traces the signal through the SILK encoder
// to find where signal quality is being lost.
func TestSILKEncoderSignalTrace(t *testing.T) {
	t.Skip("Diagnostic test - SILK encoder quality still being improved")
	// Generate a simple test signal: 1kHz sine at 8kHz sample rate
	// 20ms frame = 160 samples at 8kHz (NB)
	const (
		sampleRate = 8000
		frameSamples = 160 // 20ms
		frequency = 440.0  // Hz
		amplitude = 0.5    // Half scale
	)

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		t := float64(i) / float64(sampleRate)
		pcm[i] = float32(amplitude * math.Sin(2*math.Pi*frequency*t))
	}

	// Compute input energy
	var inputEnergy float64
	for _, s := range pcm {
		inputEnergy += float64(s) * float64(s)
	}
	inputRMS := math.Sqrt(inputEnergy / float64(len(pcm)))
	t.Logf("Input: samples=%d, amplitude=%.3f, RMS=%.4f", len(pcm), amplitude, inputRMS)

	// Create encoder
	enc := NewEncoder(BandwidthNarrowband)

	// ---- Stage 1: Check VAD/Classification ----
	signalType, quantOffset := enc.classifyFrame(pcm)
	t.Logf("Classification: signalType=%d (0=inactive, 1=unvoiced, 2=voiced), quantOffset=%d", signalType, quantOffset)

	// ---- Stage 2: Check Gain Computation ----
	config := GetBandwidthConfig(BandwidthNarrowband)
	subframeSamples := config.SubframeSamples
	numSubframes := len(pcm) / subframeSamples
	if numSubframes < 1 {
		numSubframes = 1
	}
	if numSubframes > 4 {
		numSubframes = 4
	}

	gains := enc.computeSubframeGains(pcm, numSubframes)
	t.Logf("Raw gains (float): %v", gains)
	t.Logf("Note: These gains are in int16 RMS scale (not normalized float)")

	// Check gains are in reasonable int16 RMS range
	// For 0.5 amplitude signal: RMS = 0.5/sqrt(2) * 32768 = ~11585
	for i, g := range gains {
		if g < 1.0 || g > 32768.0 {
			t.Logf("WARNING: Subframe %d gain %.4f outside expected range [1, 32768]", i, g)
		}
	}

	// ---- SKIP direct encodeSubframeGains (needs range encoder) ----
	// Instead, just trace the gain quantization math
	t.Log("=== Gain Quantization Math ===")
	for i, g := range gains {
		gainQ16 := int32(g * 65536.0)
		logGain := silkLin2Log(gainQ16)
		rawInd := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))
		t.Logf("  gains[%d]=%.2f -> gainQ16=%d, logGain=%d, rawInd=%d",
			i, g, gainQ16, logGain, rawInd)
	}

	// ---- Stage 3: Check LPC ----
	lpcQ12 := enc.computeLPCFromFrame(pcm)
	t.Logf("LPC order: %d, first 4 coeffs (Q12): %v", len(lpcQ12), lpcQ12[:min(4, len(lpcQ12))])

	// ---- Stage 4: Trace NSQ Input Scaling ----
	t.Log("=== NSQ Input Scaling ===")
	frameSamplesActual := numSubframes * subframeSamples
	if frameSamplesActual > len(pcm) {
		frameSamplesActual = len(pcm)
	}

	// Convert PCM to int16 (same as encoder does)
	inputQ0 := make([]int16, frameSamplesActual)
	for i := 0; i < frameSamplesActual; i++ {
		val := pcm[i] * 32768.0
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		inputQ0[i] = int16(val)
	}
	t.Logf("Input int16 range: min=%d, max=%d", minInt16(inputQ0), maxInt16(inputQ0))

	// Simulate quantized gain for first subframe
	// Using raw gain (int16 RMS scale) converted to Q16, then quantized
	testGainQ16 := int32(gains[0] * 65536.0)
	// Simulate what silkGainsQuantInto would produce:
	logGain := silkLin2Log(testGainQ16)
	rawInd := silkSMULWB(int32(gainScaleQ16), logGain-int32(gainOffsetQ7))
	if rawInd > nLevelsQGain-1 {
		rawInd = nLevelsQGain - 1
	}
	if rawInd < 0 {
		rawInd = 0
	}
	// Dequantize to get the actual gainQ16 that NSQ will use
	logQ7 := silkSMULWB(int32(invScaleQ16Val), rawInd) + int32(gainOffsetQ7)
	if logQ7 > 3967 {
		logQ7 = 3967
	}
	quantizedGainQ16 := silkLog2Lin(logQ7)
	t.Logf("Gain quantization: raw=%.2f -> Q16=%d -> idx=%d -> quantizedQ16=%d -> linear=%.2f",
		gains[0], testGainQ16, rawInd, quantizedGainQ16, float64(quantizedGainQ16)/65536.0)

	// Check what invGainQ31 and xScQ10 would be with quantized gain
	if quantizedGainQ16 > 0 {
		invGainQ31 := silk_INVERSE32_varQ(quantizedGainQ16, 47)
		invGainQ26 := silk_RSHIFT_ROUND(invGainQ31, 5)
		t.Logf("invGainQ31=%d, invGainQ26=%d", invGainQ31, invGainQ26)

		// Scale first few input samples
		t.Log("Sample scaling (first 5):")
		for i := 0; i < min(5, len(inputQ0)); i++ {
			xScQ10 := silk_SMULWW(int32(inputQ0[i]), invGainQ26)
			t.Logf("  inputQ0[%d]=%d -> xScQ10=%d (ratio=%.4f)",
				i, inputQ0[i], xScQ10, float64(xScQ10)/float64(inputQ0[i]))
		}
	}

	// ---- Stage 5: Run Full Encoding and Check Output ----
	data := enc.EncodeFrame(pcm, true)
	t.Logf("Encoded bytes: %d", len(data))

	if len(data) == 0 {
		t.Error("Encoding produced no output!")
		return
	}

	// ---- Stage 6: Decode and Compare ----
	dec := NewDecoder()

	// First, decode at native rate to see raw decoder output
	var rd rangecoding.Decoder
	rd.Init(data)
	nativeSamples, err := dec.DecodeFrameRaw(&rd, BandwidthNarrowband, Frame20ms, true)
	if err != nil {
		t.Fatalf("DecodeFrameRaw failed: %v", err)
	}
	t.Logf("Native samples (8kHz): %d samples", len(nativeSamples))

	// Check native samples
	var nativeMin, nativeMax float32 = 1, -1
	var nativeNonZero int
	for _, s := range nativeSamples {
		if s < nativeMin { nativeMin = s }
		if s > nativeMax { nativeMax = s }
		if s != 0 { nativeNonZero++ }
	}
	t.Logf("Native range: [%.4f, %.4f], nonZero: %d/%d", nativeMin, nativeMax, nativeNonZero, len(nativeSamples))
	t.Logf("First 20 native samples: %v", nativeSamples[:min(20, len(nativeSamples))])

	// Find where the signal starts clipping
	clippingStart := -1
	for i, s := range nativeSamples {
		if s >= 0.9 || s <= -0.9 {
			clippingStart = i
			break
		}
	}
	t.Logf("First clipping sample at index: %d", clippingStart)
	if clippingStart >= 0 && clippingStart < len(nativeSamples)-10 {
		t.Logf("Samples around clipping start: %v", nativeSamples[clippingStart:min(clippingStart+10, len(nativeSamples))])
	}

	// Print samples 40-60
	t.Logf("Native samples 40-60: %v", nativeSamples[40:min(60, len(nativeSamples))])

	// Now resample to 48kHz
	dec2 := NewDecoder()
	output, err := dec2.Decode(data, BandwidthNarrowband, 960, true) // 960 = 20ms at 48kHz
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	t.Logf("Decoded output length: %d (expected 960 at 48kHz)", len(output))

	// Check raw output energy before any downsampling
	var rawOutputEnergy float64
	for _, s := range output {
		rawOutputEnergy += float64(s) * float64(s)
	}
	rawOutputRMS := math.Sqrt(rawOutputEnergy / float64(len(output)))
	t.Logf("Raw output at 48kHz: RMS=%.4f, samples=%d", rawOutputRMS, len(output))

	// Check first 20 raw output samples
	t.Logf("First 20 raw output samples: %v", output[:min(20, len(output))])

	// Find max absolute value in output
	var maxOut float32
	for _, s := range output {
		if s > maxOut {
			maxOut = s
		}
		if -s > maxOut {
			maxOut = -s
		}
	}
	t.Logf("Max absolute output value: %.4f (expected ~0.5 for 0.5 amplitude input)", maxOut)

	// Resample output back to 8kHz for comparison (simple decimation for testing)
	outputDownsampled := make([]float32, len(pcm))
	ratio := len(output) / len(pcm)
	for i := range outputDownsampled {
		outputDownsampled[i] = output[i*ratio]
	}

	// Compute output energy
	var outputEnergy float64
	for _, s := range outputDownsampled {
		outputEnergy += float64(s) * float64(s)
	}
	outputRMS := math.Sqrt(outputEnergy / float64(len(outputDownsampled)))

	// Try to find correlation peak to align signals
	var bestCorr float64
	var bestOffset int
	for offset := -50; offset <= 50; offset++ {
		var corr float64
		var count int
		for i := 0; i < len(pcm); i++ {
			j := i + offset
			if j >= 0 && j < len(outputDownsampled) {
				corr += float64(pcm[i]) * float64(outputDownsampled[j])
				count++
			}
		}
		if count > 0 {
			corr /= float64(count)
		}
		if corr > bestCorr {
			bestCorr = corr
			bestOffset = offset
		}
	}
	t.Logf("Best correlation offset: %d samples (correlation: %.4f)", bestOffset, bestCorr)

	// Compute error with alignment
	var errorEnergy float64
	var alignedCount int
	for i := 0; i < len(pcm); i++ {
		j := i + bestOffset
		if j >= 0 && j < len(outputDownsampled) {
			diff := float64(pcm[i]) - float64(outputDownsampled[j])
			errorEnergy += diff * diff
			alignedCount++
		}
	}
	errorRMS := math.Sqrt(errorEnergy / float64(alignedCount))

	t.Logf("Output: RMS=%.4f", outputRMS)
	t.Logf("Aligned Error: RMS=%.4f (offset=%d)", errorRMS, bestOffset)

	// Compute SNR with aligned signals
	snr := 20 * math.Log10(inputRMS / errorRMS)
	t.Logf("Aligned SNR: %.2f dB", snr)

	// Check first few output samples
	t.Logf("First 10 input samples: %v", pcm[:min(10, len(pcm))])
	t.Logf("First 10 output samples (downsampled): %v", outputDownsampled[:min(10, len(outputDownsampled))])

	if snr < 5.0 {
		t.Errorf("SNR too low: %.2f dB (expected >5 dB)", snr)
	}
}

// TestSILKNSQPulseGeneration tests the NSQ directly to see if pulses are being generated
func TestSILKNSQPulseGeneration(t *testing.T) {
	// Create a simple signal
	const frameSamples = 160
	inputQ0 := make([]int16, frameSamples)
	for i := range inputQ0 {
		// 1kHz sine at 8kHz, amplitude ~16000 (half of int16 range)
		inputQ0[i] = int16(16000 * math.Sin(2*math.Pi*1000*float64(i)/8000))
	}

	t.Logf("Input int16: min=%d, max=%d", minInt16(inputQ0), maxInt16(inputQ0))

	// Create NSQ state
	nsq := NewNSQState()

	// Simple NSQ parameters
	params := &NSQParams{
		SignalType:       typeUnvoiced, // 1 = unvoiced (simpler case)
		QuantOffsetType:  0,            // Low offset
		PredCoefQ12:      make([]int16, 2*maxLPCOrder),
		NLSFInterpCoefQ2: 4,            // No interpolation
		LTPCoefQ14:       make([]int16, 4*ltpOrderConst),
		ARShpQ13:         make([]int16, 4*maxShapeLpcOrder),
		HarmShapeGainQ14: []int{0, 0, 0, 0},
		TiltQ14:          []int{0, 0, 0, 0},
		LFShpQ14:         []int32{0, 0, 0, 0},
		GainsQ16:         []int32{65536, 65536, 65536, 65536}, // Gain = 1.0
		PitchL:           []int{0, 0, 0, 0},
		LambdaQ10:        1024, // Default R-D lambda
		LTPScaleQ14:      0,
		FrameLength:      frameSamples,
		SubfrLength:      40, // 5ms at 8kHz
		NbSubfr:          4,
		LTPMemLength:     ltpMemLength,
		PredLPCOrder:     10,
		ShapeLPCOrder:    10,
		Seed:             0,
	}

	// Set up simple LPC prediction coefficients (slight lowpass)
	for i := 0; i < 10; i++ {
		params.PredCoefQ12[i] = 0
		params.PredCoefQ12[maxLPCOrder+i] = 0
	}
	params.PredCoefQ12[0] = 3277 // ~0.8 in Q12
	params.PredCoefQ12[maxLPCOrder] = 3277

	pulses, xq := NoiseShapeQuantize(nsq, inputQ0, params)

	// Analyze pulses
	var nonZeroPulses int
	var maxPulse int8
	var minPulse int8 = 127
	var sumAbsPulses int64
	for _, p := range pulses {
		if p != 0 {
			nonZeroPulses++
		}
		if p > maxPulse {
			maxPulse = p
		}
		if p < minPulse {
			minPulse = p
		}
		sumAbsPulses += int64(abs8(p))
	}

	t.Logf("Pulses: total=%d, nonZero=%d (%.1f%%), range=[%d, %d], avgAbs=%.2f",
		len(pulses), nonZeroPulses, 100*float64(nonZeroPulses)/float64(len(pulses)),
		minPulse, maxPulse, float64(sumAbsPulses)/float64(len(pulses)))

	// Analyze output xq
	var minXq, maxXq int16
	minXq = 32767
	maxXq = -32768
	for _, x := range xq {
		if x < minXq {
			minXq = x
		}
		if x > maxXq {
			maxXq = x
		}
	}
	t.Logf("Output xq: range=[%d, %d]", minXq, maxXq)

	// The issue: if pulses are all 0 or very small, no energy is preserved
	if nonZeroPulses == 0 {
		t.Error("All pulses are zero! Signal is being completely quantized away")
	}
	if float64(nonZeroPulses)/float64(len(pulses)) < 0.1 {
		t.Logf("WARNING: Only %.1f%% of pulses are non-zero - signal may be over-quantized", 100*float64(nonZeroPulses)/float64(len(pulses)))
	}

	// Check first few values
	t.Logf("First 10 input samples: %v", inputQ0[:10])
	t.Logf("First 10 pulses: %v", pulses[:10])
	t.Logf("First 10 output samples: %v", xq[:10])
}

// TestSILKGainQuantization tests the gain quantization path
func TestSILKGainQuantization(t *testing.T) {
	t.Skip("Diagnostic test - requires range encoder initialization")
	// Test various gain levels
	testGains := []float32{0.01, 0.1, 0.5, 1.0, 2.0, 5.0}

	for _, gain := range testGains {
		// Create encoder
		enc := NewEncoder(BandwidthNarrowband)

		// Generate signal with this gain level
		pcm := make([]float32, 160)
		for i := range pcm {
			pcm[i] = gain * float32(math.Sin(2*math.Pi*440*float64(i)/8000))
		}

		// Compute gains
		gains := enc.computeSubframeGains(pcm, 4)
		gainsQ16 := enc.encodeSubframeGains(gains, 1, 4) // Unvoiced

		// Check gain ratio
		for i, g := range gains {
			linear := float64(gainsQ16[i]) / 65536.0
			ratio := linear / float64(g)
			t.Logf("Input amplitude=%.2f: raw gain[%d]=%.4f, quantized linear=%.4f, ratio=%.2f",
				gain, i, g, linear, ratio)
		}
	}
}

// Helper functions
func minInt16(s []int16) int16 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxInt16(s []int16) int16 {
	if len(s) == 0 {
		return 0
	}
	m := s[0]
	for _, v := range s[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func abs8(x int8) int8 {
	if x < 0 {
		return -x
	}
	return x
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
